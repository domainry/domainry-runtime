package integrationtest

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
	report "github.com/domainry/domainry-report-sdk"
	model "github.com/domainry/domainry-report-sdk/model"
	tools "github.com/domainry/domainry-tools-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

func resultReadRoles(m map[string]any) {
	for _, value := range m["roles"].([]any) {
		role := value.(map[string]any)
		if role["key"] != "headquarters_admin" {
			continue
		}
		role["permissions"] = append(role["permissions"].([]any), map[string]any{"permission_key": report.ActionReportResultsRead, "data_scope": "all"}, map[string]any{"permission_key": tools.ReportQueryDefinitions()[0].ActionKey, "data_scope": "all"})
		for _, key := range []string{"results_reader", "results_denied", "results_narrow"} {
			raw, _ := json.Marshal(role)
			var clone map[string]any
			_ = json.Unmarshal(raw, &clone)
			clone["key"], clone["name"] = key, key
			permissions := []any{}
			for _, value := range clone["permissions"].([]any) {
				p := value.(map[string]any)
				action := p["permission_key"]
				if action == report.ActionReportQueryExecute || action == tools.ReportQueryDefinitions()[0].ActionKey || action == tools.AnalysisDefinitions()[0].ActionKey || key == "results_denied" && action == report.ActionReportResultsRead {
					continue
				}
				if action == "customer.read" {
					if key == "results_narrow" {
						p["data_scope"] = "owner"
					} else {
						p["data_scope"] = "all"
					}
				}
				permissions = append(permissions, p)
			}
			clone["permissions"] = permissions
			m["roles"] = append(m["roles"].([]any), clone)
		}
		for _, value := range role["permissions"].([]any) {
			p := value.(map[string]any)
			if p["permission_key"] == "customer.read" {
				p["data_scope"] = "all"
			}
		}
	}
}

