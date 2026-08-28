package integrationtest

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRuntimeCRMRelatedRecordsArePagedAndRoleScoped(t *testing.T) {
	application := newIntegrationRuntime(t, config.Config{
		AppLocale:      "en-US",
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath:   filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"),
		UploadDir:      filepath.Join(t.TempDir(), "uploads"),
	})
	defer application.CloseContext(t.Context())
	handler := application.Routes()

	customerID := runtimeFixtureRecordIDByField(t, handler, "sales_manager", "customer", "name", "Acme Manufacturing")
	contacts := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodGet, "/objects/customer/records/"+customerID+"/related/contact?field=customer&page=1&page_size=10", nil)
	assertRuntimeFixturePageTotal(t, contacts, 1)
	opportunities := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodGet, "/objects/customer/records/"+customerID+"/related/opportunity?field=customer&page=1&page_size=10", nil)
	assertRuntimeFixturePageTotal(t, opportunities, 1)
	contracts := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodGet, "/objects/customer/records/"+customerID+"/related/contract?field=customer&page=1&page_size=10", nil)
	assertRuntimeFixturePageTotal(t, contracts, 2)

	contractID := runtimeFixtureRecordIDByField(t, handler, "sales_manager", "contract", "status", "signed")
	payments := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodGet, "/objects/contract/records/"+contractID+"/related/payment?field=contract&page=1&page_size=10", nil)
	assertRuntimeFixturePageTotal(t, payments, 2)

	runtimeFixtureRequestStatus(t, handler, "sales_rep", http.MethodGet, "/objects/contract/records/"+contractID+"/related/payment?field=contract&page=1&page_size=10", nil, http.StatusNotFound)
}

func assertRuntimeFixturePageTotal(t *testing.T, payload map[string]any, expected int) {
	t.Helper()
	total, ok := payload["total"].(float64)
	if !ok {
		t.Fatalf("expected page total in %#v", payload)
	}
	if int(total) != expected {
		t.Fatalf("expected page total %d, got %#v", expected, payload)
	}
}
