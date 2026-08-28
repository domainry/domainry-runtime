package lifecycle

import (
	"encoding/json"
	"testing"
	"time"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	agentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/agent"
	metadatapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/metadata"
	ratelimitpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/ratelimit"
)

func TestOwnerExecutorArchivesBeforePurgeAndHonorsLegalHold(t *testing.T) {
	store := openLifecycleStore(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	insert := "INSERT INTO " + store.TableIdentifier("integration_webhook_nonces") + " (id, workspace_id, connector_key, nonce, request_timestamp, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)"
	for _, id := range []string{"nonce-held", "nonce-purge"} {
		if _, err := store.DB().ExecContext(t.Context(), insert, id, "workspace-a", "webhook", id, lifecycleTime(now.Add(-48*time.Hour)), lifecycleTime(now.Add(-48*time.Hour)), lifecycleTime(now.Add(-24*time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	executor := ownerExecutorForTest(t, DefaultOwnerExecutors(store), "integration")
	policy := lifecyclemodel.PolicyVersion{WorkspaceID: "workspace-a", Policy: lifecyclemodel.RetentionPolicy{Key: "integration.webhook_nonce.v1", Version: "1", Owner: "integration", DefaultRetention: time.Hour}}
	preview, err := executor.Preview(t.Context(), "workspace-a", policy, now)
	if err != nil || preview.Rows != 2 {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	job := lifecyclemodel.CleanupJob{ID: "job-1", WorkspaceID: "workspace-a", PolicyKey: policy.Policy.Key, PolicyVersion: "1", Operation: lifecyclemodel.OperationPurge, Status: lifecyclemodel.CleanupStatusRunning, UpdatedAt: now}
	hold := lifecyclemodel.LegalHold{ID: "hold-1", WorkspaceID: "workspace-a", Owner: "integration", ResourceType: "integration_webhook_nonces", ResourceID: "nonce-held", StartsAt: now.Add(-time.Hour), ReviewAt: now.Add(time.Hour)}
	result, err := executor.ProcessBatch(t.Context(), job, policy, []lifecyclemodel.LegalHold{hold}, 10)
	if err != nil || result.Archived != 1 || result.Purged != 1 || result.Skipped != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	for id, want := range map[string]int{"nonce-held": 1, "nonce-purge": 0} {
		var count int
		if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM integration_webhook_nonces WHERE id = ?", id).Scan(&count); err != nil || count != want {
			t.Fatalf("id=%s count=%d want=%d err=%v", id, count, want, err)
		}
	}
	var archives int
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM lifecycle_archive_entries WHERE resource_id = ?", "nonce-purge").Scan(&archives); err != nil || archives != 1 {
		t.Fatalf("archives=%d err=%v", archives, err)
	}
	entries, err := NewLifecycleStore(store).ListArchiveEntries(t.Context(), "workspace-a", "integration_webhook_nonces", 10)
	if err != nil || len(entries) != 1 || entries[0].PayloadHash == "" || len(entries[0].Payload) == 0 {
		t.Fatalf("entries=%#v err=%v", entries, err)
	}
	other, err := NewLifecycleStore(store).ListArchiveEntries(t.Context(), "workspace-b", "", 10)
	if err != nil || len(other) != 0 {
		t.Fatalf("archive crossed workspace: entries=%#v err=%v", other, err)
	}
}

func TestAgentOwnerCleanupOnlyPurgesArchivedSessionsAndDecidedProposals(t *testing.T) {
	store := openLifecycleStore(t)
	repository := agentpersistence.NewAgentStateStore(store)
	if err := repository.EnsureSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour).UnixNano()
	for _, item := range []struct {
		kind, key string
		payload   map[string]any
	}{{"session", "archived", map[string]any{"archived": true}}, {"session", "active", map[string]any{"archived": false}}, {"proposal", "decided", map[string]any{"status": "approved"}}, {"proposal", "draft", map[string]any{"status": "draft"}}} {
		raw, _ := json.Marshal(item.payload)
		if err := repository.Put(t.Context(), "workspace-a", agentmodel.AgentStateRecord{Kind: item.kind, Key: item.key, WorkspaceID: "workspace-a", UserID: "user-1", RoleKey: "admin", Payload: raw, UpdatedAt: old}); err != nil {
			t.Fatal(err)
		}
	}
	executor := ownerExecutorForTest(t, DefaultOwnerExecutors(store), "agent")
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "agent.dialog.v1", Version: "1", Owner: "agent", DefaultRetention: time.Hour}}
	result, err := executor.ProcessBatch(t.Context(), lifecyclemodel.CleanupJob{ID: "agent-cleanup", WorkspaceID: "workspace-a", Operation: lifecyclemodel.OperationPurge, UpdatedAt: now}, policy, nil, 20)
	if err != nil || result.Archived != 2 || result.Purged != 2 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	states, err := repository.List(t.Context(), "workspace-a", "session", "", "")
	if err != nil || len(states) != 1 || states[0].Key != "active" {
		t.Fatalf("sessions=%#v err=%v", states, err)
	}
	proposals, err := repository.List(t.Context(), "workspace-a", "proposal", "", "")
	if err != nil || len(proposals) != 1 || proposals[0].Key != "draft" {
		t.Fatalf("proposals=%#v err=%v", proposals, err)
	}
}

func TestOwnerExecutorDryRunHasNoArchiveOrDeleteSideEffects(t *testing.T) {
	store := openLifecycleStore(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO integration_webhook_nonces (id, workspace_id, connector_key, nonce, request_timestamp, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)", "nonce-1", "workspace-a", "webhook", "nonce", lifecycleTime(now.Add(-48*time.Hour)), lifecycleTime(now.Add(-48*time.Hour)), lifecycleTime(now.Add(-24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	executor := ownerExecutorForTest(t, DefaultOwnerExecutors(store), "integration")
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "integration.webhook_nonce.v1", Version: "1", Owner: "integration", DefaultRetention: time.Hour}}
	result, err := executor.ProcessBatch(t.Context(), lifecyclemodel.CleanupJob{ID: "dry-run", WorkspaceID: "workspace-a", Operation: lifecyclemodel.OperationPurge, DryRun: true, UpdatedAt: now}, policy, nil, 10)
	if err != nil || result.Scanned != 1 || result.Archived != 0 || result.Purged != 0 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestOwnerExecutorResumesPartialBatchesFromDurableArchiveEvidence(t *testing.T) {
	store := openLifecycleStore(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"nonce-1", "nonce-2", "nonce-3"} {
		if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO integration_webhook_nonces (id, workspace_id, connector_key, nonce, request_timestamp, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)", id, "workspace-a", "webhook", id, lifecycleTime(now.Add(-48*time.Hour)), lifecycleTime(now.Add(-48*time.Hour)), lifecycleTime(now.Add(-24*time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	executor := ownerExecutorForTest(t, DefaultOwnerExecutors(store), "integration")
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "integration.webhook_nonce.v1", Version: "1", Owner: "integration", DefaultRetention: time.Hour}}
	job := lifecyclemodel.CleanupJob{ID: "partial", WorkspaceID: "workspace-a", Operation: lifecyclemodel.OperationPurge, UpdatedAt: now}
	for index := 0; index < 3; index++ {
		result, err := executor.ProcessBatch(t.Context(), job, policy, nil, 1)
		if err != nil || result.Purged != 1 || result.Checkpoint == "" {
			t.Fatalf("batch=%d result=%#v err=%v", index, result, err)
		}
		job.Checkpoint = result.Checkpoint
	}
	var remaining int
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM integration_webhook_nonces WHERE workspace_id = ?", "workspace-a").Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("remaining=%d err=%v", remaining, err)
	}
}

