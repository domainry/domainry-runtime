package principalcache

import (
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	principalredis "github.com/domainry/domainry-runtime/runtime/infrastructure/principalredis"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestOpenSelectsConfiguredBackend(t *testing.T) {
	local, err := Open(t.Context(), config.Config{PrincipalCacheBackend: "local"})
	if err != nil || local == nil {
		t.Fatalf("local cache=%T err=%v", local, err)
	}

	server := miniredis.RunT(t)
	shared, err := Open(t.Context(), config.Config{
		PrincipalCacheBackend:             "redis",
		PrincipalCacheRedisURL:            "redis://" + server.Addr() + "/0",
		PrincipalCacheRedisPrefix:         "factory:principal:",
		PrincipalCacheRedisConnectTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := shared.(*principalredis.Cache); !ok {
		t.Fatalf("Redis cache=%T", shared)
	}
	if closer, ok := shared.(interface{ Close() error }); !ok {
		t.Fatal("Redis cache is not closeable")
	} else if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRejectsInvalidConfiguration(t *testing.T) {
	for _, cfg := range []config.Config{
		{PrincipalCacheBackend: "local", PrincipalCacheTTL: -time.Second},
		{PrincipalCacheBackend: "redis", PrincipalCacheRedisConnectTimeout: time.Second},
		{PrincipalCacheBackend: "unknown"},
	} {
		if _, err := Open(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "PRINCIPAL_CACHE") {
			t.Fatalf("config=%#v error=%v", cfg, err)
		}
	}
}
