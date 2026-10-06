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

// A generation key lives twice as long as a data entry and its TTL is refreshed
// by every generation read, bump and successful cache write. It therefore
// disappears after an inactive user while still outliving every key that could
// collide when the counter later starts again at zero.
const generationPrefix = "sf:v1:gen:"

// opTimeout bounds a single Redis round trip.
const opTimeout = 200 * time.Millisecond

// watchInterval is how often an unreachable backend is retried.
const watchInterval = 15 * time.Second

type Redis struct {
	client        *redis.Client
	ttl           time.Duration
	generationTTL time.Duration
	logger        *slog.Logger
	reachable     atomic.Bool
	ready         atomic.Bool
	unsafeUntil   atomic.Int64
	now           func() time.Time
}

func New(addr, password string, database int, ttl time.Duration, logger *slog.Logger) *Redis {
	now := time.Now
	store := &Redis{
		client:        redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: database}),
		ttl:           ttl,
		generationTTL: cacheSafetyWindow(ttl),
		logger:        logger,
		now:           now,
	}
	// A new process cannot know whether its predecessor committed a database
	// write while Redis was unavailable. Serve from MySQL until every old data
	// entry must have expired; this also covers a crash immediately after a
	// failed invalidation.
	store.unsafeUntil.Store(now().Add(store.generationTTL).UnixNano())
	return store
}

// Ping checks the backend once and records the result. Until it has succeeded the
// cache reports every read as a miss, so a backend that is down costs nothing per
// request instead of one timeout each.
func (r *Redis) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := r.client.Ping(ctx).Err(); err != nil {
		r.reachable.Store(false)
		r.ready.Store(false)
		return err
	}
	r.reachable.Store(true)
	r.ready.Store(r.safeToEnable())
	return nil
}

// Ready reports whether cache reads and writes are currently allowed. Redis can
// be reachable while still quarantined after an uncertain invalidation.
func (r *Redis) Ready() bool { return r.ready.Load() }

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
				case err == nil && r.ready.Load() && !was:
					r.info("cache quarantine elapsed, enabling")
				case err != nil && was:
					r.warn("ping", err)
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

func (r *Redis) Write(ctx context.Context, userID string, generation int64, key string, value any) {
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
	dataTTL := jittered(r.ttl)
	if err := writeIfCurrentScript.Run(ctx, r.client, []string{generationPrefix + userID, key}, generation, raw, dataTTL.Milliseconds(), r.generationTTL.Milliseconds()).Err(); err != nil {
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
	value, err := readGenerationScript.Run(ctx, r.client, []string{generationPrefix + userID}, r.generationTTL.Milliseconds()).Int64()
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

// Bump advances the user's generation. A bump that cannot be confirmed places
// the whole cache in quarantine. Every later write extends that quarantine while
// Redis is unreachable, so recovery cannot expose an entry created before the
// latest committed database change.
func (r *Redis) Bump(ctx context.Context, userID string) {
	if !r.reachable.Load() {
		r.quarantine()
		return
	}
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(ctx, opTimeout)
		err := bumpGenerationScript.Run(ctx, r.client, []string{generationPrefix + userID}, r.generationTTL.Milliseconds()).Err()
		cancel()
		if err == nil {
			return
		}
		if attempt == 1 {
			r.warn("bump", err)
			r.reachable.Store(false)
			r.quarantine()
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

func cacheSafetyWindow(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return time.Second
	}
	return 2 * ttl
}

func (r *Redis) safeToEnable() bool {
	return r.now().UnixNano() >= r.unsafeUntil.Load()
}

func (r *Redis) quarantine() {
	deadline := r.now().Add(r.generationTTL).UnixNano()
	for {
		current := r.unsafeUntil.Load()
		if current >= deadline || r.unsafeUntil.CompareAndSwap(current, deadline) {
			break
		}
	}
	r.ready.Store(false)
}

func (r *Redis) info(message string, args ...any) {
	if r.logger != nil {
		r.logger.Info(message, args...)
	}
}

func (r *Redis) warn(action string, err error) {
	if r.logger != nil {
		r.logger.Warn("cache unavailable", "action", action, "error", err)
	}
}

var readGenerationScript = redis.NewScript(`
local value = redis.call("GET", KEYS[1])
if not value then
  return nil
end
redis.call("PEXPIRE", KEYS[1], ARGV[1])
return value
`)

var bumpGenerationScript = redis.NewScript(`
local value = redis.call("INCR", KEYS[1])
redis.call("PEXPIRE", KEYS[1], ARGV[1])
return value
`)

var writeIfCurrentScript = redis.NewScript(`
local current = redis.call("GET", KEYS[1])
if not current then
  current = "0"
end
if current ~= ARGV[1] then
  return 0
end
redis.call("SET", KEYS[2], ARGV[2], "PX", ARGV[3])
if redis.call("EXISTS", KEYS[1]) == 0 then
  redis.call("SET", KEYS[1], "0", "PX", ARGV[4])
else
  redis.call("PEXPIRE", KEYS[1], ARGV[4])
end
return 1
`)
