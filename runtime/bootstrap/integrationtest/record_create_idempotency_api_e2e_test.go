package integrationtest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRecordCreateAPIRequiresCallerKeyAndReplaysOneDurableRecord(t *testing.T) {
	application := newIntegrationRuntime(t, config.Config{
		DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath: filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "restaurant-kitchen.json"),
		UploadDir:    filepath.Join(t.TempDir(), "uploads"),
	})
	defer application.CloseContext(t.Context())
	handler := application.Routes()
	body := map[string]any{"data": map[string]any{"table_no": "T-99", "status": "queued", "station": "hot", "priority": 1}}

	missing := recordCreateRequest(t, handler, "", body)
	if missing.Code != http.StatusBadRequest || !bytes.Contains(missing.Body.Bytes(), []byte("operations.idempotency_contract_required")) {
		t.Fatalf("missing key response=%d %s", missing.Code, missing.Body.String())
	}
	first := recordCreateRequest(t, handler, "record-create-1", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first create=%d %s", first.Code, first.Body.String())
	}
	second := recordCreateRequest(t, handler, "record-create-1", body)
	if second.Code != http.StatusCreated {
		t.Fatalf("replay create=%d %s", second.Code, second.Body.String())
	}
	if second.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay header=%q", second.Header().Get("Idempotency-Replayed"))
	}
	var firstRecord, secondRecord map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &firstRecord); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondRecord); err != nil {
		t.Fatal(err)
	}
	if firstRecord["id"] == "" || secondRecord["id"] != firstRecord["id"] {
		t.Fatalf("first=%#v second=%#v", firstRecord, secondRecord)
	}
	conflictBody := map[string]any{"data": map[string]any{"table_no": "T-100", "status": "queued", "station": "hot", "priority": 1}}
	conflict := recordCreateRequest(t, handler, "record-create-1", conflictBody)
	if conflict.Code != http.StatusConflict || !bytes.Contains(conflict.Body.Bytes(), []byte("backend.idempotency.key_reused")) {
		t.Fatalf("conflict response=%d %s", conflict.Code, conflict.Body.String())
	}
	page := runtimeFixtureRequest[map[string]any](t, handler, "kitchen_lead", http.MethodGet, "/records/kitchen_order?page=1&page_size=100", nil)
	items, _ := page["items"].([]any)
	createdCount := 0
	for _, item := range items {
		record, _ := item.(map[string]any)
		data, _ := record["data"].(map[string]any)
		if data["table_no"] == "T-99" {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created records with caller key=%d items=%#v", createdCount, items)
	}
}

func TestRecordImportAPIReplaysOperationAndDoesNotDuplicateRows(t *testing.T) {
	application := newIntegrationRuntime(t, config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db"), ManifestPath: filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "restaurant-kitchen.json"), UploadDir: filepath.Join(t.TempDir(), "uploads")})
	defer application.CloseContext(t.Context())
	handler := application.Routes()
	csv := "table_no,status,station,priority\nT-201,queued,hot,1\nT-202,queued,cold,2\n"
	first := recordImportRequest(t, handler, "import-operation-1", csv)
	if first.Code != http.StatusOK {
		t.Fatalf("first import=%d %s", first.Code, first.Body.String())
	}
	second := recordImportRequest(t, handler, "import-operation-1", csv)
	if second.Code != http.StatusOK || second.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replayed import=%d header=%q body=%s", second.Code, second.Header().Get("Idempotency-Replayed"), second.Body.String())
	}
	conflict := recordImportRequest(t, handler, "import-operation-1", "table_no,status,station,priority\nT-203,queued,bar,3\n")
	if conflict.Code != http.StatusConflict || !bytes.Contains(conflict.Body.Bytes(), []byte("backend.idempotency.key_reused")) {
		t.Fatalf("import conflict=%d %s", conflict.Code, conflict.Body.String())
	}
	page := runtimeFixtureRequest[map[string]any](t, handler, "kitchen_lead", http.MethodGet, "/records/kitchen_order?page=1&page_size=100", nil)
	items, _ := page["items"].([]any)
	counts := map[string]int{}
	for _, item := range items {
		record, _ := item.(map[string]any)
		data, _ := record["data"].(map[string]any)
		if table, _ := data["table_no"].(string); table == "T-201" || table == "T-202" || table == "T-203" {
			counts[table]++
		}
	}
	if counts["T-201"] != 1 || counts["T-202"] != 1 || counts["T-203"] != 0 {
		t.Fatalf("imported row counts=%v", counts)
	}
}

func TestRecordUpdateAPIRequiresCallerKeyAndRejectsFingerprintReuse(t *testing.T) {
	application := newIntegrationRuntime(t, config.Config{
		DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath: filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "restaurant-kitchen.json"),
		UploadDir:    filepath.Join(t.TempDir(), "uploads"),
	})
	defer application.CloseContext(t.Context())
	handler := application.Routes()
	created := recordCreateRequest(t, handler, "record-update-fixture", map[string]any{"data": map[string]any{
		"table_no": "T-UPDATE", "status": "queued", "station": "hot", "priority": 1,
	}})
	if created.Code != http.StatusCreated {
		t.Fatalf("fixture create=%d %s", created.Code, created.Body.String())
	}
	var record map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	recordID, _ := record["id"].(string)
	updatedAt, _ := record["updated_at"].(string)
	patch := map[string]any{"data": map[string]any{"status": "ready", "expected_updated_at": updatedAt}}

	missing := recordUpdateRequest(t, handler, recordID, "", patch)
	if missing.Code != http.StatusBadRequest || !bytes.Contains(missing.Body.Bytes(), []byte("backend.idempotency.key_required")) {
		t.Fatalf("missing update key=%d %s", missing.Code, missing.Body.String())
	}
	first := recordUpdateRequest(t, handler, recordID, "record-update-1", patch)
	if first.Code != http.StatusOK {
		t.Fatalf("first update=%d %s", first.Code, first.Body.String())
	}
	replay := recordUpdateRequest(t, handler, recordID, "record-update-1", patch)
	if replay.Code != http.StatusOK || replay.Body.String() != first.Body.String() {
		t.Fatalf("update replay=%d first=%s replay=%s", replay.Code, first.Body.String(), replay.Body.String())
	}
	conflict := recordUpdateRequest(t, handler, recordID, "record-update-1", map[string]any{"data": map[string]any{"status": "served"}})
	if conflict.Code != http.StatusConflict || !bytes.Contains(conflict.Body.Bytes(), []byte("backend.idempotency.key_reused")) {
		t.Fatalf("update conflict=%d %s", conflict.Code, conflict.Body.String())
	}
}

func recordCreateRequest(t *testing.T, handler http.Handler, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/records/kitchen_order", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	applyIntegrationIdentity(request, "kitchen_lead")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func recordUpdateRequest(t *testing.T, handler http.Handler, recordID, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPatch, "/records/kitchen_order/items/"+recordID, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	applyIntegrationIdentity(request, "kitchen_lead")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func recordImportRequest(t *testing.T, handler http.Handler, key, csv string) *httptest.ResponseRecorder {
	t.Helper()
	return recordCreatePathRequest(t, handler, "/records/kitchen_order/import/apply", key, map[string]any{"csv": csv})
}

func recordCreatePathRequest(t *testing.T, handler http.Handler, path, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	applyIntegrationIdentity(request, "kitchen_lead")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
