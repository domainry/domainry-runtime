package projection

import (
	"time"

	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

const (
	operationsTechnicalRetentionPolicy = "operations.technical_receipt.v1"
	operationsLegalRetentionPolicy     = "operations.receipt.v1"
	operationsDaySeconds               = int64((24 * time.Hour) / time.Second)
)

var operationsDefinitionCatalog = []operationsmodel.OperationsDefinition{
	operationsDefinition("workflow.process.retry", "workflow", "workflow_process", false, "runtime.workflows.retry_ops_workflow_process", "process is failed or configuration_error and failed node exists"),
	operationsDefinition("workflow.process.resolve", "workflow", "workflow_process", false, "runtime.workflows.resolve_ops_workflow_process", "process is failed and resolution note is present"),
	operationsDefinition("workflow.execution.retry", "workflow", "workflow_execution", false, "runtime.workflows.retry_ops_workflow_execution", "execution is failed or dead_letter and attempt budget remains"),
	operationsDefinition("workflow.execution.resolve", "workflow", "workflow_execution", false, "runtime.workflows.resolve_ops_workflow_execution", "execution is dead_letter"),
	operationsDefinition("automation.rule.enable", "automation", "automation_rule", false, operationscontract.ActionEnableAutomationRule, "rule definition is valid"),
	operationsDefinition("automation.rule.disable", "automation", "automation_rule", false, operationscontract.ActionDisableAutomationRule, "rule exists"),
	operationsDefinition("runtime.publication.retry", "runtime", "runtime_publication_outbox", false, operationscontract.ActionRetryRuntimePublication, "Runtime publication handoff is failed and the owner result is not uncertain"),
	operationsSystemDefinition("backup.create", "persistence", "database", true, operationscontract.ActionCreateBackup, "target is immutable, encryption key is ready, and capacity budget permits"),
	operationsSystemDefinition("backup.restore", "persistence", "database", true, operationscontract.ActionRestoreBackup, "maintenance, drain, verified backup, change plan, and restore target are present"),
	operationsDefinition("retention.cleanup", "lifecycle", "retention_policy", true, operationscontract.ActionRunLifecycleCleanupJob, "dry-run, legal hold, reference, retention window, and batch limit checks pass"),
	operationsDefinition("idempotency.receipt.retry", "deployment", "idempotency_receipt", false, operationscontract.ActionRetryIdempotencyReceipt, "receipt is failed_retryable or has an expired processing lease"),
	operationsDefinition("idempotency.receipt.reset", "deployment", "idempotency_receipt", false, operationscontract.ActionResetIdempotencyReceipt, "receipt is terminal, explicitly confirmed, and owner policy permits reset"),
	operationsSystemDefinition("database.retirement.execute", "operations", "database_object", true, "runtime.operations.execute_database_retirement", "observation, comparison, backup, restore drill, maintenance, drain, approval, and quarantine evidence pass"),
	operationsDefinition("workspace.deletion.execute", "operations", "workspace", true, operationscontract.ActionDeleteWorkspace, "workspace is frozen, exported, outside retention, without legal hold, and cleanup is confirmed"),
	operationsSystemDefinition("runtime.maintenance.enable", "operations", "runtime", false, operationscontract.ActionEnableMaintenance, "reason and change reference are present and recovery routes remain available"),
	operationsSystemDefinition("runtime.maintenance.disable", "operations", "runtime", false, operationscontract.ActionDisableMaintenance, "readiness blockers are resolved and operator explicitly confirms recovery"),
	operationsSystemDefinition("worker.owner.pause", "operations", "worker_owner", false, operationscontract.ActionPauseWorkerOwner, "owner is registered and reason is present"),
	operationsSystemDefinition("worker.owner.resume", "operations", "worker_owner", false, operationscontract.ActionResumeWorkerOwner, "owner is registered and dependencies are ready"),
	operationsSystemDefinition("runtime.instance.drain", "operations", "runtime_instance", true, operationscontract.ActionDrainRuntimeInstance, "instance identity and drain timeout are present"),
	operationsSystemDefinition("runtime.instance.undrain", "operations", "runtime_instance", false, operationscontract.ActionUndrainRuntimeInstance, "instance is healthy, dependencies are ready and operator explicitly confirms admission"),
	operationsSystemDefinition("worker.lease.force_release", "operations", "worker_lease", false, operationscontract.ActionForceReleaseLease, "lease is expired or independently verified stuck and fencing remains active"),
	operationsTechnicalDefinition("dead_letter.inspect", "operations", "dead_letter", false, operationscontract.ActionInspectDeadLetter, "owner and dead-letter identity are present"),
	operationsDefinition("dead_letter.resolve", "operations", "dead_letter", false, operationscontract.ActionResolveDeadLetter, "owner accepts the explicit resolution transition"),
	operationsDefinition("dead_letter.retry", "operations", "dead_letter", false, operationscontract.ActionRetryDeadLetter, "owner reauthorizes replay and current dependency readiness passes"),
	operationsDefinition("dead_letter.ack", "operations", "dead_letter", false, operationscontract.ActionAcknowledgeDeadLetter, "owner permits acknowledgement and evidence note is present"),
	operationsTechnicalDefinition("bulk_operation.dry_run", "operations", "bulk_operation", true, operationscontract.ActionDryRunBulkDeadLetters, "filter and bounded item limit are valid"),
	operationsDefinition("bulk_operation.apply", "operations", "bulk_operation", true, operationscontract.ActionApplyBulkDeadLetters, "matching dry-run receipt, confirmation, and bounded item limit are present"),
	operationsSystemTechnicalDefinition("diagnostics.snapshot", "operations", "runtime", false, operationscontract.ActionCaptureDiagnostics, "requested sections and bounded cost limits are valid"),
	operationsSystemDefinition("break_glass.enable", "operations", "runtime", false, operationscontract.ActionEnableBreakGlass, "incident reference, expiry, approver, alert target, and strong audit are present"),
	operationsSystemDefinition("break_glass.disable", "operations", "runtime", false, operationscontract.ActionDisableBreakGlass, "active grant exists and revocation evidence is recorded"),
}

func operationsDefinition(kind, owner, resourceType string, longRunning bool, actionKey, precondition string) operationsmodel.OperationsDefinition {
	return operationsmodel.OperationsDefinition{
		Kind: kind, Owner: owner, ActionKey: actionKey, ExecutionScope: operationsmodel.OperationsExecutionWorkspace,
		ResourceType: resourceType, Preconditions: []string{precondition}, Idempotency: "caller_key_and_semantic_fingerprint",
		AuditEvent: kind + ".requested", ReceiptType: "operations.receipt.v1",
		FailureSemantics: []operationsmodel.OperationsFailureClass{operationsmodel.OperationsFailureRetryable, operationsmodel.OperationsFailureTerminal, operationsmodel.OperationsFailureManualIntervention},
		LongRunning:      longRunning,
		Retention: operationsmodel.OperationsRetentionPolicy{
			PolicyKey: operationsLegalRetentionPolicy, Class: operationsmodel.OperationsRetentionLegalAudit,
			SucceededRetentionSeconds: 365 * operationsDaySeconds,
			FailedRetentionSeconds:    7 * 365 * operationsDaySeconds,
			MinimumRetentionSeconds:   90 * operationsDaySeconds,
		},
	}
}

func operationsTechnicalDefinition(kind, owner, resourceType string, longRunning bool, actionKey, precondition string) operationsmodel.OperationsDefinition {
	definition := operationsDefinition(kind, owner, resourceType, longRunning, actionKey, precondition)
	definition.Retention = operationsmodel.OperationsRetentionPolicy{
		PolicyKey: operationsTechnicalRetentionPolicy, Class: operationsmodel.OperationsRetentionTechnical,
		SucceededRetentionSeconds: 30 * operationsDaySeconds,
		FailedRetentionSeconds:    30 * operationsDaySeconds,
		MinimumRetentionSeconds:   7 * operationsDaySeconds,
	}
	return definition
}

func operationsSystemDefinition(kind, owner, resourceType string, longRunning bool, actionKey, precondition string) operationsmodel.OperationsDefinition {
	definition := operationsDefinition(kind, owner, resourceType, longRunning, actionKey, precondition)
	definition.ExecutionScope = operationsmodel.OperationsExecutionSystem
	return definition
}

func operationsSystemTechnicalDefinition(kind, owner, resourceType string, longRunning bool, actionKey, precondition string) operationsmodel.OperationsDefinition {
	definition := operationsTechnicalDefinition(kind, owner, resourceType, longRunning, actionKey, precondition)
	definition.ExecutionScope = operationsmodel.OperationsExecutionSystem
	return definition
}

func OperationsDefinitions() []operationsmodel.OperationsDefinition {
	result := make([]operationsmodel.OperationsDefinition, len(operationsDefinitionCatalog))
	copy(result, operationsDefinitionCatalog)
	for index := range result {
		result[index].Preconditions = append([]string(nil), result[index].Preconditions...)
		result[index].FailureSemantics = append([]operationsmodel.OperationsFailureClass(nil), result[index].FailureSemantics...)
	}
	return result
}

func OperationsDefinition(kind string) (operationsmodel.OperationsDefinition, bool) {
	for _, definition := range operationsDefinitionCatalog {
		if definition.Kind == kind {
			return definition, true
		}
	}
	return operationsmodel.OperationsDefinition{}, false
}
