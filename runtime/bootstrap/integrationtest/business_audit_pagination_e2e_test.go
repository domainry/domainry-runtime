package integrationtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	auditpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRuntimeBusinessAuditPaginationTraversesStableSQLitePagesOverHTTP(t *testing.T) {
	directory := t.TempDir()
	cfg := config.Config{
		AppLocale:      "en-US",
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(directory, "runtime.db"),
		ManifestPath:   filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "domain-only-minimal.json"),
		UploadDir:      filepath.Join(directory, "uploads"),
	}
	application := newIntegrationRuntime(t, cfg)
	defer application.CloseContext(t.Context())
	token := runtimeIdentityFixtureSession(t, "admin", "admin").AccessToken

	store := openRuntimePersistenceFixture(t, cfg)
	audits := auditpersistence.NewAuditStoreFromRuntimeStore(t.Context(), store)
	for index := 1; index <= 5; index++ {
		id := fmt.Sprintf("pagination-%03d", index)
		if err := audits.InsertAuditEvent(t.Context(), "workspace-primary", auditmodel.AuditEvent{
			ID: id, WorkspaceID: "workspace-primary", Event: "pos.checkout.completed", ObjectKey: "pos_order",
			RecordID: id, ActorID: "admin", RoleKey: "admin", Summary: "pagination acceptance",
			CreatedAt: "2026-08-18T08:00:00Z",
		}); err != nil {
			t.Fatalf("seed audit %s: %v", id, err)
		}
	}

	server := httptest.NewServer(auditModuleRoutes(t, application))
	defer server.Close()
	page := requestRuntimeBusinessAuditPage(t, server, token, "")
	assertRuntimeBusinessAuditPage(t, page, []string{"pagination-005", "pagination-004"}, true, true)

	// A newer immutable event arriving between requests must not be repeated or
	// injected behind the first page's descending keyset cursor.
	if err := audits.InsertAuditEvent(t.Context(), "workspace-primary", auditmodel.AuditEvent{
		ID: "pagination-999", WorkspaceID: "workspace-primary", Event: "pos.checkout.completed", ObjectKey: "pos_order",
		RecordID: "pagination-999", ActorID: "admin", RoleKey: "admin", Summary: "arrived after page one",
		CreatedAt: "2026-08-18T08:01:00Z",
	}); err != nil {
		t.Fatalf("insert concurrent audit: %v", err)
	}

	second := requestRuntimeBusinessAuditPage(t, server, token, page.NextCursor)
	assertRuntimeBusinessAuditPage(t, second, []string{"pagination-003", "pagination-002"}, true, true)
	last := requestRuntimeBusinessAuditPage(t, server, token, second.NextCursor)
	assertRuntimeBusinessAuditPage(t, last, []string{"pagination-001"}, false, false)

	request, err := http.NewRequest(http.MethodGet, server.URL+"/business/audit-events?event=pos.checkout.completed&page_size=2&cursor=not-a-cursor", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusBadRequest || !containsJSONCode(body, "backend.audit.cursor_invalid") {
		t.Fatalf("invalid cursor status=%d body=%s", response.StatusCode, body)
	}
}

type runtimeBusinessAuditPage struct {
	Items []struct {
		ID string `json:"id"`
	} `json:"items"`
	Count      int    `json:"count"`
	PageSize   int    `json:"page_size"`
	Truncated  bool   `json:"truncated"`
	NextCursor string `json:"next_cursor"`
}

func requestRuntimeBusinessAuditPage(t *testing.T, server *httptest.Server, token, cursor string) runtimeBusinessAuditPage {
	t.Helper()
	values := url.Values{"event": {"pos.checkout.completed"}, "page_size": {"2"}}
	if cursor != "" {
		values.Set("cursor", cursor)
	}
	request, err := http.NewRequest(http.MethodGet, server.URL+"/business/audit-events?"+values.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("audit page status=%d body=%s", response.StatusCode, body)
	}
	var page runtimeBusinessAuditPage
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	return page
}

func assertRuntimeBusinessAuditPage(t *testing.T, page runtimeBusinessAuditPage, ids []string, truncated, cursorPresent bool) {
	t.Helper()
	if page.PageSize != 2 || page.Count != len(ids) || page.Truncated != truncated || (page.NextCursor != "") != cursorPresent {
		t.Fatalf("page metadata=%#v want count=%d truncated=%v cursor=%v", page, len(ids), truncated, cursorPresent)
	}
	if len(page.Items) != len(ids) {
		t.Fatalf("page ids=%#v want=%#v", page.Items, ids)
	}
	for index, id := range ids {
		if page.Items[index].ID != id {
			t.Fatalf("page item %d=%s want=%s", index, page.Items[index].ID, id)
		}
	}
}

func containsJSONCode(body []byte, code string) bool {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	return payload["code"] == code
}
