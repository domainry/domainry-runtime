package schema

import "context"

type EvidenceSchemaCapabilities struct {
	Workflow            bool
	Automation          bool
	Lifecycle           bool
	ReleaseCoordination bool
}

func FullEvidenceSchemaCapabilities() EvidenceSchemaCapabilities {
	return EvidenceSchemaCapabilities{Workflow: true, Automation: true, Lifecycle: true, ReleaseCoordination: true}
}

func EnsureEvidenceSchema(ctx context.Context, s Store) error {
	return EnsureEvidenceSchemaFor(ctx, s, FullEvidenceSchemaCapabilities())
}

func EnsureEvidenceSchemaFor(ctx context.Context, s Store, capabilities EvidenceSchemaCapabilities) error {
	text := s.ApplicationSchemaIDColumnType()
	indexText := s.RuntimeProfile().TextKeyColumnType(191)
	timestampText := s.RuntimeProfile().TextKeyColumnType(40)
	types := s.RuntimeProfile().EvidenceSchemaTypes(text)
	idempotencyScopeText := types.IdempotencyScope
	tables := map[string][]string{
		"_release_cohorts": {
			"cohort_key " + text + " PRIMARY KEY",
			"combination_sha256 " + indexText + " NOT NULL DEFAULT ''",
			"identity_json TEXT NOT NULL DEFAULT ''",
			"generation BIGINT NOT NULL DEFAULT 0",
			"revision BIGINT NOT NULL DEFAULT 0",
			"updated_at " + text + " NOT NULL DEFAULT ''",
		},
		"_release_instances": {
			"instance_id " + text + " PRIMARY KEY",
			"combination_sha256 " + indexText + " NOT NULL",
			"generation BIGINT NOT NULL",
			"lease_expires_at " + timestampText + " NOT NULL",
			"joined_at " + text + " NOT NULL",
			"heartbeat_at " + text + " NOT NULL",
		},
		"_workflow_executions": {
			"workspace_id " + idempotencyScopeText + " NOT NULL",
			"id " + text + " NOT NULL",
			"operation_id " + text + " NOT NULL DEFAULT ''",
			"workflow_key " + text + " NOT NULL",
			"name TEXT NOT NULL",
			"trigger " + text + " NOT NULL",
			"status " + indexText + " NOT NULL",
			"action_type " + text,
			"action_json TEXT NOT NULL",
			"payload_json TEXT NOT NULL",
			"result_json TEXT NOT NULL",
			"process_id " + indexText,
			"node_id " + indexText,
			"object_key " + text,
			"record_id " + text,
			"actor_id " + text,
			"run_as " + text,
			"idempotency_key " + text,
			"attempt INTEGER NOT NULL DEFAULT 0",
			"max_attempts INTEGER NOT NULL DEFAULT 0",
			"next_run_at " + text,
			"last_error TEXT",
			"lease_owner " + text + " NOT NULL DEFAULT ''",
			"lease_expires_at " + text + " NOT NULL DEFAULT ''",
			"fencing_token BIGINT NOT NULL DEFAULT 0",
			"message TEXT",
			"created_at " + text + " NOT NULL",
			"updated_at " + text + " NOT NULL",
		},
		"_action_assurance_grants": {
			"id " + text + " PRIMARY KEY",
			"token_hash " + text + " NOT NULL",
			"workspace_id " + idempotencyScopeText + " NOT NULL",
			"user_id " + idempotencyScopeText + " NOT NULL",
			"action_key " + idempotencyScopeText + " NOT NULL",
			"object_key " + idempotencyScopeText + " NOT NULL",
			"record_id " + idempotencyScopeText + " NOT NULL DEFAULT ''",
			"payload_digest " + text + " NOT NULL",
			"methods_json TEXT NOT NULL",
			"approval_version " + text + " NOT NULL DEFAULT ''",
			"approval_hash " + text + " NOT NULL DEFAULT ''",
			"issued_at " + text + " NOT NULL",
			"expires_at " + timestampText + " NOT NULL",
			"consumed_at " + timestampText + " NOT NULL DEFAULT ''",
		},
		"_worker_scopes": {
			"id " + text + " PRIMARY KEY",
			"owner " + idempotencyScopeText + " NOT NULL",
			"scope_key " + idempotencyScopeText + " NOT NULL",
			"cursor " + text + " NOT NULL DEFAULT ''",
			"checkpoint BIGINT NOT NULL DEFAULT 0",
			"capacity BIGINT NOT NULL DEFAULT 0",
			"lease_owner " + text + " NOT NULL DEFAULT ''",
			"lease_expires_at " + text + " NOT NULL DEFAULT ''",
			"fencing_token BIGINT NOT NULL DEFAULT 0",
			"last_started_at " + text + " NOT NULL DEFAULT ''",
			"last_completed_at " + text + " NOT NULL DEFAULT ''",
			"last_error TEXT NOT NULL DEFAULT ''",
			"updated_at " + text + " NOT NULL DEFAULT ''",
		},
		"_artifacts": {
			"workspace_id " + idempotencyScopeText + " NOT NULL",
			"id " + text + " PRIMARY KEY",
			"owner " + idempotencyScopeText + " NOT NULL",
			"kind " + idempotencyScopeText + " NOT NULL",
			"idempotency_key " + idempotencyScopeText + " NOT NULL",
			"created_by " + text + " NOT NULL",
			"owner_org_id " + text + " NOT NULL DEFAULT ''",
			"filename TEXT NOT NULL",
			"media_type " + text + " NOT NULL",
			"content_sha256 " + text + " NOT NULL",
			"size_bytes BIGINT NOT NULL",
			"storage_reference TEXT NOT NULL",
			"status " + indexText + " NOT NULL",
			"expires_at " + timestampText + " NOT NULL DEFAULT ''",
			"scan_status " + indexText + " NOT NULL",
			"download_token_sha256 " + text + " NOT NULL DEFAULT ''",
			"authorization_scope_sha256 " + text + " NOT NULL DEFAULT ''",
			"metadata_json TEXT NOT NULL",
			"created_at " + timestampText + " NOT NULL",
			"updated_at " + timestampText + " NOT NULL",
		},
		"_artifact_bindings": {
			"workspace_id " + idempotencyScopeText + " NOT NULL",
			"id " + text + " PRIMARY KEY",
			"artifact_id " + text + " NOT NULL",
			"owner " + idempotencyScopeText + " NOT NULL",
			"kind " + idempotencyScopeText + " NOT NULL",
			"resource_type " + idempotencyScopeText + " NOT NULL",
			"resource_id " + text + " NOT NULL",
			"field_key " + text + " NOT NULL DEFAULT ''",
			"metadata_json TEXT NOT NULL",
			"created_at " + timestampText + " NOT NULL",
		},
		"_automation_runs": {
			"id " + text + " PRIMARY KEY",
			"workspace_id " + indexText + " NOT NULL",
			"run_kind " + indexText + " NOT NULL",
			"idempotency_key " + indexText + " NOT NULL",
			"rule_key " + indexText + " NOT NULL",
			"object_key " + indexText + " NOT NULL",
			"record_id " + indexText + " NOT NULL DEFAULT ''",
			"record_version " + text + " NOT NULL DEFAULT ''",
			"phase " + text + " NOT NULL DEFAULT ''",
			"operation " + text + " NOT NULL",
			"instruction_key " + text + " NOT NULL DEFAULT ''",
			"status " + indexText + " NOT NULL",
			"actor_id " + text + " NOT NULL DEFAULT ''",
			"role_key " + text + " NOT NULL DEFAULT ''",
			"request_id " + text + " NOT NULL DEFAULT ''",
			"correlation_id " + text + " NOT NULL DEFAULT ''",
			"event_id " + text + " NOT NULL DEFAULT ''",
			"duration_ms INTEGER NOT NULL DEFAULT 0",
			"error_code " + text + " NOT NULL DEFAULT ''",
			"candidate_json TEXT NOT NULL DEFAULT '{}'",
			"trace_json TEXT NOT NULL DEFAULT '{}'",
			"result_json TEXT NOT NULL DEFAULT '{}'",
			"lease_owner " + text + " NOT NULL DEFAULT ''",
			"lease_expires_at " + timestampText + " NOT NULL DEFAULT ''",
			"fencing_token BIGINT NOT NULL DEFAULT 1",
			"created_at " + timestampText + " NOT NULL",
			"updated_at " + text + " NOT NULL",
		},
		"_publication_outbox": {
			"id " + text + " PRIMARY KEY",
			"publication_type " + idempotencyScopeText + " NOT NULL DEFAULT 'integration.connector'",
			"workspace_id " + idempotencyScopeText + " NOT NULL",
			"operation_id " + text + " NOT NULL DEFAULT ''",
			"application_key " + idempotencyScopeText + " NOT NULL DEFAULT ''",
			"source_event_id " + idempotencyScopeText + " NOT NULL DEFAULT ''",
			"event_type " + text + " NOT NULL DEFAULT ''",
			"intent_json TEXT NOT NULL DEFAULT '{}'",
			"connector_key " + idempotencyScopeText + " NOT NULL DEFAULT ''",
			"connection_key " + idempotencyScopeText,
			"operation " + idempotencyScopeText + " NOT NULL DEFAULT ''",
			"status " + indexText + " NOT NULL",
			"payload_json TEXT NOT NULL DEFAULT '{}'",
			"event_id " + text,
			"request_ref TEXT",
			"dedup_key " + idempotencyScopeText + " NOT NULL DEFAULT ''",
			"request_fingerprint " + text + " NOT NULL DEFAULT ''",
			"response_ref TEXT",
			"error TEXT",
			"attempt_count INTEGER NOT NULL DEFAULT 0",
			"next_attempt_at " + timestampText + " NOT NULL DEFAULT ''",
			"last_attempt_at " + text + " NOT NULL DEFAULT ''",
			"lease_owner " + text + " NOT NULL DEFAULT ''",
			"lease_expires_at " + timestampText + " NOT NULL DEFAULT ''",
			"fencing_token BIGINT NOT NULL DEFAULT 0",
			"remote_event_id " + text + " NOT NULL DEFAULT ''",
			"last_error_code " + text + " NOT NULL DEFAULT ''",
			"last_error TEXT NOT NULL DEFAULT ''",
			"terminal_at " + text + " NOT NULL DEFAULT ''",
			"created_by " + text + " NOT NULL DEFAULT ''",
			"created_at " + timestampText + " NOT NULL",
			"updated_at " + text + " NOT NULL",
		},
	}
	if !capabilities.Workflow {
		delete(tables, "_workflow_executions")
	}
	if !capabilities.Automation {
		delete(tables, "_automation_runs")
	}
	if !capabilities.ReleaseCoordination {
		delete(tables, "_release_cohorts")
		delete(tables, "_release_instances")
	}
	return ensureEvidenceTables(ctx, s, tables, text)
}
