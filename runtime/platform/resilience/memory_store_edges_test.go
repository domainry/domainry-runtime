package resilience

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryStoreDefaultsAndRecordTransitions(t *testing.T) {
	now := time.Date(2026, time.July, 20, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore(0)
	if stats := store.Stats(); stats.Capacity != DefaultMemoryCapacity {
		t.Fatalf("default capacity=%d", stats.Capacity)
	}
	var nilStore *MemoryStore
	if stats := nilStore.Stats(); stats != (Stats{}) {
		t.Fatalf("nil stats=%+v", stats)
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.Record(cancelled, "cancelled", Config{}, true, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled record error=%v", err)
	}

	store.states["job"] = state{failureCount: 2, openUntil: now.Add(time.Minute)}
	if err := store.Record(t.Context(), "job", Config{}, true, now); err != nil {
		t.Fatal(err)
	}
	if current := store.states["job"]; current.failureCount != 0 || !current.openUntil.IsZero() {
		t.Fatalf("successful record did not reset circuit: %+v", current)
	}

	config := Config{FailureThreshold: 1}
	if err := store.Record(t.Context(), "failed", config, false, now); err != nil {
		t.Fatal(err)
	}
	if got := store.states["failed"].openUntil; !got.Equal(now.Add(time.Minute)) {
		t.Fatalf("default cooldown deadline=%s", got)
	}
	if err := store.Record(t.Context(), "below-threshold", Config{FailureThreshold: 2}, false, now); err != nil {
		t.Fatal(err)
	}
	if got := store.states["below-threshold"].openUntil; !got.IsZero() {
		t.Fatalf("circuit opened below threshold: %s", got)
	}
	if err := store.Record(t.Context(), "disabled-circuit", Config{}, false, now); err != nil {
		t.Fatal(err)
	}
	if got := store.states["disabled-circuit"].openUntil; !got.IsZero() {
		t.Fatalf("disabled circuit opened: %s", got)
	}
}

func TestMemoryStoreIntervalsWindowsAndExistingEntry(t *testing.T) {
	now := time.Date(2026, time.July, 20, 11, 0, 0, 0, time.UTC)
	store := NewMemoryStore(2)
	if err := store.Before(t.Context(), "fresh-interval", Config{MinInterval: time.Minute}, now); err != nil {
		t.Fatalf("fresh minimum interval error=%v", err)
	}
	if err := store.Record(t.Context(), "interval", Config{}, true, now); err != nil {
		t.Fatal(err)
	}
	if err := store.Before(t.Context(), "interval", Config{MinInterval: time.Minute}, now.Add(30*time.Second)); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("active minimum interval error=%v", err)
	}
	if err := store.Before(t.Context(), "interval", Config{MinInterval: time.Minute}, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("expired minimum interval error=%v", err)
	}

	rate := Config{RateLimit: 1, RateWindow: time.Minute}
	if err := store.Before(t.Context(), "window", rate, now); err != nil {
		t.Fatal(err)
	}
	if err := store.Before(t.Context(), "window", rate, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("rolled rate window error=%v", err)
	}
	if err := store.Before(t.Context(), "default-window", Config{RateLimit: 2}, now); err != nil {
		t.Fatal(err)
	}

	bounded := NewMemoryStore(1)
	bounded.put("same", state{lastSeen: now})
	bounded.put("same", state{lastSeen: now.Add(time.Minute)})
	if stats := bounded.Stats(); stats.Entries != 1 || stats.Evictions != 0 {
		t.Fatalf("existing entry update stats=%+v", stats)
	}

	zeroCapacity := &MemoryStore{states: map[string]state{}}
	zeroCapacity.put("first", state{lastSeen: now})
	if stats := zeroCapacity.Stats(); stats.Entries != 1 || stats.Evictions != 0 {
		t.Fatalf("zero-capacity direct store stats=%+v", stats)
	}
}

func TestMemoryStoreExpirationProtectionAndOldestEviction(t *testing.T) {
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore(4)
	store.idleTTL = 0
	store.states["kept"] = state{lastSeen: now.Add(-time.Hour)}
	store.evictExpired(now)
	if _, found := store.states["kept"]; !found {
		t.Fatal("disabled expiration removed entry")
	}

	store.idleTTL = time.Minute
	store.states = map[string]state{
		"zero":   {},
		"open":   {lastSeen: now.Add(-time.Hour), openUntil: now.Add(time.Hour)},
		"closed": {lastSeen: now.Add(-time.Hour)},
	}
	store.evictExpired(now)
	if _, found := store.states["closed"]; found {
		t.Fatal("expired closed entry was retained")
	}
	if _, found := store.states["zero"]; !found {
		t.Fatal("zero last-seen entry was removed")
	}
	if _, found := store.states["open"]; !found {
		t.Fatal("open circuit was removed")
	}

	bounded := NewMemoryStore(4)
	bounded.states = map[string]state{
		"newest": {lastSeen: now},
		"oldest": {lastSeen: now.Add(-3 * time.Hour)},
		"middle": {lastSeen: now.Add(-time.Hour)},
		"later":  {lastSeen: now.Add(time.Hour)},
	}
	bounded.put("replacement", state{lastSeen: now.Add(2 * time.Hour)})
	if _, found := bounded.states["oldest"]; found {
		t.Fatal("oldest entry was not evicted")
	}
	if stats := bounded.Stats(); stats.Entries != 4 || stats.Evictions != 1 {
		t.Fatalf("eviction stats=%+v", stats)
	}

	// Rebuild the map for each run so its iteration seed varies. The invariant
	// remains deterministic: regardless of traversal order, the oldest entry
	// must be the one removed.
	for iteration := 0; iteration < 64; iteration++ {
		repeated := NewMemoryStore(8)
		for index := 0; index < repeated.capacity; index++ {
			repeated.states[string(rune('a'+index))] = state{lastSeen: now.Add(time.Duration(index) * time.Minute)}
		}
		repeated.put("replacement", state{lastSeen: now.Add(time.Hour)})
		if _, found := repeated.states["a"]; found {
			t.Fatalf("iteration %d retained oldest entry", iteration)
		}
	}
}