func TestRecordBatchCleanupArchivesAndPurgesChunksBeforeJob(t *testing.T) {
	store := openLifecycleStore(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	old := lifecycleTime(now.Add(-48 * time.Hour))
	if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO record_batch_jobs (id, workspace_id, kind, object_key, status, idempotency_key, request_fingerprint, payload_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", "batch-1", "workspace-a", "export", "customer", "succeeded", "idem-1", "fingerprint", "{}", old, old); err != nil {
		t.Fatal(err)
	}
	for sequence := 1; sequence <= 2; sequence++ {
		if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO record_batch_job_chunks (workspace_id, job_id, sequence_no, content, created_at) VALUES (?, ?, ?, ?, ?)", "workspace-a", "batch-1", sequence, "row", old); err != nil {
			t.Fatal(err)
		}
	}
	executor := ownerExecutorForTest(t, DefaultOwnerExecutors(store), "record")
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "record.batch_artifact.v1", Version: "1", Owner: "record", DefaultRetention: time.Hour}}
	result, err := executor.ProcessBatch(t.Context(), lifecyclemodel.CleanupJob{ID: "cleanup-batch", WorkspaceID: "workspace-a", Operation: lifecyclemodel.OperationPurge, UpdatedAt: now}, policy, nil, 10)
	if err != nil || result.Purged != 3 || result.Archived != 3 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	for _, table := range []string{"record_batch_jobs", "record_batch_job_chunks"} {
		var count int
		if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table+" WHERE workspace_id = ?", "workspace-a").Scan(&count); err != nil || count != 0 {
			t.Fatalf("table=%s count=%d err=%v", table, count, err)
		}
	}
}

