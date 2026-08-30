package lifecycle

import (
	"testing"
	"time"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	lifecyclepolicy "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/policy"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	agentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/agent"
	appschemapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
)

func TestDefaultOwnerExecutorPoliciesHaveContractCoverage(t *testing.T) {
	store := openLifecycleStore(t)
	want := map[string]map[string]bool{
		"runtime_security": {"ratelimit.bucket.v1": true},
		"action":           {"execution.idempotency_receipt.v1": true},
		"record":           {"execution.idempotency_receipt.v1": true},
		"operations":       {"operations.receipt.v1": true, "operations.break_glass.v1": true},
		"integration":      {"integration.webhook_nonce.v1": true, "integration.event.v1": true, "integration.delivery_evidence.v1": true, "technical.lease_checkpoint.v1": true},
		"workflow":         {"workflow.definition.v1": true, "workflow.receipt.v1": true, "workflow.execution.v1": true},
		"automation":       {"automation.execution.v1": true},
		"metadata":         {"metadata.definition_history.v1": true},
		"audit":            {"audit.evidence.v1": true},
		"agent":            {"agent.dialog.v1": true},
		"report":           {"report.download.v1": true, "report.export.v1": true},
	}
	for _, executor := range DefaultOwnerExecutors(store) {
		owner := executor.Owner(t.Context())
		policies, ok := want[owner]
		if !ok {
			t.Fatalf("unreviewed owner executor %q", owner)
		}
		for policyKey := range policies {
			switch typed := executor.(type) {
			case OwnerExecutor:
				if len(typed.policySpecs(policyKey)) == 0 {
					t.Errorf("owner %s has no cleanup spec for %s", owner, policyKey)
				}
			case AgentOwnerExecutor:
				if policyKey != "agent.dialog.v1" {
					t.Errorf("agent executor does not cover %s", policyKey)
				}
			case ReportOwnerExecutor:
				if policyKey != "report.download.v1" && policyKey != "report.export.v1" {
					t.Errorf("report executor does not cover %s", policyKey)
				}
			default:
				t.Errorf("owner %s has unreviewed executor type %T", owner, executor)
			}
		}
		delete(want, owner)
	}
	if len(want) != 0 {
		t.Fatalf("owners without executor coverage: %#v", want)
	}
}

func TestEveryDefaultRetentionPolicyHasAnExplicitEnforcementMode(t *testing.T) {
	store := openLifecycleStore(t)
	covered := map[string]string{}
	for _, executor := range DefaultOwnerExecutors(store) {
		switch typed := executor.(type) {
		case OwnerExecutor:
			for _, spec := range typed.specs {
				covered[spec.policyKey] = "owner_cleanup"
			}
		case AgentOwnerExecutor:
			covered["agent.dialog.v1"] = "owner_cleanup"
		case ReportOwnerExecutor:
			covered["report.download.v1"] = "owner_cleanup_and_file_expiry"
			covered["report.export.v1"] = "owner_cleanup_and_file_expiry"
		}
	}
	for key, mode := range map[string]string{
		"notification.history.v1":             "notification_system_retention",
		"notification.publication_history.v1": "notification_system_retention",
		"scheduler.execution.v1":              "manifest_owner_cleanup",
		"record.object.default.v1":            "manifest_owner_cleanup_and_subject_policy",
		"integration.configuration.v1":        "bounded_active_configuration",
		"integration.secret.v1":               "subject_and_rotation_policy",
		"integration.identity_mapping.v1":     "subject_and_provider_reconciliation",
		"localization.text.v1":                "source_driven_projection",
		"runtime.configuration.v1":            "bounded_current_revision",
		"operations.control.v1":               "bounded_current_control",
		"persistence.migration_evidence.v1":   "installation_lifetime_evidence",
		"file.upload.v1":                      "file_registry_reconciliation",
		"cache.dictionary.v1":                 "bounded_reconstructable_cache",
		"cache.runtime_projection.v1":         "bounded_reconstructable_cache",
	} {
		covered[key] = mode
	}
	for _, version := range lifecyclepolicy.DefaultPolicyCatalog("workspace-a", "test", time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)) {
		if mode := covered[version.Policy.Key]; mode == "" {
			t.Errorf("default policy %s owned by %s has no reviewed enforcement mode", version.Policy.Key, version.Policy.Owner)
		}
		delete(covered, version.Policy.Key)
	}
	if len(covered) != 0 {
		t.Fatalf("enforcement catalog contains unknown policies: %#v", covered)
	}
}

