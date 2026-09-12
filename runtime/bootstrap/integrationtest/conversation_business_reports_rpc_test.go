package integrationtest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/businessrpc"
	report "github.com/domainry/domainry-report-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	bootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap"
)

func businessReportManifest(m map[string]any) {
	names := managedIdentityFieldReport("customer_names", "name")
	names["object_sql_v1"].(map[string]any)["sql"] = "SELECT c.name AS name FROM customer AS c ORDER BY c.name LIMIT 10"
	balance := managedIdentityFieldReport("customer_balances", "balance")
	balance["object_sql_v1"].(map[string]any)["sql"] = "SELECT c.balance AS balance FROM customer AS c ORDER BY c.name LIMIT 10"
	balance["object_sql_v1"].(map[string]any)["result_schema"] = []any{map[string]any{"key": "balance", "kind": "dimension", "type": "integer"}}
	m["reports"] = []any{names, balance}
	for _, raw := range m["roles"].([]any) {
		role := raw.(map[string]any)
		if role["key"] == "headquarters_admin" || role["key"] == "business_field_restricted" || role["key"] == "business_restricted" {
			seen := map[string]bool{}
			for _, value := range role["permissions"].([]any) {
				seen[value.(map[string]any)["permission_key"].(string)] = true
			}
			for _, key := range []string{report.ActionReportSummaryGet, report.ActionReportQueryExecute} {
				if !seen[key] {
					role["permissions"] = append(role["permissions"].([]any), map[string]any{"permission_key": key, "data_scope": "all"})
				}
			}
		}
	}
}

func TestConversationReportRPCUsesRealReportOwner(t *testing.T) {
	f := newBusinessWebFixture(t, businessRPCManifest, businessReportManifest)
	b := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	b.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	b.assign("headquarters_admin")
	b.session()
	a := agent.ConversationAuthority{Known: true, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, UserID: "admin"}
	open := func() (*businessrpc.Client, *httptest.Server) {
		source, err := bootstrap.ConversationBusinessSource(f.runtime)
		if err != nil {
			t.Fatal(err)
		}
		local, ok := source.(businessrpc.ReportReader)
		if !ok {
			t.Fatal("Report host port absent")
		}
		result, err := local.ReportSummary(t.Context(), reportmodel.ReportSummaryRequest{ReportKey: "customer_names", Page: reportmodel.ReportPageRequest{PageSize: 1}}, a)
		if err != nil || len(result.Rows) != 1 || result.Rows[0].Dimensions["name"] != "Acme" {
			t.Fatal("actual local Report owner", result, err)
		}
		h, err := bootstrap.ConversationBusinessHandler(f.runtime, "report-host-private-test-service-token")
		if err != nil {
			t.Fatal(err)
		}
		s := httptest.NewServer(h)
		c, err := businessrpc.Open(t.Context(), businessrpc.ClientOptions{BaseURL: s.URL, Token: "report-host-private-test-service-token", Scope: businessrpc.Scope{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, ApplicationKey: f.cfg.IdentityAudience, IdentityIssuer: f.identity.Descriptor().Issuer}, ExpectedSourceIdentity: source.BusinessSourceIdentity(), ExpectedContractSHA256: businessrpc.ContractSHA256()})
		if err != nil {
			s.Close()
			t.Fatal(err)
		}
		return c, s
	}
	c, server := open()
	defer func() { server.Close() }()
	first, err := c.ReportSummary(t.Context(), reportmodel.ReportSummaryRequest{ReportKey: "customer_names", Page: reportmodel.ReportPageRequest{PageSize: 1}}, a)
	if err != nil || len(first.Rows) != 1 || first.NextCursor == "" || first.Rows[0].Dimensions["name"] != "Acme" {
		t.Fatal(first, err)
	}
	second, err := c.ReportSummary(t.Context(), reportmodel.ReportSummaryRequest{ReportKey: "customer_names", Page: reportmodel.ReportPageRequest{PageSize: 1, Cursor: first.NextCursor}}, a)
	if err != nil || len(second.Rows) != 1 || second.Rows[0].Dimensions["name"] != "Beta" {
		t.Fatal(second, err)
	}
	readBalance := func() (reportmodel.ReportSummary, error) {
		return c.ReportObjectSQL(t.Context(), reportmodel.ReportObjectSQLRequest{ReportKey: "customer_balances", Page: reportmodel.ReportPageRequest{PageSize: 1}}, a)
	}
	if result, err := readBalance(); err != nil || len(result.Rows) != 1 {
		t.Fatal(result, err)
	}
	if _, err := c.ReportSummary(t.Context(), reportmodel.ReportSummaryRequest{ReportKey: "customer_balances", Page: reportmodel.ReportPageRequest{PageSize: 1, Cursor: first.NextCursor}}, a); err == nil {
		t.Fatal("cross-report cursor accepted")
	}
	b.assign("business_field_restricted")
	if _, err := readBalance(); err == nil {
		t.Fatal("Report ignored current field revocation")
	}
	b.assign("business_restricted")
	if _, err := c.ReportSummary(t.Context(), reportmodel.ReportSummaryRequest{ReportKey: "customer_names"}, a); err == nil {
		t.Fatal("Report ignored current read revocation")
	}
	b.assign("headquarters_admin")
	if _, err := c.ReportSummary(t.Context(), reportmodel.ReportSummaryRequest{ReportKey: "customer_names", Parameters: map[string]any{"sql": "SELECT unauthorized"}}, a); err == nil {
		t.Fatal("undeclared Report parameter accepted")
	}
	other := a
	other.WorkspaceID = "other"
	if _, err := c.ReportSummary(t.Context(), reportmodel.ReportSummaryRequest{ReportKey: "customer_names"}, other); err == nil {
		t.Fatal("cross workspace accepted")
	}
	server.Close()
	f.close()
	f.open()
	c, server = open()
	if result, err := readBalance(); err != nil || len(result.Rows) != 1 {
		t.Fatal("Report owner after restart", result, err)
	}
	t.Log("real Report v0.1.7 / SDK v0.1.6, Runtime, Identity and SQLite: local and service summary/ObjectSQL, stable pages, cursor scope, field/read revocation, undeclared input, cross-workspace rejection and complete restart")
}
