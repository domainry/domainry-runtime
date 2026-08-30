package ratelimitredis

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
)

func TestRateLimiterFixedWindowAndSharedClient(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	first := New(client, "test:rate:")
	second := New(client, "test:rate:")

	for request := 1; request <= 3; request++ {
		limiter := first
		if request%2 == 0 {
			limiter = second
		}
		decision, err := limiter.Allow(t.Context(), "workspace:a", 2, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Count != request || decision.Allowed != (request <= 2) {
			t.Fatalf("request=%d decision=%#v", request, decision)
		}
		if request == 3 && (decision.RetryAfter <= 0 || decision.RetryAfter > time.Minute) {
			t.Fatalf("retry after=%s", decision.RetryAfter)
		}
	}
	server.FastForward(time.Minute)
	decision, err := first.Allow(t.Context(), "workspace:a", 2, time.Minute)
	if err != nil || !decision.Allowed || decision.Count != 1 {
		t.Fatalf("reset decision=%#v err=%v", decision, err)
	}
}

func TestRateLimiterInputAndKeyContract(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	limiter := New(client, "test:rate:")

	if _, err := limiter.Allow(t.Context(), " ", 1, time.Minute); err == nil {
		t.Fatal("empty key accepted")
	}
	decision, err := limiter.Allow(t.Context(), "ignored", 0, time.Minute)
	if err != nil || !decision.Allowed {
		t.Fatalf("disabled decision=%#v err=%v", decision, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := limiter.Allow(cancelled, "key", 1, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
	longKey := strings.Repeat("x", 4096)
	if _, err := limiter.Allow(t.Context(), longKey, 1, time.Minute); err != nil {
		t.Fatal(err)
	}
	keys := server.Keys()
	if len(keys) != 1 || len(keys[0]) != len("test:rate:")+64 {
		t.Fatalf("keys=%v", keys)
	}
}

func TestOpenValidatesAndPingsRedis(t *testing.T) {
	server := miniredis.RunT(t)
	limiter, err := Open(t.Context(), "redis://"+server.Addr()+"/0", "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := limiter.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(t.Context(), "://bad", "", time.Second); err == nil {
		t.Fatal("invalid URL accepted")
	}
}
