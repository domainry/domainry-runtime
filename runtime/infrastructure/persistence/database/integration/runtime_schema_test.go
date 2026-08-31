package integration

import (
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	automationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/automation"
)

func TestEnsureRuntimeSchemaCreatesIntegrationOutbox(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatalf("ensure runtime schema: %v", err)
	}
	delivery := NewIntegrationDeliveryStore(store)
	message, err := delivery.InsertOutbox(t.Context(), "workspace-primary", integrationmodel.IntegrationOutboxMessage{ID: "automation:rule:customer:1", WorkspaceID: "workspace-primary", ConnectorKey: "__automation__", Operation: "customer.after_create", Status: "queued", Payload: map[string]any{"rule_key": "customer.after_create"}, CreatedBy: "tester"})
	if err != nil {
		t.Fatalf("enqueue outbox on fresh runtime schema: %v", err)
	}
	messages, err := delivery.ListOutbox(t.Context(), "workspace-primary", "__automation__", "queued", 10)
	if err != nil || len(messages) != 1 || messages[0].ID != message.ID {
		t.Fatalf("list outbox on fresh runtime schema: messages=%#v err=%v", messages, err)
	}
	automation := automationpersistence.NewAutomationExecutionStore(store)
	execution, err := automation.InsertExecution(t.Context(), "workspace-primary", automationmodel.AutomationRuleExecution{ID: "automation_execution_1", WorkspaceID: "workspace-primary", RuleKey: "customer.after_create", ObjectKey: "customer", RecordID: "customer_1", Phase: "after", Operation: "create", Status: "succeeded", ActorID: "tester", RequestID: "request_1", CorrelationID: "request_1", EventID: message.EventID, DurationMS: 12, Candidate: map[string]any{"name": "Customer"}, Trace: map[string]any{"actions": []any{map[string]any{"key": "notify", "connector_key": "mock"}}}})
	if err != nil {
		t.Fatalf("insert automation execution on fresh runtime schema: %v", err)
	}
	executions, err := automation.ListExecutions(t.Context(), "workspace-primary", automationmodel.AutomationExecutionFilter{RuleKey: execution.RuleKey, RecordID: execution.RecordID, ConnectorKey: "mock", Status: "succeeded", Limit: 10})
	if err != nil || len(executions) != 1 || executions[0].CorrelationID != "request_1" {
		t.Fatalf("filter linked automation execution evidence: executions=%#v err=%v", executions, err)
	}
	identity, err := NewIntegrationConfigStore(store).UpsertExternalIdentity(t.Context(), "workspace-primary", integrationmodel.IntegrationExternalIdentity{Key: "agent_test", WorkspaceID: "workspace-primary", Provider: "test", ExternalSubject: "agent-1", ExternalSubjectType: "user", ActorID: "admin", RoleKey: "admin", Status: "active", CreatedBy: "admin"})
	if err != nil || identity.Key != "agent_test" {
		t.Fatalf("upsert external identity on fresh runtime schema: identity=%#v err=%v", identity, err)
	}
}
