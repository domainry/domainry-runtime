package projection

import operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"

var operationsDefinitionCatalog = []operationsmodel.OperationsDefinition{
	operationsDefinition("scheduler.job.run", "scheduler", "scheduler_definition", false, []string{"scheduler.command"}, "published definition exists and target readiness passes"),
	operationsDefinition("scheduler.definition.reschedule", "scheduler", "scheduler_definition", false, []string{"scheduler.command"}, "published definition exists and next_run_at is valid"),
	operationsDefinition("scheduler.run.retry", "scheduler", "scheduler_run", false, []string{"scheduler.command"}, "run is failed, retryable, and below max attempts"),
	operationsDefinition("scheduler.run.cancel", "scheduler", "scheduler_run", false, []string{"scheduler.command"}, "run is cancellable and caller observes current status"),
	operationsDefinition("scheduler.dead_letter.resolve", "scheduler", "scheduler_dead_letter", false, []string{"scheduler.command"}, "dead letter is unresolved and resolution note is present"),
	operationsDefinition("workflow.process.retry", "workflow", "workflow_process", false, []string{"workflow.process.operate"}, "process is failed or configuration_error and failed node exists"),
	operationsDefinition("workflow.process.cancel", "workflow", "workflow_process", false, []string{"workflow.process.operate", "workflow.run"}, "process is running or waiting and caller may operate it"),
	operationsDefinition("workflow.process.resolve", "workflow", "workflow_process", false, []string{"workflow.process.operate", "workflow.run"}, "process is failed and resolution note is present"),
	operationsDefinition("workflow.execution.retry", "workflow", "workflow_execution", false, []string{"workflow.retry"}, "execution is failed or dead_letter and attempt budget remains"),
	operationsDefinition("workflow.execution.resolve", "workflow", "workflow_execution", false, []string{"workflow.resolve"}, "execution is dead_letter"),
	operationsDefinition("agent.task.retry", "agent", "agent_task_run", false, []string{"agent.task.operate"}, "task is failed, cancelled, or dead_letter"),
	operationsDefinition("agent.task.cancel", "agent", "agent_task_run", false, []string{"agent.task.operate"}, "task is pending, running, retry_scheduled, or waiting_approval"),
	operationsDefinition("agent.task.resolve", "agent", "agent_task_run", false, []string{"agent.task.operate"}, "task is terminal and requires manual reconciliation"),
	operationsDefinition("agent.task.reconcile", "agent", "agent_task_run", false, []string{"agent.task.operate"}, "task has uncertain provider evidence and an external run id"),
	operationsDefinition("automation.rule.enable", "automation", "automation_rule", false, []string{"automation.rule.write"}, "rule definition is valid"),
	operationsDefinition("automation.rule.disable", "automation", "automation_rule", false, []string{"automation.rule.write"}, "rule exists"),
	operationsDefinition("integration.event.retry", "integration", "integration_event", false, []string{"integration.retry"}, "event is retryable and current connector configuration is ready"),
	operationsDefinition("integration.event.replay", "integration", "integration_event", false, []string{"integration.retry"}, "event workspace, connector, configuration, and secret readiness are rechecked"),
	operationsDefinition("runtime.publication.retry", "runtime", "runtime_publication_outbox", false, []string{"integration.retry"}, "Runtime publication handoff is failed and the owner result is not uncertain"),
	operationsDefinition("integration.invocation.reconcile", "integration", "integration_invocation", true, []string{"integration.retry"}, "invocation is stale and provider evidence can be queried"),
	operationsSystemDefinition("metadata.migration.apply", "metadata", "metadata_migration", true, []string{"metadata.write"}, "migration plan is current, reviewed, and backup readiness passes"),
	operationsSystemDefinition("backup.create", "persistence", "database", true, []string{"runtime.backup.create"}, "target is immutable, encryption key is ready, and capacity budget permits"),
	operationsSystemDefinition("backup.restore", "persistence", "database", true, []string{"runtime.backup.restore"}, "maintenance, drain, verified backup, change plan, and restore target are present"),
	operationsDefinition("retention.cleanup", "lifecycle", "retention_policy", true, []string{"runtime.retention.execute"}, "dry-run, legal hold, reference, retention window, and batch limit checks pass"),
	operationsDefinition("idempotency.receipt.retry", "deployment", "idempotency_receipt", false, []string{"runtime.idempotency.manage"}, "receipt is failed_retryable or has an expired processing lease"),
	operationsDefinition("idempotency.receipt.reset", "deployment", "idempotency_receipt", false, []string{"runtime.idempotency.manage"}, "receipt is terminal, explicitly confirmed, and owner policy permits reset"),
	operationsSystemDefinition("database.retirement.execute", "operations", "database_object", true, []string{"runtime.database.retirement.manage"}, "observation, comparison, backup, restore drill, maintenance, drain, approval, and quarantine evidence pass"),
	operationsDefinition("workspace.deletion.execute", "operations", "workspace", true, []string{"runtime.workspace.delete"}, "workspace is frozen, exported, outside retention, without legal hold, and cleanup is confirmed"),
	operationsSystemDefinition("runtime.maintenance.enable", "operations", "runtime", false, []string{"runtime.maintenance.write"}, "reason and change reference are present and recovery routes remain available"),
	operationsSystemDefinition("runtime.maintenance.disable", "operations", "runtime", false, []string{"runtime.maintenance.write"}, "readiness blockers are resolved and operator explicitly confirms recovery"),
	operationsSystemDefinition("worker.owner.pause", "operations", "worker_owner", false, []string{"runtime.worker.control"}, "owner is registered and reason is present"),
	operationsSystemDefinition("worker.owner.resume", "operations", "worker_owner", false, []string{"runtime.worker.control"}, "owner is registered and dependencies are ready"),
	operationsSystemDefinition("runtime.instance.drain", "operations", "runtime_instance", true, []string{"runtime.instance.drain"}, "instance identity and drain timeout are present"),
	operationsSystemDefinition("runtime.instance.undrain", "operations", "runtime_instance", false, []string{"runtime.instance.drain"}, "instance is healthy, dependencies are ready, and operator explicitly confirms admission"),
	operationsSystemDefinition("worker.lease.force_release", "operations", "worker_lease", false, []string{"runtime.worker.force_release"}, "lease is expired or independently verified stuck and fencing remains active"),
	operationsDefinition("dead_letter.inspect", "operations", "dead_letter", false, []string{"runtime.dead_letter.read"}, "owner and dead-letter identity are present"),
	operationsDefinition("dead_letter.resolve", "operations", "dead_letter", false, []string{"runtime.dead_letter.write"}, "owner accepts the explicit resolution transition"),
	operationsDefinition("dead_letter.retry", "operations", "dead_letter", false, []string{"runtime.dead_letter.write"}, "owner reauthorizes replay and current dependency readiness passes"),
	operationsDefinition("dead_letter.ack", "operations", "dead_letter", false, []string{"runtime.dead_letter.write"}, "owner permits acknowledgement and evidence note is present"),
	operationsDefinition("bulk_operation.dry_run", "operations", "bulk_operation", true, []string{"runtime.bulk.execute"}, "filter and bounded item limit are valid"),
	operationsDefinition("bulk_operation.apply", "operations", "bulk_operation", true, []string{"runtime.bulk.execute"}, "matching dry-run receipt, confirmation, and bounded item limit are present"),
	operationsSystemDefinition("diagnostics.snapshot", "operations", "runtime", false, []string{"runtime.diagnostics.read"}, "requested sections and bounded cost limits are valid"),
	operationsSystemDefinition("break_glass.enable", "operations", "runtime", false, []string{"runtime.break_glass"}, "incident reference, expiry, approver, alert target, and strong audit are present"),
	operationsSystemDefinition("break_glass.disable", "operations", "runtime", false, []string{"runtime.break_glass"}, "active grant exists and revocation evidence is recorded"),
}