func TestSoftDeletedBusinessRecordCleanupBlocksWorkflowReference(t *testing.T) {
	store := openLifecycleStore(t)
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}, {Key: "deleted_at", Type: "datetime"}, {Key: "deleted_by", Type: "text"}, {Key: "name", Type: "text"}}}
	manifest := manifestmodel.ManifestSchema{TemplateID: "record-lifecycle", Version: "1", Name: "Record lifecycle", Objects: []definitionmodel.ObjectSchema{object}}
	if err := metadatapersistence.NewMetadataStore(store).SyncManifest(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "record lifecycle test"), manifest); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	old := lifecycleTime(now.Add(-48 * time.Hour))
	for _, id := range []string{"customer-purge", "customer-referenced"} {
		if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO customer (workspace_id, id, created_at, updated_at, status, deleted_at, deleted_by, name) VALUES ('workspace-a', ?, ?, ?, 'deleted', ?, 'admin', 'Acme')", id, old, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO workflow_process_instances (workspace_id, id, workflow_key, workflow_name, workflow_definition_version_id, definition_version, definition_hash, definition_json, object_key, record_id, initiator_id, status, current_node_ids_json, variables_json, result_json, created_at, updated_at) VALUES ('workspace-a', 'process-record', 'approval', 'Approval', '', 1, 'hash', '{}', 'customer', 'customer-referenced', 'user-1', 'completed', '[]', '{}', '{}', ?, ?)", old, old); err != nil {
		t.Fatal(err)
	}
	executor := ownerExecutorForTest(t, DefaultOwnerExecutors(store, object), "record")
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "record.object.default.v1", Version: "1", Owner: "record", DefaultRetention: time.Hour}}
	result, err := executor.ProcessBatch(t.Context(), lifecyclemodel.CleanupJob{ID: "record-object-cleanup", WorkspaceID: "workspace-a", Operation: lifecyclemodel.OperationPurge, UpdatedAt: now}, policy, nil, 10)
	if err != nil || result.Archived != 1 || result.Purged != 1 || result.Skipped != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	var referenced int
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM customer WHERE workspace_id = 'workspace-a' AND id = 'customer-referenced'").Scan(&referenced); err != nil || referenced != 1 {
		t.Fatalf("referenced=%d err=%v", referenced, err)
	}
}

func TestOwnerExecutorAppliesTerminalStatusRetentionSeparately(t *testing.T) {
	store := openLifecycleStore(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	old := lifecycleTime(now.Add(-180 * 24 * time.Hour))
	for _, item := range []struct{ id, status string }{{"event-success", "processed"}, {"event-failed", "failed"}} {
		if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO integration_events (id, workspace_id, provider, event_type, external_id, status, payload_json, received_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)", item.id, "workspace-a", "provider", "changed", item.id, item.status, "{}", old, old); err != nil {
			t.Fatal(err)
		}
	}
	executor := ownerExecutorForTest(t, DefaultOwnerExecutors(store), "integration")
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "integration.event.v1", Version: "1", Owner: "integration", DefaultRetention: 90 * 24 * time.Hour, StatusRetention: map[string]time.Duration{"succeeded": 90 * 24 * time.Hour, "failed": 365 * 24 * time.Hour}}}
	preview, err := executor.Preview(t.Context(), "workspace-a", policy, now)
	if err != nil || preview.Rows != 1 {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
}

