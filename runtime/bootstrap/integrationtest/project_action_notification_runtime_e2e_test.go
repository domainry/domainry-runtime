package integrationtest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	bootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestProjectActionNotificationDispatchRealRuntimeReplayConcurrencyLifecycleAndRestart(t *testing.T) {
	temp := t.TempDir()
	manifestPath := projectNotificationManifest(t, temp)
	databasePath := filepath.Join(temp, "runtime.db")
	cfg := config.Config{AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: databasePath, ManifestPath: manifestPath, UploadDir: filepath.Join(temp, "uploads")}
	runtime := newIntegrationRuntime(t, cfg)
	bootstrap.StartWorkers(t.Context(), runtime)
	handler := runtime.Routes()
	leadID := firstRuntimeFixtureRecordID(t, handler, "sales_manager", "lead")
	path := "/objects/lead/records/" + leadID + "/actions/lead.qualify"

	type result struct {
		status int
		body   string
		key    string
	}
	results := make(chan result, 2)
	var group sync.WaitGroup
	for _, key := range []string{"notification-concurrent-a", "notification-concurrent-b"} {
		group.Add(1)
		go func(key string) {
			defer group.Done()
			status, body := projectNotificationRequest(handler, "runtime_fixture_user", "sales_manager", http.MethodPost, path, key, map[string]any{"data": map[string]any{}})
			results <- result{status: status, body: body, key: key}
		}(key)
	}
	group.Wait()
	close(results)
	winner := ""
	observed := []result{}
	successes := 0
	for value := range results {
		observed = append(observed, value)
		if value.status >= 200 && value.status < 300 {
			winner = value.key
			successes++
		} else if value.status != http.StatusConflict {
			t.Fatalf("concurrent duplicate must fail as conflict, result=%#v all=%#v", value, observed)
		}
	}
	if winner == "" || successes != 1 {
		t.Fatalf("both concurrent Action calls failed: %#v", observed)
	}
	status, body := projectNotificationRequest(handler, "runtime_fixture_user", "sales_manager", http.MethodPost, path, winner, map[string]any{"data": map[string]any{}})
	if status < 200 || status >= 300 {
		t.Fatalf("idempotent replay status=%d body=%s", status, body)
	}

	item := waitProjectNotification(t, handler, "admin")
	if item["event_type"] != "lead.qualified" || item["recipient_user_id"] != "admin" || item["subject_type"] != "lead" || item["subject_id"] != leadID || item["subject_version"] == "" {
		t.Fatalf("notification=%#v", item)
	}
	other := projectNotificationList(t, handler, "other-user")
	if len(other) != 0 {
		t.Fatalf("notification leaked to another user: %#v", other)
	}
	status, body = projectNotificationRequest(handler, "finance-user", "finance_reviewer", http.MethodPost, path, "notification-denied", map[string]any{"data": map[string]any{}})
	if status != http.StatusForbidden {
		t.Fatalf("denied Action status=%d body=%s", status, body)
	}
	status, body = projectNotificationRequest(handler, "runtime_fixture_user", "sales_manager", http.MethodPost, "/objects/lead/records/"+leadID+"/actions/lead.convert", "notification-invalid", map[string]any{"data": map[string]any{}})
	if status != http.StatusBadRequest || !bytes.Contains([]byte(body), []byte("backend.notification.template_variable_required")) {
		t.Fatalf("invalid Action status=%d body=%s", status, body)
	}
	lead := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodGet, "/objects/lead/records/"+leadID, nil)
	data, _ := lead["data"].(map[string]any)
	if data["status"] != "qualified" || item["subject_version"] != lead["updated_at"] {
		t.Fatalf("invalid notification partially committed record=%#v", lead)
	}
	notificationID := item["id"].(string)
	status, body = projectNotificationRequest(handler, "admin", "sales_manager", http.MethodGet, "/business/notifications/"+notificationID+"/actions/lead.open/resolve", "", nil)
	if status != http.StatusOK || !bytes.Contains([]byte(body), []byte(`"route_key":"lead.detail"`)) || !bytes.Contains([]byte(body), []byte(`"object_key":"lead"`)) {
		t.Fatalf("safe action status=%d body=%s", status, body)
	}
	status, body = projectNotificationRequest(handler, "admin", "sales_manager", http.MethodPost, "/business/notifications/"+notificationID+"/read", "notification-read", nil)
	if status != http.StatusOK || !bytes.Contains([]byte(body), []byte(`"read_at":"`)) {
		t.Fatalf("read status=%d body=%s", status, body)
	}
	status, body = projectNotificationRequest(handler, "admin", "sales_manager", http.MethodPost, "/business/notifications/"+notificationID+"/acknowledge", "notification-handled", nil)
	if status != http.StatusOK || !bytes.Contains([]byte(body), []byte(`"alert_state":"acknowledged"`)) {
		t.Fatalf("handled status=%d body=%s", status, body)
	}
	audits := runtimeFixtureRequestWithHeaders[map[string]any](t, handler, "admin", http.MethodGet, "/business/audit-events?event=notification.intent.dispatch&object_key=lead&record_id="+leadID, nil, map[string]string{"Authorization": "Bearer " + integrationIdentityAccessToken("admin"), "X-Domainry-Product-Surface": "business_workspace"})
	if items, _ := audits["items"].([]any); len(items) != 1 {
		t.Fatalf("notification audit=%#v", audits)
	}

	runtime.Close()
	restarted := newIntegrationRuntime(t, cfg)
	bootstrap.StartWorkers(t.Context(), restarted)
	defer restarted.Close()
	reloaded := projectNotificationList(t, restarted.Routes(), "admin")
	if len(reloaded) != 1 || reloaded[0]["id"] != notificationID || reloaded[0]["read_at"] == "" || reloaded[0]["alert_state"] != "acknowledged" {
		t.Fatalf("cold restart inbox=%#v", reloaded)
	}
}

