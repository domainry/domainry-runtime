package ratelimit

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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

func TestDatabaseRateLimiterValidatesKeyAndClockRollback(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "rate-limit-input.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	limiter := NewRateLimiter(store)
	if _, err := limiter.Allow(t.Context(), " ", 1, time.Minute); err == nil {
		t.Fatal("empty key accepted")
	}
	if _, err := limiter.Allow(t.Context(), strings.Repeat("x", 256), 1, time.Minute); err == nil {
		t.Fatal("oversized key accepted")
	}
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }
	if decision, err := limiter.Allow(t.Context(), "clock", 1, time.Minute); err != nil || !decision.Allowed {
		t.Fatalf("initial decision=%#v err=%v", decision, err)
	}
	now = now.Add(-time.Hour)
	if decision, err := limiter.Allow(t.Context(), "clock", 1, time.Minute); err != nil || !decision.Allowed || decision.Count != 1 {
		t.Fatalf("rollback decision=%#v err=%v", decision, err)
	}
}

func TestSQLiteRateLimiterSerializesConcurrentBucketUpdates(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "rate-limit-concurrent.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.DB().SetMaxOpenConns(16)

	const requests = 24
	counts := make([]int, 0, requests)
	errorsByRequest := make([]error, requests)
	var mutex sync.Mutex
	var wait sync.WaitGroup
	start := make(chan struct{})
	for index := 0; index < requests; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			decision, allowErr := NewRateLimiter(store).Allow(t.Context(), "workspace:concurrent", requests, time.Minute)
			errorsByRequest[index] = allowErr
			if allowErr == nil {
				mutex.Lock()
				counts = append(counts, decision.Count)
				mutex.Unlock()
			}
		}(index)
	}
	close(start)
	wait.Wait()
	for _, allowErr := range errorsByRequest {
		if allowErr != nil {
			t.Fatal(allowErr)
		}
	}
	sort.Ints(counts)
	if len(counts) != requests {
		t.Fatalf("counts=%v", counts)
	}
	for index, count := range counts {
		if count != index+1 {
			t.Fatalf("counts=%v", counts)
		}
	}
}
