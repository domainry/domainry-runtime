// Integration application service lifecycle tests.
package integration

import (
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
	"github.com/domainry/domainry-runtime/runtime/platform/resilience"
)

func TestApplicationServiceOwnsConstructorPolicyDependencies(t *testing.T) {
	limiter := ratelimit.NewMemoryLimiter(8)
	policy := resilience.NewMemoryStore(8)
	service := NewIntegrationApplicationService(ApplicationDependencies{APILimiter: limiter, PolicyStore: policy})
	if service.apiLimiter != limiter {
		t.Fatal("API limiter constructor dependency was not retained")
	}
	if service.policyStore != policy {
		t.Fatal("policy store constructor dependency was not retained")
	}
}
