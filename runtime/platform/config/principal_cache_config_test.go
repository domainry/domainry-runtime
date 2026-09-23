package config

import (
	"strings"
	"testing"
	"time"
)

func TestIdentityPrincipalCacheConfigDefaultsToLocalFiveMinutes(t *testing.T) {
	cfg := FromEnv()
	if cfg.PrincipalCacheBackend != "local" || cfg.PrincipalCacheTTL != 5*time.Minute || cfg.PrincipalCacheRedisPrefix != "domainry:identity:principal:v1:" {
		t.Fatalf("config=%#v", cfg)
	}
}

func TestIdentityPrincipalCacheConfigFromEnvironment(t *testing.T) {
	t.Setenv("PRINCIPAL_CACHE_BACKEND", "redis")
	t.Setenv("PRINCIPAL_CACHE_TTL", "5m")
	t.Setenv("PRINCIPAL_CACHE_REDIS_URL", "redis://localhost:6379/3")
	t.Setenv("PRINCIPAL_CACHE_REDIS_PREFIX", "tenant:principal:")
	t.Setenv("PRINCIPAL_CACHE_REDIS_CONNECT_TIMEOUT", "3s")
	t.Setenv("PRINCIPAL_CACHE_REDIS_CLUSTER", "true")
	cfg := FromEnv()
	if cfg.PrincipalCacheBackend != "redis" || cfg.PrincipalCacheTTL != 5*time.Minute || cfg.PrincipalCacheRedisURL != "redis://localhost:6379/3" || cfg.PrincipalCacheRedisPrefix != "tenant:principal:" || cfg.PrincipalCacheRedisConnectTimeout != 3*time.Second || !cfg.PrincipalCacheRedisCluster {
		t.Fatalf("config=%#v", cfg)
	}
}

func TestIdentityPrincipalCacheConfigValidation(t *testing.T) {
	valid := Config{PrincipalCacheBackend: "redis", PrincipalCacheTTL: 5 * time.Minute, PrincipalCacheRedisURL: "redis://localhost:6379", PrincipalCacheRedisConnectTimeout: time.Second}
	if err := valid.Validate(); err != nil && strings.Contains(err.Error(), "PRINCIPAL_CACHE") {
		t.Fatalf("valid Redis config rejected: %v", err)
	}
	for _, cfg := range []Config{
		{PrincipalCacheBackend: "local", PrincipalCacheTTL: -time.Second},
		{PrincipalCacheBackend: "redis", PrincipalCacheTTL: 5 * time.Minute, PrincipalCacheRedisConnectTimeout: time.Second},
		{PrincipalCacheBackend: "redis", PrincipalCacheTTL: 5 * time.Minute, PrincipalCacheRedisURL: "redis://localhost:6379"},
		{PrincipalCacheBackend: "unknown", PrincipalCacheTTL: 5 * time.Minute},
	} {
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "PRINCIPAL_CACHE") {
			t.Fatalf("config=%#v error=%v", cfg, err)
		}
	}
}

func TestIdentityPrincipalCacheRedisURLIsSecretConfiguration(t *testing.T) {
	definition, ok := definitionByName(Definitions(), "PRINCIPAL_CACHE_REDIS_URL")
	if !ok || !definition.Secret {
		t.Fatalf("definition=%#v found=%v", definition, ok)
	}
}
