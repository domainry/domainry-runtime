package integration

import (
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"testing"
	"time"
)

func TestAPIKeyRateLimitConfigPrefersValidScope(t *testing.T) {
	t.Setenv("INTEGRATION_API_KEY_RATE_LIMIT", "2/hour")
	config := APIKeyRateLimitConfig(integrationmodel.IntegrationAPIKey{Scopes: []string{"records.read", "rate_limit:15/30s"}})
	if !config.Enabled || config.Limit != 15 || config.Window != 30*time.Second || config.Source != "scope:15/30s" {
		t.Fatalf("scope rate limit=%#v", config)
	}
}

func TestParseAPIKeyRateLimitRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "off", "0", "x/minute", "5/invalid"} {
		if config := ParseAPIKeyRateLimit(value, "test"); config.Enabled {
			t.Fatalf("invalid value %q enabled: %#v", value, config)
		}
	}
}
