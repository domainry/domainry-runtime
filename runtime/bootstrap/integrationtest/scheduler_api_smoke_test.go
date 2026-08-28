package integrationtest

import (
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	schedulerpolicy "github.com/domainry/domainry-runtime/runtime/domain/scheduler/policy"
	schedulerprojection "github.com/domainry/domainry-runtime/runtime/domain/scheduler/projection"

	"bytes"
	"encoding/json"
	"fmt"

	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestSchedulerAPISmokeVerifiesRuntimeOperationsAndEvidence(t *testing.T) {
	cfg := config.Config{
		AppLocale:      "en-US",
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "scheduler-smoke.db"),
		ManifestPath:   filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"),
		UploadDir:      filepath.Join(t.TempDir(), "uploads"),
	}
	application := newIntegrationRuntime(t, cfg)
	defer application.CloseContext(t.Context())
	handler := application.Routes()
	store := openRuntimePersistenceFixture(t, cfg)
	recordStore := recordpersistence.NewRecordStore(store)
	objects := schedulerSmokeObjectMap()

	definition := recordmodel.Record{ID: "scheduler_daily_workflow_scan_seed", Data: map[string]any{
		"key": "scheduler_daily_workflow_scan_seed", "status": "enabled", "trigger_type": "scheduled", "schedule_expression": "daily", "timezone": "UTC",
		"target_type": "workflow", "target_key": "scheduled:*", "max_attempts": 3, "timeout_seconds": 300,
	}}
	publishSchedulerDefinitionStoreFixture(t, store, definition.ID, definition.Data)
	preview := schedulerSmokeRequest(t, handler, http.MethodPost, "/tenant-admin/scheduler/definitions/validate", map[string]any{"data": definition.Data}, http.StatusOK)
	if nextRuns, ok := preview["next_runs"].([]any); !ok || len(nextRuns) != 3 {
		t.Fatalf("expected three scheduler preview times, got %#v", preview)
	}

	simulated := schedulerSmokeRequest(t, handler, http.MethodPost, "/tenant-admin/scheduler/definitions/"+definition.ID+"/simulate", nil, http.StatusOK)
	if strings.TrimSpace(stringValueFromJSON(simulated, "status")) != "simulated" {
		t.Fatalf("expected simulated scheduler response, got %#v", simulated)
	}
	runResult := schedulerSmokeRequest(t, handler, http.MethodPost, "/operations/scheduler/definitions/"+definition.ID+"/run", nil, http.StatusOK)
	if strings.TrimSpace(stringValueFromJSON(runResult, "status")) == "" {
		t.Fatalf("expected scheduler run response status, got %#v", runResult)
	}
	if replay := schedulerSmokeRequest(t, handler, http.MethodPost, "/operations/scheduler/definitions/"+definition.ID+"/run", nil, http.StatusOK); replay["idempotency_replayed"] != true {
		t.Fatalf("expected manual run replay, got %#v", replay)
	}

	runID := schedulerSmokeCreateFailedRun(t, recordStore, objects["job_run"], definition.ID)
	observed := schedulerSmokeRequest(t, handler, http.MethodGet, "/operations/scheduler/state", nil, http.StatusOK)
	observedFailedRun := false
	if runs, ok := observed["runs"].([]any); ok {
		for _, rawRun := range runs {
			run, _ := rawRun.(map[string]any)
			if run["id"] == runID && run["status"] == "failed" {
				observedFailedRun = true
			}
		}
	}
	if !observedFailedRun {
		t.Fatalf("Ops state did not expose controlled failed run %q: %#v", runID, observed)
	}
	schedulerSmokeRequest(t, handler, http.MethodPost, "/operations/scheduler/runs/"+runID+"/retry", nil, http.StatusOK)
	if replay := schedulerSmokeRequest(t, handler, http.MethodPost, "/operations/scheduler/runs/"+runID+"/retry", nil, http.StatusOK); replay["idempotency_replayed"] != true {
		t.Fatalf("expected retry replay, got %#v", replay)
	}
	schedulerSmokeRequest(t, handler, http.MethodPost, "/operations/scheduler/runs/"+runID+"/cancel", nil, http.StatusOK)
	if replay := schedulerSmokeRequest(t, handler, http.MethodPost, "/operations/scheduler/runs/"+runID+"/cancel", nil, http.StatusOK); replay["idempotency_replayed"] != true {
		t.Fatalf("expected cancel replay, got %#v", replay)
	}

	deadLetterID := schedulerSmokeCreateDeadLetter(t, recordStore, objects["job_dead_letter"], definition.ID, runID)
	schedulerSmokeRequest(t, handler, http.MethodPost, "/operations/scheduler/dead-letters/"+deadLetterID+"/resolve", map[string]any{"note": "scheduler API smoke"}, http.StatusOK)
	if replay := schedulerSmokeRequest(t, handler, http.MethodPost, "/operations/scheduler/dead-letters/"+deadLetterID+"/resolve", map[string]any{"note": "scheduler API smoke"}, http.StatusOK); replay["idempotency_replayed"] != true {
		t.Fatalf("expected dead-letter resolve replay, got %#v", replay)
	}

	eventPage, err := recordStore.ListRecords(t.Context(), "default", objects["job_run_event"], recordmodel.RecordListQuery{Page: 1, PageSize: 100})
	if err != nil {
		t.Fatalf("list scheduler run events: %v", err)
	}
	for _, eventType := range []string{"retry_scheduled", "cancelled", "dead_letter_resolved"} {
		if !schedulerSmokeHasEvent(eventPage.Items, eventType) {
			t.Fatalf("expected scheduler event %q in %#v", eventType, eventPage.Items)
		}
	}
	var operationCount int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM runtime_operations WHERE workspace_id = ? AND status = 'succeeded'`, "default").Scan(&operationCount); err != nil || operationCount != 4 {
		t.Fatalf("terminal scheduler Operations receipts=%d want=4 err=%v", operationCount, err)
	}
}

func schedulerSmokeRequest(t *testing.T, handler http.Handler, method string, path string, body any, expectedStatus int) map[string]any {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal scheduler smoke request: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	applyIntegrationIdentity(req, "platform_admin")
	if strings.HasPrefix(path, "/tenant-admin/") {
		req.Header.Set("X-Domainry-Product-Surface", "admin_console")
	} else if strings.HasPrefix(path, "/operations/") {
		req.Header.Set("X-Domainry-Product-Surface", "admin_console")
		req.Header.Set("X-Operation-Reason", "scheduler API smoke controlled recovery")
		if strings.Contains(path, "/cancel") || strings.Contains(path, "/resolve") || strings.Contains(path, "/requeue") {
			req.Header.Set("X-Operation-Confirmation", "confirmed")
		}
	}
	req.Header.Set("Idempotency-Key", "scheduler-smoke-"+schedulerpolicy.SchedulerSlug(path))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != expectedStatus {
		t.Fatalf("%s %s returned %d, expected %d: %s", method, path, res.Code, expectedStatus, res.Body.String())
	}
	if strings.Contains(path, "/run") || strings.Contains(path, "/retry") || strings.Contains(path, "/cancel") || strings.Contains(path, "/resolve") {
		if res.Header().Get("Operation-ID") == "" || res.Header().Get("Operation-Location") == "" {
			t.Fatalf("%s %s did not return unified Operations receipt headers: %#v", method, path, res.Header())
		}
	}
	out := map[string]any{}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode scheduler smoke response: %v\n%s", err, res.Body.String())
	}
	out["idempotency_replayed"] = res.Header().Get("Idempotency-Replayed") == "true"
	return out
}

func schedulerSmokeCreateFailedRun(t *testing.T, store recordpersistence.RecordStore, object definitionmodel.ObjectSchema, definitionID string) string {
	t.Helper()
	run := recordmodel.Record{
		ID:        "scheduler_api_smoke_failed_run",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Data: map[string]any{
			"scheduler_definition_key": definitionID,
			"status":                   "failed",
			"triggered_by":             "scheduler",
			"scheduled_for":            "2026-07-09T00:00:00Z",
			"attempt":                  1,
			"max_attempts":             3,
			"timeout_seconds":          300,
			"idempotency_key":          "scheduler-api-smoke",
			"workflow_key":             "scheduled:*",
			"payload_json":             "{}",
			"result_json":              "{}",
			"error_message":            "scheduler API smoke failure",
		},
	}
	if err := store.InsertRecord(t.Context(), "default", object, run); err != nil {
		t.Fatalf("insert scheduler smoke failed run: %v", err)
	}
	return run.ID
}

func schedulerSmokeCreateDeadLetter(t *testing.T, store recordpersistence.RecordStore, object definitionmodel.ObjectSchema, definitionID string, runID string) string {
	t.Helper()
	deadLetter := recordmodel.Record{
		ID:        "scheduler_api_smoke_dead_letter",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Data: map[string]any{
			"job_run_id":               runID,
			"scheduler_definition_key": definitionID,
			"status":                   "open",
			"reason":                   "scheduler API smoke dead letter",
			"last_error":               "scheduler API smoke failure",
			"failed_at":                "2026-07-09T00:00:00Z",
		},
	}
	if err := store.InsertRecord(t.Context(), "default", object, deadLetter); err != nil {
		t.Fatalf("insert scheduler smoke dead letter: %v", err)
	}
	return deadLetter.ID
}

func schedulerSmokeObjectMap() map[string]definitionmodel.ObjectSchema {
	objects := map[string]definitionmodel.ObjectSchema{}
	for _, object := range schedulerprojection.SchedulerSystemObjects() {
		objects[object.Key] = object
	}
	return objects
}

func schedulerSmokeRecordByDataKey(t *testing.T, records []recordmodel.Record, key string, value string) recordmodel.Record {
	t.Helper()
	for _, record := range records {
		if strings.TrimSpace(schedulerSmokeStringValue(record.Data[key])) == value {
			return record
		}
	}
	t.Fatalf("scheduler smoke record %s=%s not found in %#v", key, value, records)
	return recordmodel.Record{}
}

func schedulerSmokeHasEvent(events []recordmodel.Record, eventType string) bool {
	for _, event := range events {
		if strings.TrimSpace(schedulerSmokeStringValue(event.Data["event_type"])) == eventType {
			return true
		}
	}
	return false
}

func stringValueFromJSON(payload map[string]any, key string) string {
	if value, ok := payload[key]; ok && value != nil {
		return schedulerSmokeStringValue(value)
	}
	return ""
}

func schedulerSmokeStringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
