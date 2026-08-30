package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type webPushSubscriptionsProbe struct {
	readiness integrationsdk.WebPushReadiness
}

func (p webPushSubscriptionsProbe) Readiness(context.Context, string) (integrationsdk.WebPushReadiness, error) {
	return p.readiness, nil
}
func (webPushSubscriptionsProbe) List(context.Context, string, string) ([]integrationsdk.WebPushSubscription, error) {
	return nil, nil
}
func (webPushSubscriptionsProbe) Upsert(context.Context, string, string, string, integrationsdk.WebPushSubscriptionInput) (integrationsdk.WebPushSubscription, error) {
	return integrationsdk.WebPushSubscription{}, nil
}
func (webPushSubscriptionsProbe) Revoke(context.Context, string, string, string) (integrationsdk.WebPushSubscription, error) {
	return integrationsdk.WebPushSubscription{}, nil
}
func (webPushSubscriptionsProbe) CleanupExpired(context.Context, string) (int, error) { return 0, nil }

func TestWebPushReadinessReturnsOnlyPublicVAPIDConfiguration(t *testing.T) {
	service := NewIntegrationApplicationService(ApplicationDependencies{OwnerWebPushSubscriptions: webPushSubscriptionsProbe{readiness: integrationsdk.WebPushReadiness{Ready: true, PublicKey: "public-vapid-key", ConnectionKey: "push", Status: "verified"}}})
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
	service := NewIntegrationApplicationService(ApplicationDependencies{OwnerWebPushSubscriptions: webPushSubscriptionsProbe{readiness: integrationsdk.WebPushReadiness{PublicKey: "public-vapid-key", ConnectionKey: "push", Status: "active", Reason: "private_key_unbound"}}})
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