func TestInstallationOwnerCleanupPreservesCurrentMetadataVersion(t *testing.T) {
	store := openLifecycleStore(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	old := lifecycleTime(now.Add(-48 * time.Hour))
	for _, item := range []struct{ id, version, created string }{{"metadata-old", "1", old}, {"metadata-current", "2", lifecycleTime(now)}} {
		if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO metadata_definition_versions (id, resource_type, resource_key, schema_version, schema_hash, payload_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)", item.id, "object", "customer", item.version, "hash-"+item.version, "{}", item.created); err != nil {
			t.Fatal(err)
		}
	}
	metadataExecutor := ownerExecutorForTest(t, DefaultOwnerExecutors(store), "metadata")
	metadataPolicy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "metadata.definition_history.v1", Version: "1", Owner: "metadata", DefaultRetention: time.Hour}}
	if _, err := metadataExecutor.Preview(t.Context(), "workspace-a", metadataPolicy, now); err == nil {
		t.Fatal("workspace cleanup accepted installation metadata policy")
	}
	metadataResult, err := metadataExecutor.ProcessBatch(t.Context(), lifecyclemodel.CleanupJob{ID: "metadata-cleanup", WorkspaceID: principalmodel.InstallationWorkspaceID, Operation: lifecyclemodel.OperationPurge, UpdatedAt: now}, metadataPolicy, nil, 20)
	if err != nil || metadataResult.Archived != 1 || metadataResult.Purged != 1 {
		t.Fatalf("metadata result=%#v err=%v", metadataResult, err)
	}
	var metadataRemaining int
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM metadata_definition_versions WHERE resource_key = ?", "customer").Scan(&metadataRemaining); err != nil || metadataRemaining != 1 {
		t.Fatalf("metadata remaining=%d err=%v", metadataRemaining, err)
	}

}

func TestReportOwnerCleanupExpiresThenPurgesInReferenceOrder(t *testing.T) {
	store := openLifecycleStore(t)
	if err := agentpersistence.NewAgentSchemaMigration(store).EnsureSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	objects := []definitionmodel.ObjectSchema{
		{Key: "report_query_run", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}},
		{Key: "report_export_audit", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}, {Key: "report_query_run", Type: "text"}}},
		{Key: "download_task", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}, {Key: "report_export_audit", Type: "text"}}},
	}
	manifest := manifestmodel.ManifestSchema{TemplateID: "report-lifecycle", Version: "1", Name: "Report lifecycle", Objects: objects}
	if err := appschemapersistence.NewApplicationSchemaStore(store).SyncManifest(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "report lifecycle test"), manifest); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	old := lifecycleTime(now.Add(-48 * time.Hour))
	for _, insert := range []string{
		"INSERT INTO report_query_run (workspace_id, id, created_at, updated_at, status) VALUES ('workspace-a', 'query-1', '" + old + "', '" + old + "', 'completed')",
		"INSERT INTO report_export_audit (workspace_id, id, created_at, updated_at, status, report_query_run) VALUES ('workspace-a', 'audit-1', '" + old + "', '" + old + "', 'completed', 'query-1')",
		"INSERT INTO download_task (workspace_id, id, created_at, updated_at, status, report_export_audit) VALUES ('workspace-a', 'download-1', '" + old + "', '" + old + "', 'expired', 'audit-1')",
	} {
		if _, err := store.DB().ExecContext(t.Context(), insert); err != nil {
			t.Fatal(err)
		}
	}
	executor := ownerExecutorForTest(t, DefaultOwnerExecutors(store, objects...), "report")
	downloadPolicy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "report.download.v1", Version: "1", Owner: "report", DefaultRetention: time.Hour}}
	download, err := executor.ProcessBatch(t.Context(), lifecyclemodel.CleanupJob{ID: "report-download-cleanup", WorkspaceID: "workspace-a", Operation: lifecyclemodel.OperationPurge, UpdatedAt: now}, downloadPolicy, nil, 20)
	if err != nil || download.Archived != 1 || download.Purged != 1 {
		t.Fatalf("download=%#v err=%v", download, err)
	}
	exportPolicy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "report.export.v1", Version: "1", Owner: "report", DefaultRetention: time.Hour, StatusRetention: map[string]time.Duration{"succeeded": time.Hour}}}
	export, err := executor.ProcessBatch(t.Context(), lifecyclemodel.CleanupJob{ID: "report-export-cleanup", WorkspaceID: "workspace-a", Operation: lifecyclemodel.OperationPurge, UpdatedAt: now}, exportPolicy, nil, 20)
	if err != nil || export.Archived != 2 || export.Purged != 2 {
		t.Fatalf("export=%#v err=%v", export, err)
	}
}

