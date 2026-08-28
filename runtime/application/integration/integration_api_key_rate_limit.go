package integration

import (
	"context"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"os"
	"strconv"
	"strings"
	"time"
)

type APIKeyRateLimit struct {
	Enabled bool
	Limit   int
	Window  time.Duration
	Source  string
}

func (s *IntegrationApplicationService) checkAPIKeyRateLimit(ctx context.Context, apiKey integrationmodel.IntegrationAPIKey) error {
	config := APIKeyRateLimitConfig(apiKey)
	if !config.Enabled || s.apiLimiter == nil {
		return nil
	}
	bucketKey := strings.TrimSpace(apiKey.WorkspaceID) + ":" + strings.TrimSpace(apiKey.Key) + ":" + config.Source
	decision, err := s.apiLimiter.Allow(ctx, "integration_api_key:"+bucketKey, config.Limit, config.Window)
	if err != nil {
		return err
	}
	if !decision.Allowed {
		return forbidden("backend.integration.api_key.rate_limited")
	}
	return nil
}

func APIKeyRateLimitConfig(apiKey integrationmodel.IntegrationAPIKey) APIKeyRateLimit {
	for _, scope := range apiKey.Scopes {
		if limit := APIKeyRateLimitFromScope(scope); limit.Enabled {
			return limit
		}
	}
	return ParseAPIKeyRateLimit(strings.TrimSpace(os.Getenv("INTEGRATION_API_KEY_RATE_LIMIT")), "env")
}

func APIKeyRateLimitFromScope(scope string) APIKeyRateLimit {
	scope = strings.TrimSpace(scope)
	if !strings.HasPrefix(scope, "rate_limit:") {
		return APIKeyRateLimit{}
	}
	return ParseAPIKeyRateLimit(strings.TrimPrefix(scope, "rate_limit:"), "scope")
}

func ParseAPIKeyRateLimit(raw, source string) APIKeyRateLimit {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" || raw == "off" || raw == "disabled" || raw == "0" {
		return APIKeyRateLimit{}
	}
	parts := strings.Split(raw, "/")
	if len(parts) != 2 {
		return APIKeyRateLimit{}
	}
	limit, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || limit <= 0 {
		return APIKeyRateLimit{}
	}
	windowText := strings.TrimSpace(parts[1])
	window := time.Minute
	switch windowText {
	case "s", "sec", "second", "seconds":
		window = time.Second
	case "m", "min", "minute", "minutes":
		window = time.Minute
	case "h", "hr", "hour", "hours":
		window = time.Hour
	default:
		parsed, err := time.ParseDuration(windowText)
		if err != nil || parsed <= 0 {
			return APIKeyRateLimit{}
		}
		window = parsed
	}
	return APIKeyRateLimit{Enabled: true, Limit: limit, Window: window, Source: source + ":" + raw}
}
