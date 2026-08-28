package integration

import (
	"context"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
)

func TestIntegrationMemoryKeysArePartitionedByWorkspace(t *testing.T) {
	request := SyncCallRequest{ConnectorKey: "crm", ConnectionKey: "primary", ActionKey: "sync", InvocationKey: "send", Operation: "upsert"}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}
	keyA := SyncPolicyKey(request, integrationmodel.IntegrationConnection{WorkspaceID: "workspace-a", Key: "primary"}, principal)
	principal.WorkspaceID = "workspace-b"
	keyB := SyncPolicyKey(request, integrationmodel.IntegrationConnection{WorkspaceID: "workspace-b", Key: "primary"}, principal)
	if keyA == keyB || keyA == "" || keyB == "" {
		t.Fatalf("sync resilience keys must be workspace-partitioned: a=%q b=%q", keyA, keyB)
	}
	if connectionCircuitKey(integrationmodel.IntegrationConnection{WorkspaceID: "workspace-a", Key: "primary"}) == connectionCircuitKey(integrationmodel.IntegrationConnection{WorkspaceID: "workspace-b", Key: "primary"}) {
		t.Fatal("outbox circuit keys must be workspace-partitioned")
	}

	limiter := &capturingLimiter{}
	service := NewIntegrationApplicationService(ApplicationDependencies{APILimiter: limiter})
	for _, workspaceID := range []string{"workspace-a", "workspace-b"} {
		apiKey := integrationmodel.IntegrationAPIKey{WorkspaceID: workspaceID, Key: "same-key", Scopes: []string{"rate_limit:1/minute"}}
		if err := service.checkAPIKeyRateLimit(t.Context(), apiKey); err != nil {
			t.Fatal(err)
		}
	}
	if len(limiter.keys) != 2 || limiter.keys[0] == limiter.keys[1] {
		t.Fatalf("API rate-limit keys must be workspace-partitioned: %#v", limiter.keys)
	}
}

type capturingLimiter struct{ keys []string }

func (l *capturingLimiter) Allow(_ context.Context, key string, limit int, window time.Duration) (ratelimit.Decision, error) {
	l.keys = append(l.keys, key)
	return ratelimit.Decision{Allowed: true, Limit: limit, RetryAfter: window}, nil
}
