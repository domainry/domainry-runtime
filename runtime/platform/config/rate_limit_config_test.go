package config

import (
	"strings"
	"testing"
	"time"
)

func TestRateLimitConfigFromEnvironment(t *testing.T) {
	t.Setenv("RATE_LIMIT_BACKEND", "redis")
	t.Setenv("RATE_LIMIT_REDIS_URL", "redis://localhost:6379/2")
	t.Setenv("RATE_LIMIT_REDIS_PREFIX", "tenant:rate:")
	t.Setenv("RATE_LIMIT_REDIS_CONNECT_TIMEOUT", "3s")
	cfg := FromEnv()
	if cfg.RateLimitBackend != "redis" || cfg.RateLimitRedisURL != "redis://localhost:6379/2" || cfg.RateLimitRedisPrefix != "tenant:rate:" || cfg.RateLimitRedisConnectTimeout != 3*time.Second {
		t.Fatalf("config=%#v", cfg)
	}
}

func TestRateLimitConfigValidation(t *testing.T) {
	valid := Config{RateLimitBackend: "redis", RateLimitRedisURL: "redis://localhost:6379", RateLimitRedisConnectTimeout: time.Second}
	// Validate has unrelated required settings, so only assert that rate-limit
	// validation is not the first failure for a valid Redis configuration.
	if err := valid.Validate(); err != nil && strings.Contains(err.Error(), "RATE_LIMIT_") {
		t.Fatalf("valid Redis config rejected: %v", err)
	}
	for _, cfg := range []Config{
		{RateLimitBackend: "redis", RateLimitRedisConnectTimeout: time.Second},
		{RateLimitBackend: "redis", RateLimitRedisURL: "redis://localhost:6379"},
		{RateLimitBackend: "unknown"},
	} {
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "RATE_LIMIT_") {
			t.Fatalf("config=%#v error=%v", cfg, err)
		}
	}
}

func TestRateLimitRedisURLIsSecretConfiguration(t *testing.T) {
	definition, ok := definitionByName(Definitions(), "RATE_LIMIT_REDIS_URL")
	if !ok || !definition.Secret {
		t.Fatalf("definition=%#v found=%v", definition, ok)
	}
}
