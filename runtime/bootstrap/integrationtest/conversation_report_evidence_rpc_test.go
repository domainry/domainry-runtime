package integrationtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/businessrpc"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	bootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap"
)

func TestConversationGovernedReportRPCUsesRealOwnerAndCurrentData(t *testing.T) {
	f := newBusinessWebFixture(t, businessRPCManifest, businessReportManifest)
	b := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	b.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	b.assign("headquarters_admin")
	b.session()
	a := agent.ConversationAuthority{Known: true, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, UserID: "admin"}
	open := func() (*businessrpc.Client, *httptest.Server) {
		t.Helper()
		source, err := bootstrap.ConversationBusinessSource(f.runtime)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := source.(businessrpc.GovernedReportReader); !ok {
			t.Fatal("Report evidence port absent")
		}
		h, err := bootstrap.ConversationBusinessHandler(f.runtime, "governed-report-test-service-token")
		if err != nil {
			t.Fatal(err)
		}
		s := httptest.NewServer(h)
		c, err := businessrpc.Open(t.Context(), businessrpc.ClientOptions{BaseURL: s.URL, Token: "governed-report-test-service-token", Scope: businessrpc.Scope{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, ApplicationKey: f.cfg.IdentityAudience, IdentityIssuer: f.identity.Descriptor().Issuer}, ExpectedSourceIdentity: source.BusinessSourceIdentity(), ExpectedContractSHA256: businessrpc.ContractSHA256()})
		if err != nil {
			s.Close()
			t.Fatal(err)
		}
		return c, s
	}
	c, server := open()
	defer func() { server.Close() }()
	catalog, err := c.ReportCatalog(t.Context(), reportmodel.ReportCatalogRequest{Page: reportmodel.ReportPageRequest{PageSize: 1}}, a)
	if err != nil || len(catalog.Reports) != 1 || catalog.Reports[0].Key != "customer_balances" || !catalog.Truncated {
		t.Fatal(catalog, err)
	}
	lastCatalog, err := c.ReportCatalog(t.Context(), reportmodel.ReportCatalogRequest{Page: reportmodel.ReportPageRequest{PageSize: 1, Cursor: catalog.NextCursor}}, a)
	if err != nil || len(lastCatalog.Reports) != 1 || lastCatalog.Reports[0].Key != "customer_names" || lastCatalog.Truncated {
		t.Fatal(lastCatalog, err)
	}
	raw, _ := json.Marshal(catalog)
	if strings.Contains(string(raw), "SELECT") || strings.Contains(string(raw), "required_permissions") {
		t.Fatal("catalog leaked owner definition internals")
	}
	query := reportmodel.ReportObjectSQLRequest{ReportKey: "customer_names", Page: reportmodel.ReportPageRequest{PageSize: 1}}
	first, err := c.QueryReport(t.Context(), query, a)
	if err != nil || len(first.Summary.Rows) != 1 || first.Summary.Rows[0].Dimensions["name"] != "Acme" || first.Source.Complete || first.Source.Proof == "" {
		t.Fatal(first, err)
	}
	if first.Source.DefinitionVersion != lastCatalog.Reports[0].DefinitionVersion || first.Source.RowLimit != 10 {
		t.Fatal("query/catalog source mismatch")
	}
	authorize := func(result reportmodel.ReportQueryResult) error {
		return c.AuthorizeReportResult(t.Context(), reportmodel.ReportQueryResultAuthorization{Query: query, Result: result}, a)
	}
	if err := authorize(first); err != nil {
		t.Fatal("fresh result", err)
	}
	copyResult := first
	copyResult.Source.Complete = true
	if err := authorize(copyResult); err == nil {
		t.Fatal("tampered completeness accepted")
	}
	all, err := c.QueryReport(t.Context(), reportmodel.ReportObjectSQLRequest{ReportKey: "customer_names"}, a)
	if err != nil || !all.Source.Complete || len(all.Summary.Rows) != 3 {
		t.Fatal("whole report", all, err)
	}
	// Reopen the real owner and Identity against the same on-disk database.
	server.Close()
	f.close()
	f.open()
	c, server = open()
	if err := authorize(first); err != nil {
		t.Fatal("persisted result after full restart", err)
	}
	for _, role := range []string{"business_field_restricted", "business_restricted"} {
		b.assign(role)
		visible, err := c.ReportCatalog(t.Context(), reportmodel.ReportCatalogRequest{}, a)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range visible.Reports {
			if item.Key == "customer_balances" || role == "business_restricted" {
				t.Fatal("revoked report exposed in catalog", visible)
			}
		}
		if _, err := c.QueryReport(t.Context(), reportmodel.ReportObjectSQLRequest{ReportKey: "customer_balances"}, a); err == nil {
			t.Fatal("revoked fields queried")
		}
		if err := authorize(first); err == nil {
			t.Fatal("authorization revision did not invalidate evidence")
		}
	}
	b.assign("headquarters_admin")
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": businessWebPassword}, 200)
	b.session()
	first, err = c.QueryReport(t.Context(), query, a)
	if err != nil {
		t.Fatal(err)
	}
	records, err := c.QueryBusinessRecords(t.Context(), agent.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name"}, PageSize: 1, Sort: []agent.ConversationBusinessSort{{Field: "name", Direction: "asc"}}}, a)
	if err != nil || len(records.Items) != 1 {
		t.Fatal(records, err)
	}
	routes := f.runtime.Routes()
	scoped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Workspace-ID", f.cfg.IdentityWorkspaceID)
		routes.ServeHTTP(w, r)
	})
	managedIdentityRequest(t, scoped, b.cookies["domainry_agent_access"].Value, http.MethodPost, "/records/customer/items/"+records.Items[0].ID+"/actions/customer.rename", map[string]any{"data": map[string]any{"name": "Updated Acme", "expected_updated_at": records.Items[0].Version}}, "report-source-change", http.StatusOK)
	if err := authorize(first); err == nil {
		t.Fatal("changed actual source accepted old evidence")
	}
	if _, err := c.QueryReport(t.Context(), reportmodel.ReportObjectSQLRequest{ReportKey: query.ReportKey, Page: reportmodel.ReportPageRequest{Cursor: first.Summary.NextCursor, PageSize: 1}}, a); err == nil {
		t.Fatal("changed source accepted old cursor")
	}
	current, err := c.QueryReport(t.Context(), reportmodel.ReportObjectSQLRequest{ReportKey: query.ReportKey}, a)
	if err != nil || current.Source.DataVersion == first.Source.DataVersion {
		t.Fatal("new source was not observed", current, err)
	}
	for _, row := range current.Summary.Rows {
		if row.Dimensions["name"] == "Acme" {
			t.Fatal("new query reused old rows")
		}
	}
	other := a
	other.WorkspaceID = "other-workspace"
	if _, err := c.ReportCatalog(t.Context(), reportmodel.ReportCatalogRequest{}, other); err == nil {
		t.Fatal("cross-workspace catalog accepted")
	}
	if err := c.AuthorizeReportResult(t.Context(), reportmodel.ReportQueryResultAuthorization{Query: query, Result: first}, other); err == nil {
		t.Fatal("cross-workspace source accepted")
	}
	t.Log("actual Report/Runtime/Identity/SQLite via public service: authorized catalog/pagination, real rows, complete versus page, signed result, field/read revocation, current data mutation invalidates evidence and cursor, full owner restart; no model or browser asserted")
}