func projectNotificationManifest(t *testing.T, directory string) string {
	t.Helper()
	source := filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["notification_event_types"] = []any{map[string]any{
		"key": "lead.qualified", "source": "project_action", "category": "task", "default_severity": "info", "surfaces": []any{"business_workspace"}, "mandatory_in_app": true,
		"template_key": "lead.qualified", "default_locale": "en-US", "locales": map[string]any{"en-US": map[string]any{"title": "Lead qualified", "body": "Open qualified lead", "action_labels": map[string]any{"lead.open": "Open"}}},
		"actions": []any{map[string]any{"key": "lead.open", "kind": "route", "resource_type": "project_record", "surface_routes": map[string]any{"business_workspace": "lead.detail"}}}, "version": 1, "status": "published",
	}, map[string]any{
		"key": "lead.invalid", "source": "project_action", "category": "task", "default_severity": "info", "surfaces": []any{"business_workspace"}, "mandatory_in_app": true,
		"template_key": "lead.invalid", "default_locale": "en-US", "variables": []any{map[string]any{"key": "required_name", "type": "text", "required": true}}, "locales": map[string]any{"en-US": map[string]any{"title": "Hello {{required_name}}", "body": "Invalid path", "action_labels": map[string]any{"lead.open": "Open"}}},
		"actions": []any{map[string]any{"key": "lead.open", "kind": "route", "resource_type": "project_record", "surface_routes": map[string]any{"business_workspace": "lead.detail"}}}, "version": 1, "status": "published",
	}}
	manifest["notification_rules"] = []any{
		map[string]any{"event_type_key": "lead.qualified", "enabled": true, "mandatory_in_app": true, "minimum_severity": "info", "channels": []any{map[string]any{"channel": "in_app", "mandatory": true}}},
		map[string]any{"event_type_key": "lead.invalid", "enabled": true, "mandatory_in_app": true, "minimum_severity": "info", "channels": []any{map[string]any{"channel": "in_app", "mandatory": true}}},
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "notification-manifest.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func projectNotificationRequest(handler http.Handler, userID, role, method, path, idempotencyKey string, body any) (int, string) {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Authorization", "Bearer "+integrationIdentityAccessTokenFor(userID, role))
	request.Header.Set("X-Workspace-ID", "default")
	request.Header.Set("X-Domainry-Product-Surface", "business_workspace")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response.Code, response.Body.String()
}

func projectNotificationList(t *testing.T, handler http.Handler, userID string) []map[string]any {
	t.Helper()
	status, body := projectNotificationRequest(handler, userID, "sales_manager", http.MethodGet, "/business/notifications?scope=mine&mailbox=inbox&limit=20", "", nil)
	if status != http.StatusOK {
		t.Fatalf("list notifications status=%d body=%s", status, body)
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatal(err)
	}
	return page.Items
}

func waitProjectNotification(t *testing.T, handler http.Handler, userID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		items := projectNotificationList(t, handler, userID)
		if len(items) > 0 {
			if len(items) != 1 {
				t.Fatalf("duplicate notifications=%#v", items)
			}
			return items[0]
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("notification was not materialized")
	return nil
}
