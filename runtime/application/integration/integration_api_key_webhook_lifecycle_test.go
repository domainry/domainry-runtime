package integration

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestIntegrationAPIKeyLifecycleScopesIdentityAndInvalidatesTokens(t *testing.T) {
	repository := &independentConfigRepository{apiKeys: map[string]integrationmodel.IntegrationAPIKey{}}
	auditEvents := []string{}
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository,
		PrincipalResolver: func(_ context.Context, actorID, roleKey, _ string) principalmodel.Principal {
			if actorID != "service-user" || roleKey != "integration-client" {
				return principalmodel.Principal{}
			}
			return integrationSDKPrincipal(actorID, roleKey, "customer.read", "customer.write")
		},
		Audit: func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, _, _ map[string]any, _ map[string]any) {
			auditEvents = append(auditEvents, event)
		},
	})
	admin := integrationSDKPrincipal("admin", "admin", "workspace.admin")
	admin.WorkspaceID = "workspace-a"
	created, err := service.CreateIntegrationAPIKey(t.Context(), integrationmodel.IntegrationAPIKeyCreateRequest{
		Key: "erp-reader", Name: "ERP reader", ActorID: "service-user", RoleKey: "integration-client",
		Scopes: []string{"customer.read", "rate_limit:20/minute", "customer.read"}, ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}, admin)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Token, APIKeyTokenPrefix) || created.APIKey.TokenHash != apiKeyTokenHash(created.Token) || created.APIKey.TokenPrefix == created.Token {
		t.Fatalf("created key did not retain a one-way token contract: %#v", created)
	}
	if want := []string{"customer.read", "rate_limit:20/minute"}; !reflect.DeepEqual(created.APIKey.Scopes, want) {
		t.Fatalf("scopes=%v want=%v", created.APIKey.Scopes, want)
	}

	principal, used, err := service.PrincipalFromIntegrationAPIKey(t.Context(), created.Token, "workspace-a", "request-1")
	if err != nil {
		t.Fatal(err)
	}
	if principal.WorkspaceID != "workspace-a" || principal.RequestID != "request-1" || !principal.HasPermission("customer.read") || principal.HasPermission("customer.write") || used.LastUsedAt == "" {
		t.Fatalf("scoped principal=%#v key=%#v", principal, used)
	}
	if principal.AccessBundle == nil {
		t.Fatal("scoped principal lost the SDK access bundle")
	}

	disabled, err := service.DisableIntegrationAPIKey(t.Context(), "erp-reader", admin)
	if err != nil || disabled.Status != "disabled" || disabled.DisabledAt == "" {
		t.Fatalf("disabled=%#v err=%v", disabled, err)
	}
	if _, _, err := service.PrincipalFromIntegrationAPIKey(t.Context(), created.Token, "workspace-a", "request-2"); testErrorCode(err) != "auth.invalid_token" {
		t.Fatalf("disabled token error=%v", err)
	}

	rotated, err := service.RotateIntegrationAPIKey(t.Context(), "erp-reader", admin)
	if err != nil || rotated.APIKey.Status != "active" || rotated.APIKey.DisabledAt != "" || rotated.Token == created.Token {
		t.Fatalf("rotated=%#v err=%v", rotated, err)
	}
	if _, _, err := service.PrincipalFromIntegrationAPIKey(t.Context(), created.Token, "workspace-a", "request-old"); testErrorCode(err) != "auth.invalid_token" {
		t.Fatalf("old token remained valid: %v", err)
	}
	if _, _, err := service.PrincipalFromIntegrationAPIKey(t.Context(), rotated.Token, "workspace-a", "request-new"); err != nil {
		t.Fatalf("rotated token rejected: %v", err)
	}
	if want := []string{"integration_api_key_created", "integration_api_key_used", "integration_api_key_disabled", "integration_api_key_rotated", "integration_api_key_used"}; !reflect.DeepEqual(auditEvents, want) {
		t.Fatalf("audit events=%v want=%v", auditEvents, want)
	}
}

func TestIntegrationAPIKeyValidationRejectsInvalidAuthorityAndInput(t *testing.T) {
	repository := &independentConfigRepository{apiKeys: map[string]integrationmodel.IntegrationAPIKey{}}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, PrincipalResolver: func(_ context.Context, actorID, roleKey, _ string) principalmodel.Principal {
		if actorID == "known" && roleKey == "client" {
			return integrationSDKPrincipal(actorID, roleKey, "customer.read")
		}
		return principalmodel.Principal{}
	}})
	admin := integrationSDKPrincipal("admin", "admin", "workspace.admin")
	admin.WorkspaceID = "workspace-a"
	tests := []struct {
		name      string
		principal principalmodel.Principal
		request   integrationmodel.IntegrationAPIKeyCreateRequest
		code      string
	}{
		{name: "permission", principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, request: integrationmodel.IntegrationAPIKeyCreateRequest{}, code: "auth.permission_denied"},
		{name: "actor", principal: admin, request: integrationmodel.IntegrationAPIKeyCreateRequest{RoleKey: "client"}, code: "backend.integration.api_key.missing_actor"},
		{name: "role", principal: admin, request: integrationmodel.IntegrationAPIKeyCreateRequest{ActorID: "known"}, code: "backend.integration.api_key.missing_role"},
		{name: "unknown role", principal: admin, request: integrationmodel.IntegrationAPIKeyCreateRequest{ActorID: "unknown", RoleKey: "client"}, code: "backend.integration.api_key.unknown_role"},
		{name: "blank scope", principal: admin, request: integrationmodel.IntegrationAPIKeyCreateRequest{ActorID: "known", RoleKey: "client", Scopes: []string{" "}}, code: "backend.integration.api_key.invalid_scope"},
		{name: "excess scope", principal: admin, request: integrationmodel.IntegrationAPIKeyCreateRequest{ActorID: "known", RoleKey: "client", Scopes: []string{"customer.write"}}, code: "backend.integration.api_key.invalid_scope"},
		{name: "expiry", principal: admin, request: integrationmodel.IntegrationAPIKeyCreateRequest{ActorID: "known", RoleKey: "client", ExpiresAt: "tomorrow"}, code: "backend.integration.api_key.invalid_expires_at"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.CreateIntegrationAPIKey(t.Context(), test.request, test.principal)
			if got := testErrorCode(err); got != test.code {
				t.Fatalf("error code=%q want=%q err=%v", got, test.code, err)
			}
		})
	}
}

