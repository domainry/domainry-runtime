package runtime

import (
	"context"
	"fmt"
	"strings"

	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	ratelimitpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/ratelimit"
	ratelimitredis "github.com/domainry/domainry-runtime/runtime/infrastructure/ratelimitredis"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-foundation/ratelimit"
)

func openSharedRateLimiter(ctx context.Context, cfg config.Config, store *persistence.RuntimeStore) (ratelimit.Limiter, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.RateLimitBackend)) {
	case "", "database":
		limiter := ratelimitpersistence.NewRateLimiter(store)
		if err := limiter.EnsureSchema(ctx); err != nil {
			return nil, err
		}
		return limiter, nil
	case "redis":
		if strings.TrimSpace(cfg.RateLimitRedisURL) == "" {
			return nil, fmt.Errorf("RATE_LIMIT_REDIS_URL is required when RATE_LIMIT_BACKEND=redis")
		}
		return ratelimitredis.Open(ctx, cfg.RateLimitRedisURL, cfg.RateLimitRedisPrefix, cfg.RateLimitRedisConnectTimeout)
	default:
		return nil, fmt.Errorf("unsupported RATE_LIMIT_BACKEND %q", cfg.RateLimitBackend)
	}
}