func TestReportAndAnalysisIndependentResultReadThroughRealOwnerRPC(t *testing.T) {
	f := newBusinessWebFixture(t, businessRPCManifest, businessReportManifest, analysisToolsManifest, resultReadRoles)
	b := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	b.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	b.assign("headquarters_admin")
	b.session()
	a := agent.ConversationAuthority{Known: true, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, UserID: "admin"}
	c, server := openAnalysisRPC(t, f, a)
	defer func() { server.Close() }()
	query := model.ReportObjectSQLRequest{ReportKey: "customer_balances", Page: model.ReportPageRequest{PageSize: 1}}
	qr, err := c.QueryReport(t.Context(), query, a)
	if err != nil || qr.Source.ReadProof == "" {
		t.Fatal(qr, err)
	}
	nextQuery := query
	nextQuery.Page.Cursor = qr.Summary.NextCursor
	next, err := c.QueryReport(t.Context(), nextQuery, a)
	if err != nil || len(next.Summary.Rows) != 1 {
		t.Fatal(next, err)
	}
	request := model.AnalysisRequest{DatasetKey: "customer", Mode: "aggregate", Measures: []model.AnalysisMeasure{{Key: "total", Function: "sum", Field: "balance"}, {Key: "rows", Function: "count"}}}
	analysis, err := c.RunAnalysis(t.Context(), request, a)
	if err != nil || analysis.Source.ReadProof == "" {
		t.Fatal(analysis, err)
	}
	catalogRequest := model.ReportCatalogRequest{Page: model.ReportPageRequest{PageSize: 1}}
	catalog, err := c.ReportCatalog(t.Context(), catalogRequest, a)
	if err != nil {
		t.Fatal(err)
	}
	lastCatalogRequest := catalogRequest
	lastCatalogRequest.Page.Cursor = catalog.NextCursor
	lastCatalog, err := c.ReportCatalog(t.Context(), lastCatalogRequest, a)
	if err != nil {
		t.Fatal(err)
	}
	analysisCatalogRequest := model.AnalysisCatalogRequest{DatasetKey: "customer"}
	analysisCatalog, err := c.AnalysisCatalog(t.Context(), analysisCatalogRequest, a)
	if err != nil {
		t.Fatal(err)
	}
	registry := toolmodule.NewRegistry()
	// Both the tool Action and Report operation grant use current Identity.
	authorize := func(ctx context.Context, in tools.Request) (tools.Authorization, error) {
		return c.AuthorizeConversationTool(ctx, in)
	}
	if err := (&toolmodule.ReportAdapter{Source: func() toolmodule.ReportSource { return c }, Authorize: authorize}).Register(registry); err != nil {
		t.Fatal(err)
	}
	if err := (&toolmodule.AnalysisAdapter{Source: func() toolmodule.AnalysisSource { return c }, Authorize: authorize}).Register(registry); err != nil {
		t.Fatal(err)
	}
	selected, err := registry.Select([]string{"report_query", "analysis_run"})
	if err != nil {
		t.Fatal(err)
	}
	requests := []tools.Request{
		{Authority: a, Definition: tools.ReportQueryDefinitions()[0], Call: tools.Call{Name: "report_query", Arguments: `{"operation":"catalog","page_size":1}`}},
		{Authority: a, Definition: tools.ReportQueryDefinitions()[0], Call: tools.Call{Name: "report_query", Arguments: `{"operation":"query","report_key":"customer_balances"}`}},
		{Authority: a, Definition: tools.AnalysisDefinitions()[0], Call: tools.Call{Name: "analysis_run", Arguments: `{"operation":"catalog","dataset_key":"customer"}`}},
		{Authority: a, Definition: tools.AnalysisDefinitions()[0], Call: tools.Call{Name: "analysis_run", Arguments: `{"operation":"run","spec":{"dataset_key":"customer","measures":[{"key":"total","function":"sum","field":"balance"}]}}`}},
	}
	results := []tools.Result{}
	for _, in := range requests {
		result, err := selected.InvokeConversationTool(t.Context(), in)
		if err != nil || result.Status != "completed" {
			t.Fatal(result, err)
		}
		results = append(results, result)
	}
	read := func(want bool) {
		t.Helper()
		checks := []error{
			c.AuthorizeReportResultRead(t.Context(), model.ReportQueryResultAuthorization{Query: query, Result: qr}, a),
			c.AuthorizeReportResultRead(t.Context(), model.ReportQueryResultAuthorization{Query: nextQuery, Result: next}, a),
			c.AuthorizeAnalysisResultRead(t.Context(), model.AnalysisResultAuthorization{Request: request, Result: analysis}, a),
			c.AuthorizeReportCatalogRead(t.Context(), model.ReportCatalogReadAuthorization{Request: catalogRequest, Result: catalog}, a),
			c.AuthorizeReportCatalogRead(t.Context(), model.ReportCatalogReadAuthorization{Request: lastCatalogRequest, Result: lastCatalog}, a),
			c.AuthorizeAnalysisCatalogRead(t.Context(), model.AnalysisCatalogReadAuthorization{Request: analysisCatalogRequest, Result: analysisCatalog}, a),
		}
		for i, err := range checks {
			if (err == nil) != want {
				t.Fatalf("owner read %d want %t: %v", i, want, err)
			}
		}
		for i, in := range requests {
			if err := selected.AuthorizeConversationToolResultRead(t.Context(), in, results[i]); (err == nil) != want {
				t.Fatalf("tool read %d want %t: %v", i, want, err)
			}
		}
	}
	read(true)
	b.assign("results_reader")
	read(true)
	if _, err := c.QueryReport(t.Context(), query, a); err == nil {
		t.Fatal("read grant executed report")
	}
	if _, err := c.RunAnalysis(t.Context(), request, a); err == nil {
		t.Fatal("read grant executed analysis")
	}
	for i, in := range requests {
		if _, err := selected.InvokeConversationTool(t.Context(), in); err == nil {
			t.Fatal("read grant invoked tool")
		}
		if err := selected.AuthorizeConversationToolResult(t.Context(), in, results[i]); err == nil {
			t.Fatal("read grant authorized raw replay")
		}
	}
	server.Close()
	f.close()
	f.open()
	c, server = openAnalysisRPC(t, f, a)
	read(true)
	b.assign("results_denied")
	read(false)
	b.assign("results_narrow")
	// A row-scope change may still permit catalog metadata, but never the old
	// larger query/analysis result even if a current output happens to coincide.
	if err := c.AuthorizeReportResultRead(t.Context(), model.ReportQueryResultAuthorization{Query: query, Result: qr}, a); err == nil {
		t.Fatal("narrow row scope accepted old result")
	}
	if err := c.AuthorizeAnalysisResultRead(t.Context(), model.AnalysisResultAuthorization{Request: request, Result: analysis}, a); err == nil {
		t.Fatal("narrow row scope accepted old analysis")
	}
	b.assign("results_reader")
	read(true)
	changed := qr
	changed.Source.Complete = !changed.Source.Complete
	if err := c.AuthorizeReportResultRead(t.Context(), model.ReportQueryResultAuthorization{Query: query, Result: changed}, a); err == nil {
		t.Fatal("tampered result accepted")
	}
	other := a
	other.WorkspaceID = "other"
	if err := c.AuthorizeReportResultRead(t.Context(), model.ReportQueryResultAuthorization{Query: query, Result: qr}, other); err == nil {
		t.Fatal("cross-workspace result accepted")
	}
	t.Log("actual Report/Runtime/Identity/SQLite and Tools through RPC: query pages and catalog cursors, analysis result/catalog, tool and report execution grants revoked, independent reads survive restart, read grant and row scope revoked, raw replay/invocation/tampered result denied")
}
