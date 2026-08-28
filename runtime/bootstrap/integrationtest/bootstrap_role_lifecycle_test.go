package integrationtest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRuntimeCRMLifecycleRoleMatrixAndExceptionPaths(t *testing.T) {
	application := newIntegrationRuntime(t, config.Config{
		AppLocale:      "en-US",
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath:   filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"),
		UploadDir:      filepath.Join(t.TempDir(), "uploads"),
	})
	defer application.CloseContext(t.Context())
	handler := application.Routes()

	managerLeadID := runtimeRoleFixtureRecordIDByField(t, handler, "sales_manager", "lead", "owner", "sales_manager")
	repLeadID := runtimeRoleFixtureRecordIDByField(t, handler, "sales_rep", "lead", "owner", "sales_rep_1")
	runtimeRoleFixtureRequestStatus(t, handler, "sales_rep", http.MethodPost, "/objects/lead/records/"+managerLeadID+"/actions/lead.qualify", map[string]any{"data": map[string]any{}}, http.StatusForbidden)
	runtimeRoleFixtureRequestStatus(t, handler, "sales_rep", http.MethodPost, "/objects/lead/records/"+repLeadID+"/actions/lead.convert", map[string]any{"data": map[string]any{}}, http.StatusBadRequest)
	repLead := runtimeRoleFixtureRequest[map[string]any](t, handler, "sales_rep", http.MethodPost, "/objects/lead/records/"+repLeadID+"/actions/lead.qualify", map[string]any{"data": map[string]any{}})
	assertRuntimeFixtureRecordField(t, repLead, "status", "qualified")
	repLead = runtimeRoleFixtureRequest[map[string]any](t, handler, "sales_rep", http.MethodPost, "/objects/lead/records/"+repLeadID+"/actions/lead.convert", map[string]any{"data": map[string]any{}})
	assertRuntimeFixtureRecordField(t, repLead, "status", "converted")

	repOpportunityID := runtimeRoleFixtureRecordIDByField(t, handler, "sales_rep", "opportunity", "stage", "proposal")
	runtimeRoleFixtureRequestStatus(t, handler, "sales_rep", http.MethodPost, "/objects/opportunity/records/"+repOpportunityID+"/actions/opportunity.mark_won", map[string]any{"data": map[string]any{}}, http.StatusBadRequest)
	repOpportunity := runtimeRoleFixtureRequest[map[string]any](t, handler, "sales_rep", http.MethodPost, "/objects/opportunity/records/"+repOpportunityID+"/actions/opportunity.advance_stage", map[string]any{"data": map[string]any{"stage": "negotiation"}})
	assertRuntimeFixtureRecordField(t, repOpportunity, "stage", "negotiation")
	repOpportunity = runtimeRoleFixtureRequest[map[string]any](t, handler, "sales_rep", http.MethodPost, "/objects/opportunity/records/"+repOpportunityID+"/actions/opportunity.mark_won", map[string]any{"data": map[string]any{}})
	assertRuntimeFixtureRecordField(t, repOpportunity, "stage", "won")

	repActivityID := runtimeRoleFixtureRecordIDByField(t, handler, "sales_rep", "activity", "status", "open")
	repActivity := runtimeRoleFixtureRequest[map[string]any](t, handler, "sales_rep", http.MethodPost, "/objects/activity/records/"+repActivityID+"/actions/activity.complete", map[string]any{"data": map[string]any{}})
	assertRuntimeFixtureRecordField(t, repActivity, "status", "completed")
	runtimeRoleFixtureRequestStatus(t, handler, "sales_rep", http.MethodPost, "/objects/activity/records/"+repActivityID+"/actions/activity.start", map[string]any{"data": map[string]any{}}, http.StatusBadRequest)

	draftContractID := runtimeRoleFixtureRecordIDByField(t, handler, "sales_manager", "contract", "status", "draft")
	runtimeRoleFixtureRequestStatus(t, handler, "finance_reviewer", http.MethodPost, "/objects/contract/records/"+draftContractID+"/actions/contract.sign", map[string]any{"data": map[string]any{}}, http.StatusForbidden)
	duePaymentID := runtimeRoleFixtureRecordIDByField(t, handler, "finance_reviewer", "payment", "status", "due")
	runtimeRoleFixtureRequestStatus(t, handler, "sales_rep", http.MethodPost, "/objects/payment/records/"+duePaymentID+"/actions/payment.mark_collected", map[string]any{"data": map[string]any{}}, http.StatusForbidden)
	financePayment := runtimeRoleFixtureRequest[map[string]any](t, handler, "finance_reviewer", http.MethodPost, "/objects/payment/records/"+duePaymentID+"/actions/payment.mark_collected", map[string]any{"data": map[string]any{}})
	assertRuntimeFixtureRecordField(t, financePayment, "status", "collected")
}

func runtimeRoleFixtureRecordIDByField(t *testing.T, handler http.Handler, role string, objectKey string, field string, expected any) string {
	t.Helper()
	page := runtimeRoleFixtureRequest[map[string]any](t, handler, role, http.MethodGet, "/objects/"+objectKey+"/records?page=1&page_size=50", nil)
	items, ok := page["items"].([]any)
	if !ok {
		t.Fatalf("expected seeded %s records, got %#v", objectKey, page)
	}
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		data, ok := record["data"].(map[string]any)
		if !ok || data[field] != expected {
			continue
		}
		id, _ := record["id"].(string)
		if id != "" {
			return id
		}
	}
	t.Fatalf("expected %s record with %s=%v, got %#v", objectKey, field, expected, items)
	return ""
}

func runtimeRoleFixtureRequest[T any](t *testing.T, handler http.Handler, role string, method string, path string, body any) T {
	t.Helper()
	res := runtimeRoleFixtureDo(t, handler, role, method, path, body)
	if res.Code < 200 || res.Code >= 300 {
		t.Fatalf("%s %s returned %d: %s", method, path, res.Code, res.Body.String())
	}
	var out T
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s %s response: %v\n%s", method, path, err, res.Body.String())
	}
	return out
}

func runtimeRoleFixtureRequestStatus(t *testing.T, handler http.Handler, role string, method string, path string, body any, expectedStatus int) {
	t.Helper()
	res := runtimeRoleFixtureDo(t, handler, role, method, path, body)
	if res.Code != expectedStatus {
		t.Fatalf("%s %s returned %d, expected %d: %s", method, path, res.Code, expectedStatus, res.Body.String())
	}
}

func runtimeRoleFixtureDo(t *testing.T, handler http.Handler, role string, method string, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody *bytes.Reader
	if body == nil {
		reqBody = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal %s %s request: %v", method, path, err)
		}
		reqBody = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reqBody)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Idempotency-Key", method+":"+path+":"+time.Now().UTC().Format(time.RFC3339Nano))
	}
	req.Header.Set("Authorization", "Bearer "+integrationIdentityAccessTokenFor(runtimeRoleFixtureUser(role), role))
	req.Header.Set("X-Workspace-ID", "default")
	req.Header.Set("X-Domainry-Product-Surface", "business_workspace")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func runtimeRoleFixtureUser(role string) string {
	switch role {
	case "sales_rep":
		return "sales_rep_1"
	case "finance_reviewer":
		return "finance_reviewer"
	default:
		return role
	}
}