func TestConversationGovernedReportRespectsOwnerDataScope(t *testing.T) {
	f := newBusinessWebFixture(t, businessRPCManifest, businessReportManifest, func(m map[string]any) {
		for _, value := range m["roles"].([]any) {
			role := value.(map[string]any)
			if role["key"] != "headquarters_admin" {
				continue
			}
			raw, _ := json.Marshal(role)
			var own map[string]any
			if err := json.Unmarshal(raw, &own); err != nil {
				t.Fatal(err)
			}
			own["key"], own["name"] = "report_owner_only", "Report owner scope"
			for _, value := range own["permissions"].([]any) {
				p := value.(map[string]any)
				if p["permission_key"] == "customer.read" {
					p["data_scope"] = "owner"
				}
			}
			// The shared fixture's headquarters role also uses owner scope.
			// Give the comparison role an explicit all-row permission here.
			for _, value := range role["permissions"].([]any) {
				p := value.(map[string]any)
				if p["permission_key"] == "customer.read" {
					p["data_scope"] = "all"
				}
			}
			m["roles"] = append(m["roles"].([]any), own)
			break
		}
		m["seed_records"] = append(m["seed_records"].([]any), map[string]any{"object_key": "customer", "owner_user_id": "restricted_user", "data": map[string]any{"__seed_key": "private_other_report", "name": "Private Other", "owner": "restricted_user", "balance": 40}})
	})
	b := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	b.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	b.assign("report_owner_only")
	b.session()
	source, err := bootstrap.ConversationBusinessSource(f.runtime)
	if err != nil {
		t.Fatal(err)
	}
	reader, ok := source.(businessrpc.GovernedReportReader)
	if !ok {
		t.Fatal("governed reader unavailable")
	}
	a := agent.ConversationAuthority{Known: true, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, UserID: "admin"}
	query := reportmodel.ReportObjectSQLRequest{ReportKey: "customer_names"}
	limited, err := reader.QueryReport(t.Context(), query, a)
	if err != nil || !limited.Source.Complete || len(limited.Summary.Rows) != 3 || limited.Summary.Total != 3 {
		t.Fatal(limited, err)
	}
	for _, row := range limited.Summary.Rows {
		if row.Dimensions["name"] == "Private Other" || row.Dimensions["name"] == "Other" {
			t.Fatal("report bypassed owner RLS")
		}
	}
	if err := reader.AuthorizeReportResult(t.Context(), reportmodel.ReportQueryResultAuthorization{Query: query, Result: limited}, a); err != nil {
		t.Fatal(err)
	}
	b.assign("headquarters_admin")
	all, err := reader.QueryReport(t.Context(), query, a)
	if err != nil || len(all.Summary.Rows) != 5 || all.Summary.Total != 5 {
		t.Fatal(all, err)
	}
	if err := reader.AuthorizeReportResult(t.Context(), reportmodel.ReportQueryResultAuthorization{Query: query, Result: limited}, a); err == nil {
		t.Fatal("result escaped its original authorization scope")
	}
	t.Log("real Identity owner-scope permission, Record RLS, Report query and source validation: 3 owned versus 5 all rows (including the base fixture's Other record)")
}
