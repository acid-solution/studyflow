// Package cache implements the studyflow.Cache port on top of Redis.
//
// Every method is fail-open. If Redis is unreachable or slow, the caller sees a
// cache miss and falls back to the database: a cache must never be able to fail
// a request. Each operation carries its own short timeout so a hung backend
// cannot stall a request either.
//
// The one thing fail-open must not do is look like a valid answer. A failed
// generation read reports "unknown" rather than zero, because zero is a real
// generation that entries were written under.
package cache

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// generationPrefix is deliberately never expired. If the counter were evicted it
// would restart at zero, and keys written under an older generation could become
// reachable again before their own TTL runs out.
const generationPrefix = "sf:v1:gen:"

// opTimeout bounds a single Redis round trip.
const opTimeout = 200 * time.Millisecond

// watchInterval is how often an unreachable backend is retried.
const watchInterval = 15 * time.Second

type Redis struct {
	client *redis.Client
	ttl    time.Duration
	logger *slog.Logger
	ready  atomic.Bool
}

func New(addr, password string, database int, ttl time.Duration, logger *slog.Logger) *Redis {
	return &Redis{
		client: redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: database}),
		ttl:    ttl,
		logger: logger,
	}
}

// Ping checks the backend once and records the result. Until it has succeeded the
// cache reports every read as a miss, so a backend that is down costs nothing per
// request instead of one timeout each.
func (r *Redis) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := r.client.Ping(ctx).Err(); err != nil {
		r.ready.Store(false)
		return err
	}
	r.ready.Store(true)
	return nil
}

// Watch keeps retrying an unreachable backend until it comes back. Without it a
// Redis that is merely slow to start would leave caching disabled for the whole
// life of the process, and the health check — which only looks at MySQL — would
// never notice.
func (r *Redis) Watch(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(watchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				was := r.ready.Load()
				err := r.Ping(ctx)
				switch {
				case err == nil && !was:
					r.logger.Info("cache reachable again, enabling")
				case err != nil && was:
					r.logger.Warn("cache became unreachable, serving from mysql", "error", err)
				}
			}
		}
	}()
}

func (r *Redis) Read(ctx context.Context, key string, target any) bool {
	if !r.ready.Load() {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	raw, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		if err != redis.Nil {
			r.warn("read", err)
		}
		return false
	}
	if err := json.Unmarshal(raw, target); err != nil {
		r.warn("decode", err)
		return false
	}
	return true
}

func (r *Redis) Write(ctx context.Context, key string, value any) {
	if !r.ready.Load() {
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		r.warn("encode", err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	if err := r.client.Set(ctx, key, raw, jittered(r.ttl)).Err(); err != nil {
		r.warn("write", err)
	}
}

// Generation reports the caller's generation, and whether it could be read at
// all. The second result matters: zero is a real generation, so returning it for
// an unreachable backend would let a read hit entries written before the user's
// first write — exactly the staleness invalidation exists to prevent.
func (r *Redis) Generation(ctx context.Context, userID string) (int64, bool) {
	if !r.ready.Load() {
		return 0, false
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	value, err := r.client.Get(ctx, generationPrefix+userID).Int64()
	if err == redis.Nil {
		// No counter yet means this user has never written: generation zero.
		return 0, true
	}
	if err != nil {
		r.warn("generation", err)
		return 0, false
	}
	return value, true
}

// Bump advances the user's generation. A bump that does not land means the
// invalidation did not happen, so this retries once and then reports the backend
// as unready — reads stop being served from the cache until it comes back, which
// bounds the stale window to the entries' TTL instead of leaving it open.
func (r *Redis) Bump(ctx context.Context, userID string) {
	if !r.ready.Load() {
		return
	}
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(ctx, opTimeout)
		err := r.client.Incr(ctx, generationPrefix+userID).Err()
		cancel()
		if err == nil {
			return
		}
		if attempt == 1 {
			r.warn("bump", err)
			r.ready.Store(false)
		}
	}
}

// Close releases the connection pool.
func (r *Redis) Close() error { return r.client.Close() }

// jittered spreads expirations by ±10% so that keys written together do not all
// expire in the same instant and stampede the database.
func jittered(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return ttl
	}
	spread := int64(ttl / 5)
	return ttl*9/10 + time.Duration(rand.Int64N(spread))
}

func (r *Redis) warn(action string, err error) {
	if r.logger != nil {
		r.logger.Warn("cache unavailable", "action", action, "error", err)
	}
}
