package resilience

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryStoreCircuitRateAndCancellation(t *testing.T) {
	store := NewMemoryStore(2)
	now := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	config := Config{FailureThreshold: 2, Cooldown: time.Minute, RateLimit: 1, RateWindow: time.Minute}
	if err := store.Before(t.Context(), "sync:a", config, now); err != nil {
		t.Fatal(err)
	}
	if err := store.Before(t.Context(), "sync:a", config, now); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("rate error=%v", err)
	}
	_ = store.Record(t.Context(), "sync:a", config, false, now)
	_ = store.Record(t.Context(), "sync:a", config, false, now)
	if err := store.Before(t.Context(), "sync:a", config, now); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("circuit error=%v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.Before(ctx, "cancelled", config, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
	if store.Semantics() != SemanticsInstanceLocal {
		t.Fatalf("unexpected semantics %q", store.Semantics())
	}
}

func TestMemoryStoreReportsEvictionAndIdleExpiration(t *testing.T) {
	store := NewMemoryStore(1)
	store.idleTTL = time.Minute
	now := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	if err := store.Before(t.Context(), "first", Config{RateLimit: 2}, now); err != nil {
		t.Fatal(err)
	}
	if err := store.Before(t.Context(), "second", Config{RateLimit: 2}, now); err != nil {
		t.Fatal(err)
	}
	if stats := store.Stats(); stats.Entries != 1 || stats.Capacity != 1 || stats.Evictions != 1 {
		t.Fatalf("unexpected eviction stats: %+v", stats)
	}
	now = now.Add(2 * time.Minute)
	if err := store.Before(t.Context(), "third", Config{RateLimit: 2}, now); err != nil {
		t.Fatal(err)
	}
	if stats := store.Stats(); stats.Entries != 1 || stats.Expirations != 1 {
		t.Fatalf("unexpected expiration stats: %+v", stats)
	}
}
