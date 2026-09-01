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
	for _, definition := range definitions {
		if seen[definition.Kind] {
			t.Errorf("duplicate kind %s", definition.Kind)
		}
		seen[definition.Kind] = true
		if strings.TrimSpace(definition.Kind) == "" || strings.TrimSpace(definition.Owner) == "" || strings.TrimSpace(definition.ResourceType) == "" || len(definition.Permissions) == 0 || len(definition.Preconditions) == 0 || strings.TrimSpace(definition.Idempotency) == "" || strings.TrimSpace(definition.AuditEvent) == "" || strings.TrimSpace(definition.ReceiptType) == "" {
			t.Errorf("incomplete definition: %#v", definition)
		}
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
	for _, requiredSystemKind := range []string{"metadata.migration.apply", "backup.restore", "runtime.maintenance.enable", "worker.owner.pause", "runtime.instance.drain", "diagnostics.snapshot", "break_glass.enable"} {
		definition, found := OperationsDefinition(requiredSystemKind)
		if !found || definition.ExecutionScope != operationsmodel.OperationsExecutionSystem {
			t.Fatalf("operation %q must use system execution scope: %#v", requiredSystemKind, definition)
		}
	}
}

func TestWorkflowProcessOperationsPreserveOwnerLevelAuthorization(t *testing.T) {
	for _, kind := range []string{"workflow.process.cancel", "workflow.process.resolve"} {
		definition, found := OperationsDefinition(kind)
		if !found {
			t.Fatalf("missing operation definition %s", kind)
		}
		allowsWorkflowRunner := false
		for _, permission := range definition.Permissions {
			if permission == "workflow.run" {
				allowsWorkflowRunner = true
				break
			}
		}
		if !allowsWorkflowRunner {
			t.Fatalf("%s must admit workflow runners so the Workflow owner can enforce initiator and operator policy", kind)
		}
	}
}

func TestSchedulerManualRunDefinitionPreservesPublicOwnerPermission(t *testing.T) {
	definition, found := OperationsDefinition("scheduler.job.run")
	if !found {
		t.Fatal("missing scheduler.job.run operation definition")
	}
	if len(definition.Permissions) != 1 || definition.Permissions[0] != "runtime.scheduler.run_ops_scheduler_job" {
		t.Fatalf("scheduler.job.run permission must exactly match its Runtime endpoint Action: %#v", definition.Permissions)
	}
}
