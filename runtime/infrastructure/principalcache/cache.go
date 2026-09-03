// Package principalcache selects the Runtime-owned principal cache backend.
package principalcache

import (
	"context"
	"fmt"
	"strings"

	identityprincipal "github.com/domainry/domainry-identity-sdk/authorization/principal"
	principalredis "github.com/domainry/domainry-runtime/runtime/infrastructure/principalredis"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func Open(ctx context.Context, cfg config.Config) (identityprincipal.Cache, error) {
	if cfg.PrincipalCacheTTL < 0 {
		return nil, fmt.Errorf("PRINCIPAL_CACHE_TTL cannot be negative")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.PrincipalCacheBackend)) {
	case "", "local":
		return identityprincipal.NewMemoryCache(), nil
	case "redis":
		if strings.TrimSpace(cfg.PrincipalCacheRedisURL) == "" {
			return nil, fmt.Errorf("PRINCIPAL_CACHE_REDIS_URL is required when PRINCIPAL_CACHE_BACKEND=redis")
		}
		return principalredis.Open(ctx, cfg.PrincipalCacheRedisURL, cfg.PrincipalCacheRedisPrefix, cfg.PrincipalCacheRedisConnectTimeout)
	default:
		return nil, fmt.Errorf("unsupported PRINCIPAL_CACHE_BACKEND %q", cfg.PrincipalCacheBackend)
	}
}
