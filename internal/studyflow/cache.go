package studyflow

import (
	"context"
	"fmt"
)

// Cache is the read-model cache the service uses for its heaviest aggregate
// reads.
//
// Invalidation works by generation, not by deletion: every key embeds the
// caller's current generation, so one INCR after a write makes every cached read
// for that user unreachable at once. That matters because a goal tree is touched
// by around eighteen write paths — deleting individual keys from each of them is
// eighteen chances to miss one.
//
// Implementations must be fail-open: a backend problem has to look like a miss,
// never like an error.
type Cache interface {
	Read(ctx context.Context, key string, target any) bool
	Write(ctx context.Context, key string, value any)
	// Generation reports the caller's current generation and whether it could be
	// read at all. A caller that gets false has to skip the cache entirely rather
	// than fall back to zero, because zero is a real generation that entries were
	// written under.
	Generation(ctx context.Context, userID string) (int64, bool)
	Bump(ctx context.Context, userID string)
}

// noopCache stands in when caching is switched off, so no call site has to test
// for a nil cache.
type noopCache struct{}

func (noopCache) Read(context.Context, string, any) bool    { return false }
func (noopCache) Write(context.Context, string, any)        {}
func (noopCache) Generation(context.Context, string) (int64, bool) { return 0, false }
func (noopCache) Bump(context.Context, string)              {}

const cacheKeyPrefix = "sf:v1"

func goalTreeKey(userID string, generation int64) string {
	return fmt.Sprintf("%s:goaltree:%s:%d", cacheKeyPrefix, userID, generation)
}

// invalidate advances the user's cache generation. Call it once a write has
// committed; everything cached for that user becomes unreachable immediately and
// is reclaimed by its TTL.
func (s *Service) invalidate(ctx context.Context, userID string) {
	s.cache.Bump(ctx, userID)
}

func taskListKey(userID, today string, generation int64) string {
	return fmt.Sprintf("%s:tasklist:%s:%s:%d", cacheKeyPrefix, userID, today, generation)
}

func weeklyReviewKey(userID, weekStart string, generation int64) string {
	return fmt.Sprintf("%s:review:%s:%s:%d", cacheKeyPrefix, userID, weekStart, generation)
}