func operationsDefinition(kind, owner, resourceType string, longRunning bool, permissions []string, precondition string) operationsmodel.OperationsDefinition {
	return operationsmodel.OperationsDefinition{
		Kind: kind, Owner: owner, Permissions: permissions, ExecutionScope: operationsmodel.OperationsExecutionWorkspace,
		ResourceType: resourceType, Preconditions: []string{precondition}, Idempotency: "caller_key_and_semantic_fingerprint",
		AuditEvent: kind + ".requested", ReceiptType: "operations.receipt.v1",
		FailureSemantics: []operationsmodel.OperationsFailureClass{operationsmodel.OperationsFailureRetryable, operationsmodel.OperationsFailureTerminal, operationsmodel.OperationsFailureManualIntervention},
		LongRunning:      longRunning,
	}
}

func operationsSystemDefinition(kind, owner, resourceType string, longRunning bool, permissions []string, precondition string) operationsmodel.OperationsDefinition {
	definition := operationsDefinition(kind, owner, resourceType, longRunning, permissions, precondition)
	definition.ExecutionScope = operationsmodel.OperationsExecutionSystem
	return definition
}

func OperationsDefinitions() []operationsmodel.OperationsDefinition {
	result := make([]operationsmodel.OperationsDefinition, len(operationsDefinitionCatalog))
	copy(result, operationsDefinitionCatalog)
	for index := range result {
		result[index].Permissions = append([]string(nil), result[index].Permissions...)
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
