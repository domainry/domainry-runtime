package projection

import (
	"strings"
	"testing"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

func TestOperationsDefinitionCatalogDeclaresCompleteOperationContract(t *testing.T) {
	definitions := OperationsDefinitions()
	if len(definitions) < 31 {
		t.Fatalf("operation definitions=%d", len(definitions))
	}
	seen := map[string]bool{}
	actionKeys := map[string]string{}
	for _, definition := range definitions {
		if seen[definition.Kind] {
			t.Errorf("duplicate kind %s", definition.Kind)
		}
		seen[definition.Kind] = true
		if strings.TrimSpace(definition.Kind) == "" || strings.TrimSpace(definition.Owner) == "" || strings.TrimSpace(definition.ActionKey) == "" || strings.TrimSpace(definition.ResourceType) == "" || len(definition.Preconditions) == 0 || strings.TrimSpace(definition.Idempotency) == "" || strings.TrimSpace(definition.AuditEvent) == "" || strings.TrimSpace(definition.ReceiptType) == "" {
			t.Errorf("incomplete definition: %#v", definition)
		}
		if previous := actionKeys[definition.ActionKey]; previous != "" {
			t.Errorf("operation %q reuses Action %q from %q", definition.Kind, definition.ActionKey, previous)
		}
		actionKeys[definition.ActionKey] = definition.Kind
		if definition.ExecutionScope != operationsmodel.OperationsExecutionWorkspace && definition.ExecutionScope != operationsmodel.OperationsExecutionSystem {
			t.Errorf("invalid execution scope: %#v", definition)
		}
		if len(definition.FailureSemantics) != 3 {
			t.Errorf("failure semantics incomplete: %#v", definition)
		}
	}
	for _, required := range []string{"scheduler.run.retry", "workflow.execution.retry", "agent.task.retry", "agent.task.cancel", "agent.task.resolve", "agent.task.reconcile", "integration.event.replay", "backup.create", "backup.restore", "retention.cleanup", "database.retirement.execute", "runtime.maintenance.enable", "worker.owner.pause", "runtime.instance.drain", "worker.lease.force_release", "dead_letter.retry", "bulk_operation.dry_run", "diagnostics.snapshot", "break_glass.enable"} {
		if !seen[required] {
			t.Errorf("missing operation definition %s", required)
		}
	}
	for _, requiredSystemKind := range []string{"backup.restore", "runtime.maintenance.enable", "worker.owner.pause", "runtime.instance.drain", "diagnostics.snapshot", "break_glass.enable"} {
		definition, found := OperationsDefinition(requiredSystemKind)
		if !found || definition.ExecutionScope != operationsmodel.OperationsExecutionSystem {
			t.Fatalf("operation %q must use system execution scope: %#v", requiredSystemKind, definition)
		}
	}
}

func TestWorkflowOperationsUseTheirExactEndpointActions(t *testing.T) {
	want := map[string]string{
		"workflow.process.retry":     "runtime.workflows.retry_ops_workflow_process",
		"workflow.process.resolve":   "runtime.workflows.resolve_ops_workflow_process",
		"workflow.execution.retry":   "runtime.workflows.retry_ops_workflow_execution",
		"workflow.execution.resolve": "runtime.workflows.resolve_ops_workflow_execution",
	}
	for kind, actionKey := range want {
		definition, found := OperationsDefinition(kind)
		if !found {
			t.Fatalf("missing operation definition %s", kind)
		}
		if definition.ActionKey != actionKey {
			t.Fatalf("%s action=%s want exact Action %s", kind, definition.ActionKey, actionKey)
		}
	}
	if _, found := OperationsDefinition("workflow.process.cancel"); found {
		t.Fatal("unexposed workflow process cancel must not remain as an Operations kind")
	}
}

func TestSchedulerManualRunDefinitionPreservesPublicOwnerPermission(t *testing.T) {
	definition, found := OperationsDefinition("scheduler.job.run")
	if !found {
		t.Fatal("missing scheduler.job.run operation definition")
	}
	if definition.ActionKey != "scheduler.definitions.run" {
		t.Fatalf("scheduler.job.run must exactly match its Runtime endpoint Action: %q", definition.ActionKey)
	}
}
