package integrationtest

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	bootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap"
	schedulerprojection "github.com/domainry/domainry-runtime/runtime/domain/scheduler/projection"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestManifestSchedulerDefinitionManualCapabilityStartupRestartAndExactlyOnceWorkflowAction(t *testing.T) {
	temp := t.TempDir()
	source := filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	// Model compiler output rather than the legacy fixture's older scheduler
	// objects: source-owned published jobs always carry the canonical Runtime
	// Scheduler persistence projection.
	canonicalRaw, err := json.Marshal(schedulerprojection.SchedulerSystemObjects())
	if err != nil {
		t.Fatal(err)
	}
	var canonical []any
	if err := json.Unmarshal(canonicalRaw, &canonical); err != nil {
		t.Fatal(err)
	}
	canonicalKeys := map[string]bool{}
	for _, rawObject := range canonical {
		object, _ := rawObject.(map[string]any)
		canonicalKeys[object["key"].(string)] = true
	}
	objects, _ := manifest["objects"].([]any)
	filtered := make([]any, 0, len(objects)+len(canonical))
	for _, rawObject := range objects {
		object, _ := rawObject.(map[string]any)
		if object["key"].(string) != "job_definition" && !canonicalKeys[object["key"].(string)] {
			filtered = append(filtered, rawObject)
		}
	}
	manifest["objects"] = append(filtered, canonical...)
	seedRecords, _ := manifest["seed_records"].([]any)
	filteredSeeds := make([]any, 0, len(seedRecords))
	for _, rawSeed := range seedRecords {
		seed, _ := rawSeed.(map[string]any)
		objectKey, _ := seed["object_key"].(string)
		if objectKey == "job_definition" || canonicalKeys[objectKey] {
			continue
		}
		filteredSeeds = append(filteredSeeds, rawSeed)
	}
	manifest["seed_records"] = filteredSeeds
	dueAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	manifest["scheduler_definitions"] = []any{map[string]any{
		"key": "business_config_activation_job", "name": "Business Config Activation", "status": "enabled",
		"schedule_type": "interval", "interval_seconds": 60, "timezone": "UTC", "next_run_at": dueAt.Format(time.RFC3339),
		"target_type": "workflow", "target_key": "scheduled:payment.overdue_escalation",
		"max_attempts": 3, "timeout_seconds": 60, "retry_backoff": "fixed", "retry_delay_seconds": 1,
		"missed_window_policy": "catch_up_one", "max_catchup_windows": 1,
	}}
	roles, _ := manifest["roles"].([]any)
	manifest["roles"] = append(roles, map[string]any{
		"key": "scheduler_operator", "name": "Scheduler Operator",
		"permissions": []any{"admin_console.access", "scheduler.command", "scheduler.definition.read"}, "record_scope": "all_records",
		"data_permissions": []any{
			map[string]any{"object_key": "customer", "scope": "all_records", "read": true, "write": false},
			map[string]any{"object_key": "job_run", "scope": "all_records", "read": true, "write": true},
		},
	})
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(temp, "scheduler-manifest.json")
	if err := os.WriteFile(manifestPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(temp, "runtime.db"), ManifestPath: manifestPath,
		UploadDir: filepath.Join(temp, "uploads"), SchedulerEnabled: true, SchedulerPollInterval: 20 * time.Millisecond, SchedulerBatchSize: 5,
		SchedulerLeaseTTL: time.Minute, SchedulerMaxCatchupWindows: 1,
	}
	runtime := newIntegrationRuntime(t, cfg)
	store := openRuntimePersistenceFixture(t, cfg)
	defer store.Close()

	definitionPage := schedulerOperatorRequest(t, runtime.Routes(), http.MethodGet, "/tenant-admin/scheduler/definitions", "", http.StatusOK)
	if items, _ := definitionPage["items"].([]any); len(items) != 1 {
		t.Fatalf("published manifest scheduler definitions=%#v", definitionPage)
	}
	first := schedulerOperatorRequest(t, runtime.Routes(), http.MethodPost, "/operations/scheduler/definitions/business_config_activation_job/run", "manual-activation", http.StatusOK)
	replay := schedulerOperatorRequest(t, runtime.Routes(), http.MethodPost, "/operations/scheduler/definitions/business_config_activation_job/run", "manual-activation", http.StatusOK)
	if first["status"] == "" || replay["idempotency_replayed"] != true {
		t.Fatalf("manual first=%#v replay=%#v", first, replay)
	}
	assertSchedulerActionCount(t, store.DB(), 1)

	bootstrap.StartWorkers(t.Context(), runtime)
	waitForSchedulerPersistence(t, store.DB(), "business_config_activation_job", 2)
	state := schedulerOperatorRequest(t, runtime.Routes(), http.MethodGet, "/operations/scheduler/state", "", http.StatusOK)
	if state["provisioned"] != true {
		t.Fatalf("scheduler state=%#v", state)
	}
	if err := runtime.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}

	restarted := newIntegrationRuntime(t, cfg)
	bootstrap.StartWorkers(t.Context(), restarted)
	defer restarted.CloseContext(t.Context())
	// The successful missed window advanced the durable cursor into the future;
	// restart before that next interval must not repeat its workflow action.
	time.Sleep(150 * time.Millisecond)
	assertSchedulerActionCount(t, store.DB(), 2)
}

func schedulerOperatorRequest(t *testing.T, handler http.Handler, method, path, idempotencyKey string, expected int) map[string]any {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(nil))
	req.Header.Set("Authorization", "Bearer "+integrationIdentityAccessTokenFor("scheduler-operator", "scheduler_operator"))
	req.Header.Set("X-Workspace-ID", "default")
	req.Header.Set("X-Domainry-Product-Surface", "admin_console")
	req.Header.Set("X-Operation-Reason", "scheduler contract integration verification")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != expected {
		t.Fatalf("%s %s status=%d body=%s", method, path, res.Code, res.Body.String())
	}
	out := map[string]any{}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	out["idempotency_replayed"] = res.Header().Get("Idempotency-Replayed") == "true"
	return out
}

func waitForSchedulerPersistence(t *testing.T, db *sql.DB, definitionID string, wantActions int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var cursors, runs, actions int
	var cursorErr, runErr, actionErr error
	for time.Now().Before(deadline) {
		cursorErr = db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM scheduler_schedule_state WHERE definition_key = ?`, definitionID).Scan(&cursors)
		runErr = db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM scheduler_runs WHERE definition_key = ? AND status = 'succeeded'`, definitionID).Scan(&runs)
		actionErr = db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM business_action_executions WHERE action_key = 'payment.record_overdue_escalation' AND status = 'succeeded'`).Scan(&actions)
		if cursorErr == nil && runErr == nil && actionErr == nil && cursors == 1 && runs == 1 && actions == wantActions {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("scheduler persistence did not converge for %q: cursors=%d runs=%d actions=%d cursorErr=%v runErr=%v actionErr=%v", definitionID, cursors, runs, actions, cursorErr, runErr, actionErr)
}

func assertSchedulerActionCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM business_action_executions WHERE action_key = 'payment.record_overdue_escalation' AND status = 'succeeded'`).Scan(&got); err != nil || got != want {
		t.Fatalf("scheduler workflow action count=%d want=%d err=%v", got, want, err)
	}
}
