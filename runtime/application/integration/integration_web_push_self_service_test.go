package integration

import (
	"encoding/json"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestWebPushReadinessReturnsOnlyPublicVAPIDConfiguration(t *testing.T) {
	config := &integrationManagementConfigRepo{
		connections: []integrationmodel.IntegrationConnection{{
			Key: "push", ConnectorKey: "notification", ProviderKey: "web_push", Status: "verified",
			Config:     map[string]any{"vapid_public_key": "public-vapid-key", "unrelated": "visible-only-to-runtime"},
			SecretRefs: map[string]string{"vapid_private_key": "secret:vapid-private"},
		}},
		secrets: []integrationmodel.IntegrationSecret{{Key: "vapid-private", Status: "active", ValueRef: "runtime-secret-material-must-not-leak"}},
	}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: config})
	result, err := service.WebPushReadiness(t.Context(), integrationManagementPrincipal())
	if err != nil || !result.Ready || result.PublicKey != "public-vapid-key" || result.ConnectionKey != "push" || result.Status != "verified" || result.Reason != "" {
		t.Fatalf("readiness=%#v err=%v", result, err)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "private") || strings.Contains(string(payload), "runtime-secret-material") || strings.Contains(string(payload), "unrelated") {
		t.Fatalf("readiness leaked private Runtime configuration: %s", payload)
	}
}

func TestWebPushReadinessFailsClosedAndRequiresAuthenticatedUser(t *testing.T) {
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: &integrationManagementConfigRepo{connections: []integrationmodel.IntegrationConnection{{
		Key: "push", ConnectorKey: "notification", ProviderKey: "web_push", Status: "active",
		Config: map[string]any{"vapid_public_key": "public-vapid-key"},
	}}}})
	result, err := service.WebPushReadiness(t.Context(), integrationManagementPrincipal())
	if err != nil || result.Ready || result.Reason != "private_key_unbound" || result.PublicKey != "public-vapid-key" {
		t.Fatalf("readiness=%#v err=%v", result, err)
	}

	for _, test := range []struct {
		principal principalmodel.Principal
		code      string
	}{{principal: principalmodel.Principal{}, code: "backend.workspace_scope_required"}, {principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, code: "auth.permission_denied"}} {
		principal := test.principal
		if _, err := service.WebPushReadiness(t.Context(), principal); apperror.CodeOf(err) != test.code {
			t.Fatalf("readiness principal=%#v err=%v", principal, err)
		}
		if _, err := service.ListWebPushSubscriptions(t.Context(), principal); apperror.CodeOf(err) != test.code {
			t.Fatalf("list principal=%#v err=%v", principal, err)
		}
		if _, err := service.UpsertWebPushSubscription(t.Context(), "sub", integrationmodel.WebPushSubscriptionUpsertRequest{}, principal); apperror.CodeOf(err) != test.code {
			t.Fatalf("upsert principal=%#v err=%v", principal, err)
		}
		if _, err := service.RevokeWebPushSubscription(t.Context(), "sub", principal); apperror.CodeOf(err) != test.code {
			t.Fatalf("revoke principal=%#v err=%v", principal, err)
		}
	}
}
