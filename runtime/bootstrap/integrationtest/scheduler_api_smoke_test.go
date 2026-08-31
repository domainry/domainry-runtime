package integrationtest

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	schedulerpolicy "github.com/domainry/domainry-runtime/runtime/domain/scheduler/policy"

	"bytes"
	"encoding/json"
	"fmt"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchedulerAPISmokeVerifiesRuntimeOperationsAndEvidence(t *testing.T) {
	definition := recordmodel.Record{ID: "scheduler_daily_workflow_scan_seed", Data: map[string]any{
		"key": "scheduler_daily_workflow_scan_seed", "status": "enabled", "trigger_type": "scheduled", "schedule_type": "interval", "interval_seconds": 86400, "timezone": "UTC",
		"target_type": "workflow", "target_key": "scheduled:*", "max_attempts": 3, "timeout_seconds": 300,
	}}
	cfg := config.Config{
		AppLocale:      "en-US",
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "scheduler-smoke.db"),
		ManifestPath:   schedulerSmokeManifest(t, definition.Data),
		UploadDir:      filepath.Join(t.TempDir(), "uploads"),
	}
	application := newIntegrationRuntime(t, cfg)
	defer application.CloseContext(t.Context())
	handler := application.Routes()
	store := openRuntimePersistenceFixture(t, cfg)

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

	runID := strings.TrimSpace(stringValueFromJSON(runResult, "id"))
	observed := schedulerSmokeRequest(t, handler, http.MethodGet, "/operations/scheduler/state", nil, http.StatusOK)
	observedRun := false
	if runs, ok := observed["runs"].([]any); ok {
		for _, rawRun := range runs {
			run, _ := rawRun.(map[string]any)
			if run["id"] == runID {
				observedRun = true
			}
		}
	}
	if !observedRun {
		t.Fatalf("Ops state did not expose source-owned run %q: %#v", runID, observed)
	}
	var operationCount int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _operation_requests WHERE workspace_id = ? AND status = 'succeeded'`, "workspace-primary").Scan(&operationCount); err != nil || operationCount != 1 {
		t.Fatalf("terminal scheduler Operations receipts=%d want=1 err=%v", operationCount, err)
	}
}

func schedulerSmokeManifest(t *testing.T, definition map[string]any) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["scheduler_definitions"] = []any{definition}
	normalized, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "scheduler-smoke-manifest.json")
	if err := os.WriteFile(path, normalized, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
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