func TestWorkflowProcessCleanupArchivesChildrenBeforeParent(t *testing.T) {
	store := openLifecycleStore(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	old := lifecycleTime(now.Add(-48 * time.Hour))
	if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO workflow_process_instances (workspace_id, id, workflow_key, workflow_name, workflow_definition_version_id, definition_version, definition_hash, definition_json, initiator_id, status, current_node_ids_json, variables_json, result_json, created_at, updated_at, completed_at) VALUES ('workspace-a', 'process-purge', 'approval', 'Approval', '', 1, 'hash', '{}', 'user-1', 'completed', '[]', '{}', '{}', ?, ?, ?)", old, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO workflow_node_instances (workspace_id, id, process_id, node_id, node_type, iteration, status, input_json, output_json, started_at, completed_at) VALUES ('workspace-a', 'node-1', 'process-purge', 'approve', 'approval', 1, 'completed', '{}', '{}', ?, ?)", old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO workflow_tasks (workspace_id, id, process_id, node_instance_id, node_id, title, sequence_no, status, created_at, updated_at) VALUES ('workspace-a', 'task-1', 'process-purge', 'node-1', 'approve', 'Approve', 1, 'completed', ?, ?)", old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO workflow_process_events (workspace_id, id, process_id, event, actor_id, summary, metadata_json, created_at) VALUES ('workspace-a', 'event-1', 'process-purge', 'completed', 'user-1', 'done', '{}', ?)", old); err != nil {
		t.Fatal(err)
	}
	executor := ownerExecutorForTest(t, DefaultOwnerExecutors(store), "workflow")
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "workflow.execution.v1", Version: "1", Owner: "workflow", DefaultRetention: time.Hour, StatusRetention: map[string]time.Duration{"succeeded": time.Hour, "failed": 2 * time.Hour}}}
	result, err := executor.ProcessBatch(t.Context(), lifecyclemodel.CleanupJob{ID: "workflow-process-cleanup", WorkspaceID: "workspace-a", Operation: lifecyclemodel.OperationPurge, UpdatedAt: now}, policy, nil, 10)
	if err != nil || result.Archived != 4 || result.Purged != 4 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	for _, table := range []string{"workflow_process_events", "workflow_tasks", "workflow_node_instances", "workflow_process_instances"} {
		var count int
		if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table+" WHERE workspace_id = 'workspace-a'").Scan(&count); err != nil || count != 0 {
			t.Fatalf("table=%s count=%d err=%v", table, count, err)
		}
	}
}

func TestTechnicalLeaseCleanupOnlySelectsLongExpiredRows(t *testing.T) {
	store := openLifecycleStore(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		id, connection string
		expires        time.Time
	}{{"lease-old", "old", now.Add(-8 * 24 * time.Hour)}, {"lease-active", "active", now.Add(time.Hour)}} {
		if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO integration_credential_refresh_leases (id, workspace_id, connection_key, lease_owner, lease_expires_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)", item.id, "workspace-a", item.connection, "worker", lifecycleTime(item.expires), lifecycleTime(now)); err != nil {
			t.Fatal(err)
		}
	}
	executor := ownerExecutorForTest(t, DefaultOwnerExecutors(store), "integration")
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "technical.lease_checkpoint.v1", Version: "1", Owner: "integration", DefaultRetention: 7 * 24 * time.Hour}}
	preview, err := executor.Preview(t.Context(), "workspace-a", policy, now)
	if err != nil || preview.Rows != 1 {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
}