func TestReportOwnerCleanupPurgesAgentReportStateInReferenceOrder(t *testing.T) {
	store := openLifecycleStore(t)
	repository := agentpersistence.NewAgentStateStore(store)
	if err := agentpersistence.NewAgentSchemaMigration(store).EnsureSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour).UnixNano()
	for _, kind := range []string{"report_query_run", "report_export_audit", "report_download_task"} {
		if err := repository.Put(t.Context(), "workspace-a", agentmodel.AgentStateRecord{Kind: kind, Key: "report-1", WorkspaceID: "workspace-a", UserID: "user-1", RoleKey: "admin", Payload: []byte(`{"status":"completed"}`), UpdatedAt: old}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.Put(t.Context(), "workspace-a", agentmodel.AgentStateRecord{Kind: "report_download_task", Key: "report-new", WorkspaceID: "workspace-a", UserID: "user-1", RoleKey: "admin", Payload: []byte(`{"status":"ready"}`), UpdatedAt: now.Add(-30 * time.Minute).UnixNano()}); err != nil {
		t.Fatal(err)
	}
	executor := ownerExecutorForTest(t, DefaultOwnerExecutors(store), "report")
	downloadPolicy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "report.download.v1", Version: "1", Owner: "report", DefaultRetention: time.Hour}}
	download, err := executor.ProcessBatch(t.Context(), lifecyclemodel.CleanupJob{ID: "report-agent-download-cleanup", WorkspaceID: "workspace-a", Operation: lifecyclemodel.OperationPurge, UpdatedAt: now}, downloadPolicy, nil, 20)
	if err != nil || download.Archived != 1 || download.Purged != 1 {
		t.Fatalf("download=%#v err=%v", download, err)
	}
	exportPolicy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "report.export.v1", Version: "1", Owner: "report", DefaultRetention: time.Hour, StatusRetention: map[string]time.Duration{"succeeded": time.Hour}}}
	result, err := executor.ProcessBatch(t.Context(), lifecyclemodel.CleanupJob{ID: "report-agent-export-cleanup", WorkspaceID: "workspace-a", Operation: lifecyclemodel.OperationPurge, UpdatedAt: now}, exportPolicy, nil, 20)
	if err != nil || result.Archived != 2 || result.Purged != 2 || result.Checkpoint != "agent_runtime_state:report_query_run:report-1" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	for key, want := range map[string]int{"report-1": 0, "report-new": 1} {
		var count int
		if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM agent_runtime_state WHERE workspace_id = ? AND state_key = ?", "workspace-a", key).Scan(&count); err != nil || count != want {
			t.Fatalf("key=%s count=%d want=%d err=%v", key, count, want, err)
		}
	}
}

func TestIntegrationEventPurgeWaitsForOutboxThenDeletesMappingBeforeEvent(t *testing.T) {
	store := openLifecycleStore(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	old := lifecycleTime(now.Add(-180 * 24 * time.Hour))
	if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO integration_events (id, workspace_id, provider, event_type, external_id, status, payload_json, received_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)", "event-1", "workspace-a", "provider", "changed", "external-1", "processed", "{}", old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO integration_event_mapping_intents (id, workspace_id, event_id, target_type, status, payload_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)", "intent-1", "workspace-a", "event-1", "record", "pending", "{}", old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO integration_outbox_messages (id, workspace_id, connector_key, operation, status, payload_json, event_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)", "outbox-1", "workspace-a", "provider", "send", "pending", "{}", "event-1", old, old); err != nil {
		t.Fatal(err)
	}
	executor := ownerExecutorForTest(t, DefaultOwnerExecutors(store), "integration")
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "integration.event.v1", Version: "1", Owner: "integration", DefaultRetention: 90 * 24 * time.Hour, StatusRetention: map[string]time.Duration{"succeeded": 90 * 24 * time.Hour}}}
	job := lifecyclemodel.CleanupJob{ID: "cleanup-event", WorkspaceID: "workspace-a", Operation: lifecyclemodel.OperationPurge, UpdatedAt: now}
	blocked, err := executor.ProcessBatch(t.Context(), job, policy, nil, 10)
	if err != nil || blocked.Skipped != 1 || blocked.Purged != 0 {
		t.Fatalf("blocked=%#v err=%v", blocked, err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "DELETE FROM integration_outbox_messages WHERE workspace_id = ? AND id = ?", "workspace-a", "outbox-1"); err != nil {
		t.Fatal(err)
	}
	completed, err := executor.ProcessBatch(t.Context(), job, policy, nil, 10)
	if err != nil || completed.Archived != 2 || completed.Purged != 2 {
		t.Fatalf("completed=%#v err=%v", completed, err)
	}
	for _, table := range []string{"integration_event_mapping_intents", "integration_events"} {
		var count int
		if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table+" WHERE workspace_id = ?", "workspace-a").Scan(&count); err != nil || count != 0 {
			t.Fatalf("table=%s count=%d err=%v", table, count, err)
		}
	}
}

func ownerExecutorForTest(t *testing.T, executors []lifecyclecontract.OwnerLifecycleExecutor, owner string) lifecyclecontract.OwnerLifecycleExecutor {
	t.Helper()
	for _, executor := range executors {
		if executor.Owner(t.Context()) == owner {
			return executor
		}
	}
	t.Fatalf("owner executor not found: %s", owner)
	return nil
}
