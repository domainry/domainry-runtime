package integrationtest

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"path/filepath"
	"testing"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	"time"

	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestWorkflowPollingClaimsCommittedIntentWithoutWakeup(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "workflow-intent.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	workflow := definitionmodel.WorkflowSchema{
		Key: "transactional_intent", Name: "Transactional Intent", Enabled: true,
		Action: map[string]any{"type": "workflow_graph"},
		Graph:  &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger", Name: "Start"}}},
	}
	records := newWorkflowProcessTestService(t, store, workflow, nil)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	intent := workflowmodel.WorkflowExecution{
		ID: "intent_1", WorkflowKey: workflow.Key, Name: workflow.Name, Trigger: "action_executed:pipeline_item.advance",
		Status: "pending", ActionType: "workflow_graph", Action: workflow.Action,
		Payload: map[string]any{"object_key": "pipeline_item", "record_id": "item_1"}, Result: map[string]any{"transactional_intent": true},
		ObjectKey: "pipeline_item", RecordID: "item_1", ActorID: "admin", MaxAttempts: 3,
		Message: "workflow.message.queued", CreatedAt: now, UpdatedAt: now,
	}
	if err := workflowWorkerStore(store).InsertExecution(t.Context(), "default", intent); err != nil {
		t.Fatalf("insert intent: %v", err)
	}

	result, err := records.Applications().Workflows.ProcessDueWorkflowExecutions(t.Context(), 10, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "system", WorkspaceID: "default"}})
	if err != nil {
		t.Fatalf("process intent: %v", err)
	}
	if result.Processed != 1 || len(result.Executions) != 1 {
		t.Fatalf("expected one continued execution, got %#v", result)
	}
	persisted, ok, err := workflowWorkerStore(store).GetExecution(t.Context(), "default", intent.ID)
	if err != nil || !ok {
		t.Fatalf("get claimed intent: ok=%v err=%v", ok, err)
	}
	if persisted.Status != "skipped" || persisted.Result["continued_execution_id"] == "" {
		t.Fatalf("expected claimed intent to link continued execution, got %#v", persisted)
	}
}
