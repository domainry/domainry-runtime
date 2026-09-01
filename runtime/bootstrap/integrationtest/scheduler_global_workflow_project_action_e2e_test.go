package integrationtest

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestSchedulerGlobalWorkflowProjectActionDurableEffectAndFailurePropagation(t *testing.T) {
	cfg := config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "scheduler-global-workflow.db"),
		ManifestPath: schedulerGlobalWorkflowManifest(t), UploadDir: filepath.Join(t.TempDir(), "uploads"),
	}
	runtime := newIntegrationRuntime(t, cfg)
	defer runtime.CloseContext(t.Context())
	handler := runtime.Routes()

	activationLeadID := runtimeFixtureRecordIDByField(t, handler, "sales_manager", "lead", "status", "new")
	dailyReviewLeadID := runtimeFixtureRecordIDByField(t, handler, "sales_manager", "lead", "status", "working")
	succeeded := schedulerSmokeRequest(t, handler, http.MethodPost, "/operations/scheduler/definitions/business_config_activation_job/run", nil, http.StatusOK)
	if succeeded["status"] != "succeeded" {
		store := openRuntimePersistenceFixture(t, cfg)
		executions, listErr := workflowpersistence.NewWorkflowWorkerStore(store).ListExecutions(t.Context(), "workspace-primary", 100)
		t.Fatalf("success workflow executions=%#v listErr=%v response=%#v", executions, listErr, succeeded)
	}
	assertSchedulerRunStatus(t, succeeded, "succeeded")
	persisted := runtimeFixtureRequest[recordmodel.Record](t, handler, "sales_manager", http.MethodGet, "/objects/lead/records/"+activationLeadID, nil)
	if persisted.Data["status"] != "qualified" {
		t.Fatalf("scheduler succeeded without durable project Action effect: lead=%#v response=%#v", persisted, succeeded)
	}
	dailyReview := schedulerSmokeRequest(t, handler, http.MethodPost, "/operations/scheduler/definitions/daily_operations_review_job/run", nil, http.StatusOK)
	assertSchedulerRunStatus(t, dailyReview, "succeeded")
	dailyPersisted := runtimeFixtureRequest[recordmodel.Record](t, handler, "sales_manager", http.MethodGet, "/objects/lead/records/"+dailyReviewLeadID, nil)
	if dailyPersisted.Data["status"] != "qualified" {
		t.Fatalf("daily_operations_review_job succeeded without durable project Action effect: lead=%#v response=%#v", dailyPersisted, dailyReview)
	}

	failed := schedulerSmokeRequest(t, handler, http.MethodPost, "/operations/scheduler/definitions/lead_activation_failure_job/run", nil, http.StatusOK)
	assertSchedulerRunStatus(t, failed, "succeeded")
	store := openRuntimePersistenceFixture(t, cfg)
	executions, err := workflowpersistence.NewWorkflowWorkerStore(store).ListExecutions(t.Context(), "workspace-primary", 100)
	if err != nil {
		t.Fatal(err)
	}
	foundActionFailure := false
	for _, execution := range executions {
		if execution.WorkflowKey == "lead_activation_failure" && execution.Status == "failed" && execution.LastError == "lead.activation_failed" {
			foundActionFailure = true
			break
		}
	}
	if !foundActionFailure {
		t.Fatalf("accepted Scheduler dispatch lost downstream project Action failure evidence: response=%#v executions=%#v", failed, executions)
	}
}

func assertSchedulerRunStatus(t *testing.T, response map[string]any, want string) {
	t.Helper()
	if response["status"] != want {
		t.Fatalf("scheduler run status=%v want=%s response=%#v", response["status"], want, response)
	}
}

func schedulerGlobalWorkflowManifest(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	actions, _ := manifest["actions"].([]any)
	manifest["actions"] = append(actions,
		map[string]any{
			"key": "lead.activate_due_candidates", "label": "Activate due candidates", "object_key": "lead", "kind": "object_operation",
			"audit_event": "lead.due_candidates_activated", "payload_fields": []any{},
		},
		map[string]any{
			"key": "lead.create_daily_review_tasks", "label": "Create daily review tasks", "object_key": "lead", "kind": "object_operation",
			"audit_event": "lead.daily_review_tasks_created", "payload_fields": []any{},
		},
		map[string]any{
			"key": "lead.fail_due_candidates", "label": "Fail due candidates", "object_key": "lead", "kind": "object_operation",
			"audit_event": "lead.due_candidates_failed", "payload_fields": []any{},
		},
	)
	roles, _ := manifest["roles"].([]any)
	for _, rawRole := range roles {
		role, _ := rawRole.(map[string]any)
		if role["key"] != "platform_admin" {
			continue
		}
		permissions, _ := role["permissions"].([]any)
		role["permissions"] = append(permissions, "lead.activate_due_candidates", "lead.create_daily_review_tasks", "lead.fail_due_candidates", "lead.read", "lead.update")
		dataPermissions, _ := role["data_permissions"].([]any)
		role["data_permissions"] = append(dataPermissions, map[string]any{"object_key": "lead", "scope": "all_records", "read": true, "write": true})
	}
	workflows, _ := manifest["workflows"].([]any)
	manifest["workflows"] = append(workflows,
		schedulerGlobalActionWorkflow("business_config_activation", "lead.activate_due_candidates"),
		schedulerGlobalActionWorkflow("daily_operations_review", "lead.create_daily_review_tasks"),
		schedulerGlobalActionWorkflow("lead_activation_failure", "lead.fail_due_candidates"),
	)
	manifest["scheduler_definitions"] = []any{
		map[string]any{"key": "business_config_activation_job", "name": "Business configuration activation", "status": "enabled", "trigger_type": "scheduled", "schedule_type": "interval", "interval_seconds": 60, "timezone": "UTC", "target_type": "workflow", "target_key": "scheduled:business_config_activation", "max_attempts": 1, "timeout_seconds": 120},
		map[string]any{"key": "daily_operations_review_job", "name": "Daily operations review", "status": "enabled", "trigger_type": "scheduled", "schedule_type": "daily_at", "time_of_day": "08:00", "timezone": "America/Sao_Paulo", "target_type": "workflow", "target_key": "scheduled:daily_operations_review", "max_attempts": 1, "timeout_seconds": 300},
		map[string]any{"key": "lead_activation_failure_job", "name": "Lead activation failure", "status": "enabled", "trigger_type": "scheduled", "schedule_type": "interval", "interval_seconds": 60, "timezone": "UTC", "target_type": "workflow", "target_key": "scheduled:lead_activation_failure", "max_attempts": 1, "timeout_seconds": 120},
	}
	normalized, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "scheduler-global-workflow-manifest.json")
	if err := os.WriteFile(path, normalized, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func schedulerGlobalActionWorkflow(key, actionKey string) map[string]any {
	return map[string]any{
		"key": key, "name": key, "enabled": true, "run_as": "platform_admin", "trigger": map[string]any{"type": "scheduled"},
		"trigger_contract": map[string]any{"type": "scheduled"}, "condition": map[string]any{}, "action": map[string]any{"type": "workflow_graph"},
		"graph": map[string]any{
			"version": 2,
			"nodes": []any{
				map[string]any{"id": "scheduled", "type": "trigger", "name": "Scheduled"},
				map[string]any{"id": "invoke", "type": "action", "name": "Invoke", "contract": map[string]any{"action": map[string]any{
					"action_key": actionKey, "object_key": "lead", "input": map[string]any{}, "on_error": "fail",
				}}},
			},
			"edges": []any{map[string]any{"id": "scheduled-invoke", "source": "scheduled", "target": "invoke"}},
		},
	}
}