func TestIntegrationWebhookSubscriptionLifecyclePublishesAuditableOutbox(t *testing.T) {
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{
		"erp": {Key: "erp", WorkspaceID: "workspace-a", ConnectorKey: "webhook", Status: "verified"},
	}}
	delivery := &independentDeliveryRepository{}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, DeliveryRepository: delivery, ConnectorExists: func(key string) bool { return key == "webhook" }})
	manager := integrationSDKPrincipal("admin", "admin", PermissionConnectionManage, PermissionInvoke)
	manager.WorkspaceID, manager.RequestID = "workspace-a", "request-1"
	subscription, err := service.UpsertIntegrationWebhookSubscription(t.Context(), "orders", integrationmodel.IntegrationWebhookSubscriptionUpsertRequest{
		Name: "Orders", ConnectorKey: "webhook", ConnectionKey: "erp", EventTypes: []string{"order.paid", "order.created", "order.paid"},
	}, manager)
	if err != nil {
		t.Fatal(err)
	}
	if subscription.Status != "active" || !reflect.DeepEqual(subscription.EventTypes, []string{"order.created", "order.paid"}) {
		t.Fatalf("subscription=%#v", subscription)
	}
	result, err := service.PublishIntegrationWebhookEvent(t.Context(), integrationmodel.IntegrationWebhookPublishRequest{
		EventType: "order.paid", ObjectKey: "sales_order", RecordID: "order-1", WorkflowExecutionID: "workflow-1", Payload: map[string]any{"amount": 42},
	}, manager)
	if err != nil || result.Enqueued != 1 || len(delivery.outboxes) != 1 {
		t.Fatalf("result=%#v outboxes=%#v err=%v", result, delivery.outboxes, err)
	}
	payload, ok := delivery.outboxes[0].Payload["payload"].(map[string]any)
	if !ok {
		t.Fatalf("operation payload=%#v", delivery.outboxes[0].Payload)
	}
	for key, want := range map[string]any{"event_type": "order.paid", "subscription_key": "orders", "object_key": "sales_order", "record_id": "order-1", "workflow_execution_id": "workflow-1", "request_id": "request-1"} {
		if payload[key] != want {
			t.Fatalf("payload[%s]=%#v want=%#v payload=%#v", key, payload[key], want, payload)
		}
	}
	manager.RequestID = ""
	delivery.outboxes = nil
	if result, err := service.PublishIntegrationWebhookEvent(t.Context(), integrationmodel.IntegrationWebhookPublishRequest{EventType: "order.paid"}, manager); err != nil || result.Enqueued != 1 || len(delivery.outboxes) != 1 {
		t.Fatalf("publish without request ID result=%#v err=%v", result, err)
	}
	disabled, err := service.DisableIntegrationWebhookSubscription(t.Context(), "orders", manager)
	if err != nil || disabled.Status != "disabled" || disabled.DisabledAt == "" {
		t.Fatalf("disabled=%#v err=%v", disabled, err)
	}
}

func TestWebhookAndAPIKeyNormalizationBoundaries(t *testing.T) {
	for _, scope := range []string{"rate_limit:1/s", "rate_limit:2/minutes", "rate_limit:3/hour", "rate_limit:4/250ms"} {
		if !validRateLimitScope(scope) {
			t.Fatalf("validRateLimitScope(%q)=false", scope)
		}
	}
	for _, scope := range []string{"records.read", "rate_limit:0/s", "rate_limit:x/s", "rate_limit:1/day", "rate_limit:1/0s", "rate_limit:1/s/extra"} {
		if validRateLimitScope(scope) {
			t.Fatalf("validRateLimitScope(%q)=true", scope)
		}
	}
	if got, err := normalizeWebhookEventTypes(nil); err != nil || !reflect.DeepEqual(got, []string{"*"}) {
		t.Fatalf("default event types=%v err=%v", got, err)
	}
	if _, err := normalizeWebhookEventTypes([]string{"created", " "}); testErrorCode(err) != "backend.integration.webhook_subscription.invalid_event_type" {
		t.Fatalf("blank event type error=%v", err)
	}
	for input, want := range map[string]string{"": "active", " active ": "active", "disabled": "disabled"} {
		if got, err := normalizeWebhookSubscriptionStatus(input); err != nil || got != want {
			t.Fatalf("normalize status %q=%q err=%v", input, got, err)
		}
	}
	if _, err := normalizeWebhookSubscriptionStatus("paused"); testErrorCode(err) != "backend.integration.webhook_subscription.invalid_status" {
		t.Fatalf("invalid status error=%v", err)
	}
}
