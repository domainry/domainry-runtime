package integrationtest

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRuntimeCRMReportObjectExportsRetireTheUngovernedSecondQuery(t *testing.T) {
	application := newIntegrationRuntime(t, config.Config{
		AppLocale:      "en-US",
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath:   filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"),
		UploadDir:      filepath.Join(t.TempDir(), "uploads"),
	})
	defer application.Close()
	handler := application.Routes()

	for _, path := range []string{
		"/reports/crm_pipeline_health/exports/opportunity",
		"/reports/crm_revenue_collection/exports/payment",
	} {
		runtimeFixtureRawRequest(t, handler, "sales_manager", http.MethodGet, path, nil, http.StatusNotFound)
	}

	// Report authorization remains enforced on the single canonical query.
	runtimeFixtureRawRequest(t, handler, "sales_manager", http.MethodGet, "/reports/crm_pipeline_health/summary?page_size=50", nil, http.StatusOK)
	runtimeFixtureRawRequest(t, handler, "finance_reviewer", http.MethodGet, "/reports/crm_revenue_collection/summary?page_size=50", nil, http.StatusOK)
	runtimeFixtureRawRequest(t, handler, "sales_rep", http.MethodGet, "/reports/crm_revenue_collection/summary?page_size=50", nil, http.StatusNotFound)
}

func runtimeFixtureRawRequest(t *testing.T, handler http.Handler, role string, method string, path string, body []byte, expectedStatus int) []byte {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	applyIntegrationIdentity(req, role)
	req.Header.Set("X-Domainry-Product-Surface", "business_workspace")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != expectedStatus {
		t.Fatalf("%s %s returned %d, expected %d: %s", method, path, res.Code, expectedStatus, res.Body.String())
	}
	return res.Body.Bytes()
}
