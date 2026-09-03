package integrationtest

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"path/filepath"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"

	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestWorkflowGraphFailureAndDeadLetterLinkToProcessAndNode(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "workflow-links.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	workflow := definitionmodel.WorkflowSchema{
		Key: "failing_process", Name: "Failing Process", Enabled: true,
		Trigger: map[string]any{"type": "manual"}, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}, Action: map[string]any{"type": "workflow_graph"},
		Retry: &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 1, DelaySeconds: 1},
		Graph: &definitionmodel.WorkflowGraphSchema{Version: 2,
			Nodes: []definitionmodel.WorkflowGraphNode{
				{ID: "start", Type: "trigger", Name: "Start"},
				{ID: "broken_action", Type: "action", Name: "Broken Action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "missing.action", ObjectKey: "missing_object", OnError: "fail"}}},
			},
			Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start-action", Source: "start", Target: "broken_action"}},
		},
	}
	role := accessfixture.Bundle{Key: "admin", Permissions: []string{"workflow." + workflow.Key + ".run"}}
	records := runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{TemplateID: "workflow-links", TemplateVersion: "1", Name: "Workflow Links", Objects: nil, Actions: nil, Workflows: []definitionmodel.WorkflowSchema{workflow}, AutomationRules: nil, Dictionaries: nil, Integrations: connectormodel.IntegrationSchema{}, Reports: nil, Skills: nil, Agents: nil, Store: store, WorkflowProcesses: workflowpersistence.NewWorkflowProcessStore(store), WorkflowWorker: workflowpersistence.NewWorkflowWorkerStore(store)})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, role)

	run, err := records.Applications().Workflows.RunWorkflow(t.Context(), workflow.Key, map[string]any{"request_id": "failure-1"}, principal)
	if err != nil {
		t.Fatalf("run failing graph: %v", err)
	}
	execution := run.Execution
	if execution.Status != "dead_letter" || execution.ProcessID == "" || execution.NodeID != "broken_action" {
		t.Fatalf("failure is not linked to process/node: %#v", execution)
	}
	process, ok, err := workflowProcessStore(store).GetProcess(t.Context(), "workspace-primary", execution.ProcessID)
	if err != nil || !ok || process.Status != "configuration_error" {
		t.Fatalf("expected failed process evidence: process=%#v ok=%v err=%v", process, ok, err)
	}
	persisted, ok, err := workflowWorkerStore(store).GetExecution(t.Context(), "workspace-primary", execution.ID)
	if err != nil || !ok || persisted.ProcessID != process.ID || persisted.NodeID != "broken_action" {
		t.Fatalf("expected persisted failure links: execution=%#v ok=%v err=%v", persisted, ok, err)
	}
	events, err := workflowProcessStore(store).ListEvents(t.Context(), "workspace-primary", process.ID, 100)
	if err != nil {
		t.Fatalf("list process events: %v", err)
	}
	foundNodeFailure, foundDeadLetter := false, false
	for _, event := range events {
		foundNodeFailure = foundNodeFailure || (event.Event == "node_failed" && event.NodeID == "broken_action")
		foundDeadLetter = foundDeadLetter || event.Event == "workflow_execution_dead_letter"
	}
	if !foundNodeFailure || !foundDeadLetter {
		t.Fatalf("missing linked failure events: %#v", events)
	}
}
