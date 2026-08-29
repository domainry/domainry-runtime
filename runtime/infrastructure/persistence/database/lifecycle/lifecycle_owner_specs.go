package lifecycle

import (
	ormbuilder "github.com/domainry/domainry-orm/builder"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func DefaultOwnerExecutors(store *database.RuntimeStore, objects ...definitionmodel.ObjectSchema) []lifecyclecontract.OwnerLifecycleExecutor {
	metadataHasNewer := func(outer string) ormbuilder.Predicate {
		newer := ormbuilder.NewSelectBuilder(store.SQLRenderer, "metadata_definition_versions").Alias("newer").Columns("id").Where(ormbuilder.And(
			ormbuilder.EqualExpressions(ormbuilder.QualifiedColumn("newer", "resource_type"), ormbuilder.QualifiedColumn(outer, "resource_type")),
			ormbuilder.EqualExpressions(ormbuilder.QualifiedColumn("newer", "resource_key"), ormbuilder.QualifiedColumn(outer, "resource_key")),
			ormbuilder.Or(
				ormbuilder.GreaterThanExpressions(ormbuilder.QualifiedColumn("newer", "created_at"), ormbuilder.QualifiedColumn(outer, "created_at")),
				ormbuilder.And(ormbuilder.EqualExpressions(ormbuilder.QualifiedColumn("newer", "created_at"), ormbuilder.QualifiedColumn(outer, "created_at")), ormbuilder.GreaterThanExpressions(ormbuilder.QualifiedColumn("newer", "id"), ormbuilder.QualifiedColumn(outer, "id"))),
			),
		))
		return ormbuilder.ExistsSubquery(newer)
	}
	baseExecutors := []OwnerExecutor{
		{store: store, owner: "runtime_security", specs: []cleanupSpec{
			{policyKey: "ratelimit.bucket.v1", table: "runtime_rate_limit_bucket", idColumn: "bucket_key", timeColumn: "updated_at_ns", unixNanoTime: true},
		}},
		{store: store, owner: "action", specs: []cleanupSpec{
			{policyKey: "execution.idempotency_receipt.v1", table: "business_action_executions", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "expires_at", statusColumn: "status", ineligibleStatuses: []string{"pending", "processing"}},
		}},
		{store: store, owner: "record", specs: []cleanupSpec{
			{policyKey: "execution.idempotency_receipt.v1", table: "record_mutation_executions", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "expires_at", statusColumn: "status", ineligibleStatuses: []string{"pending", "processing"}},
			{policyKey: "record.batch_artifact.v1", table: "record_batch_jobs", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at", statusColumn: "status", ineligibleStatuses: []string{"pending", "processing", "retrying"}},
		}},
		{store: store, owner: "operations", specs: []cleanupSpec{
			{policyKey: "operations.receipt.v1", table: "runtime_operations", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at", statusColumn: "status", ineligibleStatuses: []string{"pending", "running", "pausing"}},
			{policyKey: "operations.break_glass.v1", table: "runtime_break_glass_grants", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "expires_at"},
		}},
		{store: store, owner: "integration", specs: []cleanupSpec{
			{policyKey: "integration.webhook_nonce.v1", table: "integration_webhook_nonces", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "expires_at"},
			{policyKey: "integration.event.v1", table: "integration_events", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at", statusColumn: "status", eligibleStatuses: []string{"processed", "ignored"}, retentionGroup: "succeeded", referenceChecks: []cleanupReferenceCheck{{table: "integration_outbox_messages", tenantColumn: "workspace_id", referenceColumn: "event_id"}, {table: "integration_invocations", tenantColumn: "workspace_id", referenceColumn: "event_id"}}},
			{policyKey: "integration.event.v1", table: "integration_events", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at", statusColumn: "status", eligibleStatuses: []string{"failed", "dead_letter", "quarantined"}, retentionGroup: "failed", referenceChecks: []cleanupReferenceCheck{{table: "integration_outbox_messages", tenantColumn: "workspace_id", referenceColumn: "event_id"}, {table: "integration_invocations", tenantColumn: "workspace_id", referenceColumn: "event_id"}}},
			{policyKey: "integration.delivery_evidence.v1", table: "integration_outbox_messages", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at", statusColumn: "status", ineligibleStatuses: []string{"pending", "processing", "retrying"}},
			{policyKey: "integration.delivery_evidence.v1", table: "integration_invocations", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at", statusColumn: "status", ineligibleStatuses: []string{"pending", "processing"}},
			{policyKey: "integration.delivery_evidence.v1", table: "transaction_boundary_intents", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at", statusColumn: "status", ineligibleStatuses: []string{"pending", "processing", "compensating"}},
			{policyKey: "technical.lease_checkpoint.v1", table: "integration_credential_refresh_leases", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "lease_expires_at"},
		}},
		{store: store, owner: "workflow", specs: []cleanupSpec{
			{policyKey: "workflow.definition.v1", table: "workflow_definition_versions", idColumn: "id", timeColumn: "updated_at", statusColumn: "status", eligibleStatuses: []string{"archived"}, referenceChecks: []cleanupReferenceCheck{{table: "workflow_definition_identities", referenceColumn: "current_draft_version_id"}, {table: "workflow_definition_identities", referenceColumn: "current_published_version_id"}, {table: "workflow_process_instances", referenceColumn: "workflow_definition_version_id"}}},
			{policyKey: "workflow.receipt.v1", table: "workflow_execution_receipts", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "expires_at", statusColumn: "status", ineligibleStatuses: []string{"processing"}},
			{policyKey: "workflow.execution.v1", table: "workflow_process_instances", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at", statusColumn: "status", eligibleStatuses: []string{"completed", "rejected", "cancelled", "resolved"}, retentionGroup: "succeeded", workflowProcessChildren: true},
			{policyKey: "workflow.execution.v1", table: "_workflow_executions", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at", statusColumn: "status", eligibleStatuses: []string{"succeeded", "completed"}, retentionGroup: "succeeded", referenceChecks: []cleanupReferenceCheck{{table: "integration_invocations", tenantColumn: "workspace_id", referenceColumn: "workflow_execution_id"}}},
			{policyKey: "workflow.execution.v1", table: "_workflow_executions", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at", statusColumn: "status", eligibleStatuses: []string{"failed", "dead_letter", "cancelled"}, retentionGroup: "failed", referenceChecks: []cleanupReferenceCheck{{table: "integration_invocations", tenantColumn: "workspace_id", referenceColumn: "workflow_execution_id"}}},
		}},
		{store: store, owner: "automation", specs: []cleanupSpec{
			{policyKey: "automation.execution.v1", table: "automation_rule_executions", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at", statusColumn: "status", eligibleStatuses: []string{"succeeded", "completed"}, retentionGroup: "succeeded"},
			{policyKey: "automation.execution.v1", table: "automation_rule_executions", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at", statusColumn: "status", eligibleStatuses: []string{"failed", "dead_letter", "cancelled"}, retentionGroup: "failed"},
			{policyKey: "automation.execution.v1", table: "automation_instruction_executions", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at", statusColumn: "status", eligibleStatuses: []string{"succeeded", "completed"}, retentionGroup: "succeeded"},
			{policyKey: "automation.execution.v1", table: "automation_instruction_executions", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at", statusColumn: "status", eligibleStatuses: []string{"failed", "dead_letter", "cancelled"}, retentionGroup: "failed"},
		}},
		{store: store, owner: "metadata", specs: []cleanupSpec{
			{policyKey: "metadata.definition_history.v1", table: "metadata_definition_versions", idColumn: "id", timeColumn: "created_at", additionalPredicate: metadataHasNewer},
		}},
		{store: store, owner: "audit", specs: []cleanupSpec{
			{policyKey: "audit.evidence.v1", table: "_audit_events", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "created_at"},
		}},
	}
	executors := make([]lifecyclecontract.OwnerLifecycleExecutor, 0, len(baseExecutors)+3)
	for _, executor := range baseExecutors {
		executors = append(executors, executor)
	}
	objectMap := map[string]definitionmodel.ObjectSchema{}
	for _, object := range objects {
		objectMap[object.Key] = object
	}
	for index := range executors {
		recordExecutor := executors[index].(OwnerExecutor)
		if recordExecutor.owner != "record" {
			continue
		}
		for _, object := range objects {
			if !recordpolicy.RecordUsesSoftDelete(object) {
				continue
			}
			recordExecutor.specs = append(recordExecutor.specs, cleanupSpec{
				policyKey: "record.object.default.v1", table: object.Key, idColumn: "id",
				tenantColumn: "workspace_id", timeColumn: "deleted_at", statusColumn: "status",
				eligibleStatuses: []string{"deleted"}, referenceChecks: []cleanupReferenceCheck{{
					table: "workflow_process_instances", tenantColumn: "workspace_id", referenceColumn: "record_id",
					fixedColumn: "object_key", fixedValue: object.Key,
				}},
			})
		}
		executors[index] = recordExecutor
		break
	}
	reportSpecs := []cleanupSpec{{policyKey: "report.download.v1", table: "report_export_artifacts", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "expires_at"}}
	if _, ok := objectMap["download_task"]; ok {
		reportSpecs = append(reportSpecs, cleanupSpec{policyKey: "report.download.v1", table: "download_task", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at", statusColumn: "status", eligibleStatuses: []string{"expired", "failed", "cancelled"}})
	}
	if _, ok := objectMap["report_export_audit"]; ok {
		reportSpecs = append(reportSpecs, cleanupSpec{policyKey: "report.export.v1", table: "report_export_audit", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at", statusColumn: "status", eligibleStatuses: []string{"completed", "failed", "cancelled"}, retentionGroup: "succeeded", referenceChecks: []cleanupReferenceCheck{{table: "download_task", tenantColumn: "workspace_id", referenceColumn: "report_export_audit"}}})
	}
	if _, ok := objectMap["report_query_run"]; ok {
		reportSpecs = append(reportSpecs, cleanupSpec{policyKey: "report.export.v1", table: "report_query_run", idColumn: "id", tenantColumn: "workspace_id", timeColumn: "updated_at", statusColumn: "status", eligibleStatuses: []string{"completed", "failed", "cancelled"}, retentionGroup: "succeeded", referenceChecks: []cleanupReferenceCheck{{table: "report_export_audit", tenantColumn: "workspace_id", referenceColumn: "report_query_run"}}})
	}
	executors = append(executors, ReportOwnerExecutor{store: store, db: store.DB(), relational: OwnerExecutor{store: store, db: store.DB(), owner: "report", specs: reportSpecs}})
	executors = append(executors, AgentOwnerExecutor{store: store, db: store.DB()})
	return executors
}

func lifecycleConfigEnabled(config map[string]any, key string) bool {
	value, _ := config[key].(bool)
	return value
}