func TestWorkflowDefinitionCleanupPreservesCurrentAndProcessReferencedVersions(t *testing.T) {
	store := openLifecycleStore(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	old := lifecycleTime(now.Add(-3 * time.Hour))
	for index, id := range []string{"version-purge", "version-current", "version-process"} {
		if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO workflow_definition_versions (id, definition_id, version_no, status, revision, workflow_json, validation_report_json, created_by, created_at, updated_at) VALUES (?, ?, ?, 'archived', 1, '{}', '{}', 'admin', ?, ?)", id, "definition-1", index+1, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO workflow_definition_identities (id, workflow_key, name, enabled, current_published_version_id, created_at, updated_at) VALUES ('definition-1', 'approval', 'Approval', 1, 'version-current', ?, ?)", old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO workflow_process_instances (workspace_id, id, workflow_key, workflow_name, workflow_definition_version_id, definition_version, definition_hash, definition_json, initiator_id, status, current_node_ids_json, variables_json, result_json, created_at, updated_at) VALUES ('workspace-a', 'process-1', 'approval', 'Approval', 'version-process', 3, 'hash', '{}', 'user-1', 'completed', '[]', '{}', '{}', ?, ?)", old, old); err != nil {
		t.Fatal(err)
	}
	executor := ownerExecutorForTest(t, DefaultOwnerExecutors(store), "workflow")
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "workflow.definition.v1", Version: "1", Owner: "workflow", DefaultRetention: time.Hour}}
	result, err := executor.ProcessBatch(t.Context(), lifecyclemodel.CleanupJob{ID: "workflow-definition-cleanup", WorkspaceID: principalmodel.InstallationWorkspaceID, Operation: lifecyclemodel.OperationPurge, UpdatedAt: now}, policy, nil, 10)
	if err != nil || result.Archived != 1 || result.Purged != 1 || result.Skipped != 2 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestBreakGlassCleanupUsesExpiryAndPreservesWorkspaceBoundary(t *testing.T) {
	store := openLifecycleStore(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	insert := "INSERT INTO runtime_break_glass_grants (id, workspace_id, state, actor_id, approver_ids_json, reason, incident_ref, alert_target, audit_event_id, expires_at, revision, created_at, updated_at) VALUES (?, ?, 'active', 'operator', '[\"approver-a\",\"approver-b\"]', 'incident', 'INC-1', 'security', ?, ?, 1, ?, ?)"
	old := lifecycleTime(now.Add(-48 * time.Hour))
	for _, item := range []struct{ id, workspace string }{{"grant-a", "workspace-a"}, {"grant-b", "workspace-b"}} {
		if _, err := store.DB().ExecContext(t.Context(), insert, item.id, item.workspace, "audit-"+item.id, old, old, old); err != nil {
			t.Fatal(err)
		}
	}
	executor := ownerExecutorForTest(t, DefaultOwnerExecutors(store), "operations")
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "operations.break_glass.v1", Version: "1", Owner: "operations", DefaultRetention: time.Hour}}
	result, err := executor.ProcessBatch(t.Context(), lifecyclemodel.CleanupJob{ID: "break-glass-cleanup", WorkspaceID: "workspace-a", Operation: lifecyclemodel.OperationPurge, UpdatedAt: now}, policy, nil, 10)
	if err != nil || result.Archived != 1 || result.Purged != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	var other int
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM runtime_break_glass_grants WHERE workspace_id = 'workspace-b'").Scan(&other); err != nil || other != 1 {
		t.Fatalf("other=%d err=%v", other, err)
	}
}

func TestRateLimitBucketCleanupIsInstallationScopedAndUsesNanosecondTTL(t *testing.T) {
	store := openLifecycleStore(t)
	if err := ratelimitpersistence.NewRateLimiter(store).EnsureSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	for key, updatedAt := range map[string]time.Time{"expired": now.Add(-48 * time.Hour), "active": now.Add(-30 * time.Minute)} {
		if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO runtime_rate_limit_bucket (bucket_key, window_start_ns, request_count, updated_at_ns) VALUES (?, ?, ?, ?)", key, updatedAt.UnixNano(), 1, updatedAt.UnixNano()); err != nil {
			t.Fatal(err)
		}
	}
	executor := ownerExecutorForTest(t, DefaultOwnerExecutors(store), "runtime_security")
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "ratelimit.bucket.v1", Version: "1", Owner: "runtime_security", DefaultRetention: time.Hour}}
	if _, err := executor.Preview(t.Context(), "workspace-a", policy, now); err == nil {
		t.Fatal("workspace cleanup unexpectedly claimed installation-scoped rate buckets")
	}
	preview, err := executor.Preview(t.Context(), principalmodel.InstallationWorkspaceID, policy, now)
	if err != nil || preview.Rows != 1 || !preview.OldestEligible.Equal(now.Add(-48*time.Hour)) {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	result, err := executor.ProcessBatch(t.Context(), lifecyclemodel.CleanupJob{ID: "rate-cleanup", WorkspaceID: principalmodel.InstallationWorkspaceID, Operation: lifecyclemodel.OperationPurge, UpdatedAt: now}, policy, nil, 10)
	if err != nil || result.Archived != 1 || result.Purged != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	var remaining int
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM runtime_rate_limit_bucket").Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("remaining=%d err=%v", remaining, err)
	}
}
