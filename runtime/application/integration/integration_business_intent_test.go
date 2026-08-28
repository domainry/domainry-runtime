package integration

import (
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestBusinessIntegrationIntentIsOwnerScopedAndReturnsOnlyRedactedOutcome(t *testing.T) {
	delivery := &integrationManagementDeliveryRepo{
		found: true,
		outboxes: []integrationmodel.IntegrationOutboxMessage{{
			ID: "intent-1", WorkspaceID: "workspace", ConnectorKey: "payment",
			ConnectionKey: "wechat", Operation: "create_native_payment_order",
			Status: "sent", Payload: map[string]any{"merchant_private_key": "must-not-leak"},
			CreatedBy: "user", AttemptCount: 1, CreatedAt: "created", UpdatedAt: "updated",
		}},
		invocations: []integrationmodel.IntegrationInvocation{{
			ID: "invocation-1", ProviderKey: "wechat_pay", Status: "succeeded",
			ResponseRef: "wechat_pay:transaction-1", UpdatedAt: "completed",
			Metadata: map[string]any{
				"source": "outbox", "source_id": "intent-1",
				"request":  map[string]any{"merchant_private_key": "[REDACTED]"},
				"response": map[string]any{"code_url": "weixin://wxpay/example"},
			},
		}},
	}
	service := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: delivery})
	result, err := service.GetBusinessIntegrationIntent(t.Context(), " intent-1 ", integrationManagementPrincipal())
	if err != nil || result.ID != "intent-1" || result.ProviderKey != "wechat_pay" || result.Status != "succeeded" ||
		result.Response["code_url"] != "weixin://wxpay/example" {
		t.Fatalf("intent=%#v err=%v", result, err)
	}
	if _, found := result.Response["merchant_private_key"]; found {
		t.Fatalf("business intent leaked request credentials: %#v", result.Response)
	}
	other := integrationManagementPrincipal()
	other.UserID = "other-user"
	if _, err := service.GetBusinessIntegrationIntent(t.Context(), "intent-1", other); apperror.CodeOf(err) != "backend.integration.intent.not_found" {
		t.Fatalf("ownership mismatch error=%v", err)
	}
}

func TestBusinessIntegrationIntentRemainingReadBoundaries(t *testing.T) {
	if _, err := NewIntegrationApplicationService(ApplicationDependencies{}).GetBusinessIntegrationIntent(t.Context(), "intent", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization err=%v", err)
	}
	if _, err := NewIntegrationApplicationService(ApplicationDependencies{}).GetBusinessIntegrationIntent(t.Context(), "intent", integrationManagementPrincipal()); err == nil {
		t.Fatal("missing outbox reader accepted")
	}
	repository := &integrationManagementDeliveryRepo{err: errIntegrationManagementTest}
	service := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: repository})
	if _, err := service.GetBusinessIntegrationIntent(t.Context(), "intent", integrationManagementPrincipal()); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("lookup err=%v", err)
	}
	repository.err = nil
	if _, err := service.GetBusinessIntegrationIntent(t.Context(), "intent", integrationManagementPrincipal()); apperror.CodeOf(err) != "backend.integration.intent.not_found" {
		t.Fatalf("missing err=%v", err)
	}
	repository.found = true
	repository.outboxes = []integrationmodel.IntegrationOutboxMessage{{ID: "intent", ConnectorKey: "connector"}}
	if _, err := service.GetBusinessIntegrationIntent(t.Context(), "intent", integrationManagementPrincipal()); apperror.CodeOf(err) != "backend.integration.intent.not_found" {
		t.Fatalf("ownerless err=%v", err)
	}
	repository.outboxes[0].CreatedBy = "user"
	repository.invocationErr = errIntegrationManagementTest
	if _, err := service.GetBusinessIntegrationIntent(t.Context(), "intent", integrationManagementPrincipal()); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("invocation err=%v", err)
	}
	repository.invocationErr = nil
	repository.invocations = []integrationmodel.IntegrationInvocation{
		{Metadata: map[string]any{"source": "sync", "source_id": "intent"}},
		{Metadata: map[string]any{"source": "outbox", "source_id": "other"}},
		{Status: "succeeded", Metadata: map[string]any{"source": "outbox", "source_id": "intent", "response": "not-a-map"}},
	}
	result, err := service.GetBusinessIntegrationIntent(t.Context(), "intent", integrationManagementPrincipal())
	if err != nil || result.Status != "succeeded" || result.Response != nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
