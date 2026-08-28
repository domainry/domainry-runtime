package ratelimit

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestContextRateLimiterSharedBucketAndCancellation(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "rate-limit.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	first := NewRateLimiter(store)
	second := NewRateLimiter(store)
	first.now = func() time.Time { return now }
	second.now = func() time.Time { return now }
	decision, err := first.Allow(t.Context(), "agent:user", 2, time.Minute)
	if err != nil || !decision.Allowed || decision.Count != 1 {
		t.Fatalf("first=%#v err=%v", decision, err)
	}
	decision, err = second.Allow(t.Context(), "agent:user", 2, time.Minute)
	if err != nil || !decision.Allowed || decision.Count != 2 {
		t.Fatalf("second=%#v err=%v", decision, err)
	}
	decision, err = first.Allow(t.Context(), "agent:user", 2, time.Minute)
	if err != nil || decision.Allowed || decision.Count != 3 || decision.RetryAfter != time.Minute {
		t.Fatalf("limited=%#v err=%v", decision, err)
	}
	now = now.Add(time.Minute)
	decision, err = second.Allow(t.Context(), "agent:user", 2, time.Minute)
	if err != nil || !decision.Allowed || decision.Count != 1 {
		t.Fatalf("reset=%#v err=%v", decision, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := first.Allow(cancelled, "agent:user", 2, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
