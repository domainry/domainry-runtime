package integrationtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// TestRuntimeUnknownListFilterIsABadRequest walks the whole published path --
// router, application service, domain service, validation -- for a listing
// whose filter names no field of the object. The refusal is raised as an
// apperror.CodedError, which the foundation defines as a leaf code carrying no
// kind; apperror.KindOf answers KindInternal for anything that is not an
// *AppError, so nothing classified it and every caller received 500
// backend.internal with the offending key discarded. A delivered product hit
// exactly that and read it as a platform fault. The refusal is the caller's to
// fix, so it must arrive as a 400 that still names the field.
func TestRuntimeUnknownListFilterIsABadRequest(t *testing.T) {
	application := newIntegrationRuntime(t, config.Config{
		AppLocale:      "en-US",
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath:   filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"),
		UploadDir:      filepath.Join(t.TempDir(), "uploads"),
	})
	defer application.CloseContext(t.Context())
	handler := application.Routes()

	path := "/records/customer?filters=" + url.QueryEscape(`{"not_a_field":"x"}`)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	applyIntegrationIdentity(req, "sales_manager")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("GET %s returned %d, expected 400: %s", path, res.Code, res.Body.String())
	}
	payload := map[string]any{}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode refusal body: %v (%s)", err, res.Body.String())
	}
	code, _ := payload["code"].(string)
	if code != "backend.validation.filter_field_unknown" {
		t.Fatalf("refusal code = %q, want backend.validation.filter_field_unknown: %s", code, res.Body.String())
	}
	params, _ := payload["params"].(map[string]any)
	if field, _ := params["field"].(string); field != "not_a_field" {
		t.Fatalf("refusal must name the rejected filter key, got params %#v: %s", params, res.Body.String())
	}
	// A listing whose filters all name real fields is untouched by the check.
	runtimeFixtureRequestStatus(t, handler, "sales_manager", http.MethodGet, "/records/customer?page=1&page_size=1", nil, http.StatusOK)
}
