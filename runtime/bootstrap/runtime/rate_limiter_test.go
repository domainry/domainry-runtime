package runtime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/domainry/domainry-foundation/ratelimit"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	ratelimitpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/ratelimit"
	ratelimitredis "github.com/domainry/domainry-runtime/runtime/infrastructure/ratelimitredis"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type closeTrackingRateLimiter struct{ closed bool }

func (*closeTrackingRateLimiter) Allow(context.Context, string, int, time.Duration) (ratelimit.Decision, error) {
	return ratelimit.Decision{Allowed: true}, nil
}

func (l *closeTrackingRateLimiter) Close() error { l.closed = true; return nil }

func TestOpenSharedRateLimiterSelectsConfiguredBackend(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "rate-limit-factory.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}

	databaseLimiter, err := openSharedRateLimiter(t.Context(), config.Config{RateLimitBackend: "database"}, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := databaseLimiter.(*ratelimitpersistence.RateLimiter); !ok {
		t.Fatalf("database limiter=%T", databaseLimiter)
	}

	server := miniredis.RunT(t)
	redisLimiter, err := openSharedRateLimiter(t.Context(), config.Config{
		RateLimitBackend:             "redis",
		RateLimitRedisURL:            "redis://" + server.Addr() + "/0",
		RateLimitRedisPrefix:         "factory:rate:",
		RateLimitRedisConnectTimeout: time.Second,
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := redisLimiter.(*ratelimitredis.RateLimiter); !ok {
		t.Fatalf("Redis limiter=%T", redisLimiter)
	}
	if closer, ok := redisLimiter.(interface{ Close() error }); !ok {
		t.Fatal("Redis limiter is not closeable")
	} else if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeCloseClosesRateLimiterForOwnedAndBorrowedStores(t *testing.T) {
	for _, borrowed := range []bool{false, true} {
		store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "rate-limit-close.db")})
		if err != nil {
			t.Fatal(err)
		}
		limiter := &closeTrackingRateLimiter{}
		runtime := &Runtime{cfg: config.Config{HTTPShutdownTimeout: time.Second}, store: store, borrowedStore: borrowed, rateLimiter: limiter}
		if err := runtime.CloseContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !limiter.closed {
			t.Fatalf("borrowed=%v limiter not closed", borrowed)
		}
		if borrowed {
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestOpenSharedRateLimiterRejectsInvalidConfiguration(t *testing.T) {
	for _, cfg := range []config.Config{{RateLimitBackend: "redis", RateLimitRedisConnectTimeout: time.Second}, {RateLimitBackend: "unknown"}} {
		if _, err := openSharedRateLimiter(t.Context(), cfg, nil); err == nil || !strings.Contains(err.Error(), "RATE_LIMIT_") {
			t.Fatalf("config=%#v error=%v", cfg, err)
		}
	}
}
