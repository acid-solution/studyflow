// Package cache implements the studyflow.Cache port on top of Redis.
//
// Every method is fail-open. If Redis is unreachable or slow, the caller sees a
// cache miss and falls back to the database: a cache must never be able to fail
// a request. Each operation carries its own short timeout so a hung backend
// cannot stall a request either.
package cache

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/redis/go-redis/v9"
)

// generationPrefix is deliberately never expired. If the counter were evicted it
// would restart at zero, and keys written under an older generation could become
// reachable again before their own TTL runs out.
const generationPrefix = "sf:v1:gen:"

// opTimeout bounds a single Redis round trip.
const opTimeout = 200 * time.Millisecond

type Redis struct {
	client *redis.Client
	ttl    time.Duration
	logger *slog.Logger
}

func New(addr, password string, database int, ttl time.Duration, logger *slog.Logger) *Redis {
	return &Redis{
		client: redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: database}),
		ttl:    ttl,
		logger: logger,
	}
}

// Ping reports whether the backend is reachable. Startup uses it to decide
// whether to enable caching at all rather than discovering it per request.
func (r *Redis) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return r.client.Ping(ctx).Err()
}

func (r *Redis) Read(ctx context.Context, key string, target any) bool {
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

func (r *Redis) Generation(ctx context.Context, userID string) int64 {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	value, err := r.client.Get(ctx, generationPrefix+userID).Int64()
	if err != nil {
		if err != redis.Nil {
			r.warn("generation", err)
		}
		return 0
	}
	return value
}

func (r *Redis) Bump(ctx context.Context, userID string) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	if err := r.client.Incr(ctx, generationPrefix+userID).Err(); err != nil {
		r.warn("bump", err)
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
