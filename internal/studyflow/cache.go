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
	Generation(ctx context.Context, userID string) int64
	Bump(ctx context.Context, userID string)
}

// noopCache stands in when caching is switched off, so no call site has to test
// for a nil cache.
type noopCache struct{}

func (noopCache) Read(context.Context, string, any) bool    { return false }
func (noopCache) Write(context.Context, string, any)        {}
func (noopCache) Generation(context.Context, string) int64  { return 0 }
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

func taskListKey(userID string, generation int64) string {
	return fmt.Sprintf("%s:tasklist:%s:%d", cacheKeyPrefix, userID, generation)
}

func weeklyReviewKey(userID, weekStart string, generation int64) string {
	return fmt.Sprintf("%s:review:%s:%s:%d", cacheKeyPrefix, userID, weekStart, generation)
}
