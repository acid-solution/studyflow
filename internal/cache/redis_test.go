package cache

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestCacheSafetyWindow(t *testing.T) {
	if got := cacheSafetyWindow(30 * time.Second); got != time.Minute {
		t.Fatalf("safety window = %v, want 1m", got)
	}
	if got := cacheSafetyWindow(0); got <= 0 {
		t.Fatalf("non-positive cache ttl must still produce a safe window, got %v", got)
	}
}

func TestUnavailableBumpExtendsQuarantine(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store := &Redis{generationTTL: time.Minute, now: func() time.Time { return now }}
	store.ready.Store(true)

	store.Bump(context.Background(), "user-1")
	if store.ready.Load() {
		t.Fatal("an unconfirmed invalidation must disable cache access")
	}
	if got, want := store.unsafeUntil.Load(), now.Add(time.Minute).UnixNano(); got != want {
		t.Fatalf("unsafe deadline = %d, want %d", got, want)
	}

	now = now.Add(30 * time.Second)
	store.Bump(context.Background(), "user-1")
	if got, want := store.unsafeUntil.Load(), now.Add(time.Minute).UnixNano(); got != want {
		t.Fatalf("a later write must extend quarantine: got %d, want %d", got, want)
	}
	if store.safeToEnable() {
		t.Fatal("cache became safe before the extended deadline")
	}
	now = now.Add(time.Minute)
	if !store.safeToEnable() {
		t.Fatal("cache should become safe after the extended deadline")
	}
}

func TestRedisGenerationLifecycleIntegration(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR is not set")
	}

	store := New(addr, "", 0, 200*time.Millisecond, nil)
	defer store.Close()
	if err := store.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	if store.Ready() {
		t.Fatal("a new process must stay quarantined until old cache entries expire")
	}
	store.unsafeUntil.Store(store.now().Add(-time.Second).UnixNano())
	if err := store.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !store.Ready() {
		t.Fatal("cache should be ready after the test bypasses startup quarantine")
	}

	userID := fmt.Sprintf("redis-test-%d", time.Now().UnixNano())
	dataKey := "sf:test:data:" + userID
	staleKey := "sf:test:stale:" + userID
	currentKey := "sf:test:current:" + userID
	genKey := generationPrefix + userID
	defer store.client.Del(context.Background(), dataKey, staleKey, currentKey, genKey)

	generation, known := store.Generation(t.Context(), userID)
	if !known || generation != 0 {
		t.Fatalf("initial generation = %d, known=%v", generation, known)
	}
	store.Write(t.Context(), userID, generation, dataKey, map[string]string{"value": "initial"})
	var cached map[string]string
	if !store.Read(t.Context(), dataKey, &cached) || cached["value"] != "initial" {
		t.Fatalf("initial cache write was not readable: %#v", cached)
	}

	store.Bump(t.Context(), userID)
	currentGeneration, known := store.Generation(t.Context(), userID)
	if !known || currentGeneration != 1 {
		t.Fatalf("generation after bump = %d, known=%v", currentGeneration, known)
	}
	store.Write(t.Context(), userID, generation, staleKey, map[string]string{"value": "stale"})
	if store.Read(t.Context(), staleKey, &cached) {
		t.Fatal("a stale database read repopulated the cache after invalidation")
	}
	store.Write(t.Context(), userID, currentGeneration, currentKey, map[string]string{"value": "current"})
	if !store.Read(t.Context(), currentKey, &cached) || cached["value"] != "current" {
		t.Fatalf("current generation write was not readable: %#v", cached)
	}

	dataTTL, err := store.client.PTTL(t.Context(), currentKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	generationTTL, err := store.client.PTTL(t.Context(), genKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if generationTTL <= dataTTL {
		t.Fatalf("generation ttl %v must outlive data ttl %v", generationTTL, dataTTL)
	}

	time.Sleep(260 * time.Millisecond)
	if exists := store.client.Exists(t.Context(), currentKey).Val(); exists != 0 {
		t.Fatal("data key did not expire")
	}
	if exists := store.client.Exists(t.Context(), genKey).Val(); exists != 1 {
		t.Fatal("generation key expired before the data key safety window")
	}
	time.Sleep(220 * time.Millisecond)
	if exists := store.client.Exists(t.Context(), genKey).Val(); exists != 0 {
		t.Fatal("inactive generation key did not expire")
	}
}
