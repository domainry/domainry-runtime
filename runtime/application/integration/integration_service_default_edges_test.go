package integration

import (
	"context"
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestIntegrationApplicationServiceDefaultDependencyEdges(t *testing.T) {
	service := NewIntegrationApplicationService(ApplicationDependencies{})
	if service.resolveUnmappedReadOnly(t.Context(), "provider", "subject").Known {
		t.Fatal("default unmapped resolver returned a known principal")
	}
	if refs, err := service.connectionReferences(t.Context(), "connection", principalmodel.Principal{}); err != nil || refs != nil {
		t.Fatalf("default references=%#v err=%v", refs, err)
	}
	if err := service.validateConnectionDraft(t.Context(), "connection", integrationmodel.IntegrationConnectionUpsertRequest{}, principalmodel.Principal{}); err != nil {
		t.Fatalf("default draft validation=%v", err)
	}
	if _, err := service.prepareConnectionConfig(integrationmodel.ConnectorSchema{Key: "connector"}, "", "configured", map[string]any{"provider": "forbidden"}); err == nil {
		t.Fatal("default connection config accepted invalid status")
	}
	if result := service.verifyWebhookSignature(integrationmodel.IntegrationWebhookSignatureCheck{}); result.Failure != "invalid_algorithm" {
		t.Fatalf("default webhook verification=%#v", result)
	}
	if service.normalizeWebhookAlgorithm("sha256") != "" {
		t.Fatal("default webhook normalization changed algorithm")
	}
	if err := service.executeAutomationOutbox(t.Context(), integrationmodel.IntegrationOutboxMessage{}); err == nil {
		t.Fatal("default automation outbox executor succeeded")
	}
	if _, err := service.sendAdapterOutbox(t.Context(), integrationmodel.IntegrationOutboxMessage{}, principalmodel.Principal{}); err == nil {
		t.Fatal("default adapter outbox sender succeeded")
	}
	if err := service.validateOperationInput("connector", integrationmodel.ConnectorOperationSchema{}, nil); err != nil {
		t.Fatalf("default input validation=%v", err)
	}
	if err := service.validateOperationOutput("connector", integrationmodel.ConnectorOperationSchema{}, nil); err != nil {
		t.Fatalf("default output validation=%v", err)
	}
	if _, handled, err := service.executeEventMapping(t.Context(), integrationmodel.IntegrationEvent{}, principalmodel.Principal{}); err != nil || handled {
		t.Fatalf("default mapping handled=%t err=%v", handled, err)
	}
	overridden := NewIntegrationApplicationService(ApplicationDependencies{
		EventMappingExecutor: func(context.Context, integrationmodel.IntegrationEvent, principalmodel.Principal) (EventProcessDecision, bool, error) {
			return EventProcessDecision{Status: "processed"}, true, nil
		},
		EventWorkflowExecutor: func(context.Context, string, integrationmodel.IntegrationEntrypointWorkflowRequest, principalmodel.Principal) (IntegrationWorkflowRunResult, error) {
			return IntegrationWorkflowRunResult{}, nil
		},
		EventActionExecutor: func(context.Context, string, string, string, integrationmodel.IntegrationEntrypointActionRequest, principalmodel.Principal) (IntegrationActionExecutionResult, error) {
			return IntegrationActionExecutionResult{}, nil
		},
		EventIdentityResolver: func(context.Context, integrationmodel.IntegrationExternalIdentityResolveRequest, principalmodel.Principal) (integrationmodel.IntegrationExternalIdentityResolveResult, principalmodel.Principal, error) {
			return integrationmodel.IntegrationExternalIdentityResolveResult{}, principalmodel.Principal{}, nil
		},
	})
	if decision, handled, err := overridden.executeEventMapping(t.Context(), integrationmodel.IntegrationEvent{}, principalmodel.Principal{}); err != nil || !handled || decision.Status != "processed" {
		t.Fatalf("overridden mapping decision=%#v handled=%t err=%v", decision, handled, err)
	}
}
