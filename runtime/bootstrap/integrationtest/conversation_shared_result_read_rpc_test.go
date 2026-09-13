package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	agent "github.com/domainry/domainry-agent-sdk"
	identityhttp "github.com/domainry/domainry-identity-sdk/httpapi"
	model "github.com/domainry/domainry-report-sdk/model"
	tools "github.com/domainry/domainry-tools-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

func sharedResultUserRoles(m map[string]any) {
	for _, value := range m["roles"].([]any) {
		role := value.(map[string]any)
		if role["key"] != "headquarters_admin" {
			continue
		}
		found := false
		for _, value := range role["permissions"].([]any) {
			found = found || value.(map[string]any)["permission_key"] == "identity.users.create"
		}
		if !found {
			role["permissions"] = append(role["permissions"].([]any), map[string]any{"permission_key": "identity.users.create", "data_scope": "all"})
		}
	}
}

func sharedResultIdentityHandler(f *businessWebFixture) http.Handler {
	mux := http.NewServeMux()
	for _, adapter := range f.identity.(identityhttp.Provider).HTTPAdapters() {
		for _, route := range adapter.Routes() {
			mux.Handle(route.Pattern(), adapter.Handler())
		}
	}
	return mux
}

func createSharedResultProducer(t *testing.T, f *businessWebFixture, admin *businessBrowser) *businessBrowser {
	t.Helper()
	handler := sharedResultIdentityHandler(f)
	response := managedIdentityRequest(t, handler, admin.cookies["domainry_agent_access"].Value, "POST", "/identity/users", map[string]any{"id": "professional_source", "name": "Professional Source", "email": "professional@example.com", "status": "active"}, "shared-result-create-user", http.StatusCreated)
	var credential struct {
		InitialPassword string `json:"initial_password"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &credential); err != nil || credential.InitialPassword == "" {
		t.Fatal("initial credential missing", err)
	}
	assignSharedResultProducer(t, f, admin, "headquarters_admin")
	producer := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	producer.call("POST", "/auth/login", map[string]any{"login": "professional@example.com", "password": credential.InitialPassword}, 200)
	producer.call("POST", "/auth/password/change", map[string]any{"current_password": credential.InitialPassword, "new_password": businessWebPassword}, 200)
	producer.session()
	return producer
}

func assignSharedResultProducer(t *testing.T, f *businessWebFixture, admin *businessBrowser, role string) {
	t.Helper()
	managedIdentityRequest(t, sharedResultIdentityHandler(f), admin.cookies["domainry_agent_access"].Value, "PUT", "/identity/users/professional_source/account-and-roles", map[string]any{"user": map[string]any{"name": "Professional Source", "email": "professional@example.com", "status": "active"}, "assignments": []any{map[string]any{"role_id": role}}}, fmt.Sprintf("shared-result-role-%d", time.Now().UnixNano()), 200)
}

func TestCrossUserReportAndAnalysisReadThroughRealOwnerAndToolRPC(t *testing.T) {
	f := newBusinessWebFixture(t, businessRPCManifest, businessReportManifest, analysisToolsManifest, sharedResultUserRoles, resultReadRoles)
	admin := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	admin.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	admin.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	admin.assign("headquarters_admin")
	admin.session()
	_ = createSharedResultProducer(t, f, admin)
	reader := agent.ConversationAuthority{Known: true, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, UserID: "admin"}
	producer := reader
	producer.UserID = "professional_source"
	c, server := openAnalysisRPC(t, f, reader)
	defer func() { server.Close() }()
	query := model.ReportObjectSQLRequest{ReportKey: "customer_balances", Page: model.ReportPageRequest{PageSize: 1}}
	qr, err := c.QueryReport(t.Context(), query, producer)
	if err != nil || qr.Source.ReadProof == "" || len(qr.Summary.Rows) != 1 {
		t.Fatal("original report", qr, err)
	}
	nextQuery := query
	nextQuery.Page.Cursor = qr.Summary.NextCursor
	next, err := c.QueryReport(t.Context(), nextQuery, producer)
	if err != nil || len(next.Summary.Rows) != 1 {
		t.Fatal("original cursor report", next, err)
	}
	request := model.AnalysisRequest{DatasetKey: "customer", Mode: "aggregate", Measures: []model.AnalysisMeasure{{Key: "total", Function: "sum", Field: "balance"}}}
	analysis, err := c.RunAnalysis(t.Context(), request, producer)
	// The all-data role sees Acme and the restricted customer's 10 each,
	// plus Beta=20 and Gamma=30 from the persisted fixture manifest.
	if err != nil || analysis.Source.ReadProof == "" || len(analysis.Rows) != 1 || analysis.Rows[0].Values["total"] == nil || *analysis.Rows[0].Values["total"] != "70" {
		t.Fatal("original analysis", analysis, err)
	}
	catalogRequest := model.ReportCatalogRequest{Page: model.ReportPageRequest{PageSize: 1}}
	catalog, err := c.ReportCatalog(t.Context(), catalogRequest, producer)
	if err != nil {
		t.Fatal(err)
	}
	analysisCatalogRequest := model.AnalysisCatalogRequest{DatasetKey: "customer"}
	analysisCatalog, err := c.AnalysisCatalog(t.Context(), analysisCatalogRequest, producer)
	if err != nil {
		t.Fatal(err)
	}
	registry := toolmodule.NewRegistry()
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
		{Authority: producer, Definition: tools.ReportQueryDefinitions()[0], Call: tools.Call{Name: "report_query", Arguments: `{"operation":"catalog","page_size":1}`}},
		{Authority: producer, Definition: tools.ReportQueryDefinitions()[0], Call: tools.Call{Name: "report_query", Arguments: `{"operation":"query","report_key":"customer_balances"}`}},
		{Authority: producer, Definition: tools.AnalysisDefinitions()[0], Call: tools.Call{Name: "analysis_run", Arguments: `{"operation":"catalog","dataset_key":"customer"}`}},
		{Authority: producer, Definition: tools.AnalysisDefinitions()[0], Call: tools.Call{Name: "analysis_run", Arguments: `{"operation":"run","spec":{"dataset_key":"customer","measures":[{"key":"total","function":"sum","field":"balance"}]}}`}},
	}
	results := make([]tools.Result, len(requests))
	for i, in := range requests {
		results[i], err = selected.InvokeConversationTool(t.Context(), in)
		if err != nil || results[i].Status != "completed" {
			t.Fatal("original tool", i, results[i], err)
		}
		requests[i].Authority = reader
		requests[i].ResultProducer = &producer
	}
	read := func(want bool) {
		t.Helper()
		checks := []error{
			c.AuthorizeSharedReportResultRead(t.Context(), model.ReportQueryResultAuthorization{Query: query, Result: qr}, reader, producer),
			c.AuthorizeSharedReportResultRead(t.Context(), model.ReportQueryResultAuthorization{Query: nextQuery, Result: next}, reader, producer),
			c.AuthorizeSharedAnalysisResultRead(t.Context(), model.AnalysisResultAuthorization{Request: request, Result: analysis}, reader, producer),
			c.AuthorizeSharedReportCatalogRead(t.Context(), model.ReportCatalogReadAuthorization{Request: catalogRequest, Result: catalog}, reader, producer),
			c.AuthorizeSharedAnalysisCatalogRead(t.Context(), model.AnalysisCatalogReadAuthorization{Request: analysisCatalogRequest, Result: analysisCatalog}, reader, producer),
		}
		for i, err := range checks {
			if (err == nil) != want {
				t.Fatalf("shared owner read %d want %t: %v", i, want, err)
			}
		}
		for i, in := range requests {
			if err := selected.AuthorizeConversationToolResultRead(t.Context(), in, results[i]); (err == nil) != want {
				t.Fatalf("shared tool read %d want %t: %v", i, want, err)
			}
		}
	}
	read(true)
	admin.assign("results_reader")
	read(true)
	if _, err := c.QueryReport(t.Context(), query, reader); err == nil {
		t.Fatal("reader executed report")
	}
	if _, err := c.RunAnalysis(t.Context(), request, reader); err == nil {
		t.Fatal("reader executed analysis")
	}
	if err := c.AuthorizeReportResultRead(t.Context(), model.ReportQueryResultAuthorization{Query: query, Result: qr}, reader); err == nil {
		t.Fatal("ordinary read accepted foreign proof")
	}
	if err := c.AuthorizeAnalysisResultRead(t.Context(), model.AnalysisResultAuthorization{Request: request, Result: analysis}, reader); err == nil {
		t.Fatal("ordinary read accepted foreign analysis")
	}
	for i, in := range requests {
		if err := selected.AuthorizeConversationToolResult(t.Context(), in, results[i]); err == nil {
			t.Fatalf("reader replayed original tool %d", i)
		}
	}
	for _, role := range []string{"results_denied", "results_field_denied", "results_data_denied"} {
		admin.assign(role)
		read(false)
	}
	admin.assign("results_narrow")
	if err := c.AuthorizeSharedReportResultRead(t.Context(), model.ReportQueryResultAuthorization{Query: query, Result: qr}, reader, producer); err == nil {
		t.Fatal("narrow reader obtained all-row report")
	}
	if err := c.AuthorizeSharedAnalysisResultRead(t.Context(), model.AnalysisResultAuthorization{Request: request, Result: analysis}, reader, producer); err == nil {
		t.Fatal("narrow reader obtained all-row analysis")
	}
	admin.assign("results_reader")
	assignSharedResultProducer(t, f, admin, "results_reader")
	read(true)
	if _, err := c.QueryReport(t.Context(), query, producer); err == nil {
		t.Fatal("revoked producer executed query")
	}
	assignSharedResultProducer(t, f, admin, "results_data_denied")
	if err := c.AuthorizeSharedReportResultRead(t.Context(), model.ReportQueryResultAuthorization{Query: query, Result: qr}, reader, producer); err == nil {
		t.Fatal("producer's revoked source access retained original rows")
	}
	if err := c.AuthorizeSharedAnalysisResultRead(t.Context(), model.AnalysisResultAuthorization{Request: request, Result: analysis}, reader, producer); err == nil {
		t.Fatal("producer's revoked source access retained original aggregate")
	}
	assignSharedResultProducer(t, f, admin, "results_reader")
	read(true)
	altered := analysis
	altered.Source.ReadProof = "forged"
	if err := c.AuthorizeSharedAnalysisResultRead(t.Context(), model.AnalysisResultAuthorization{Request: request, Result: altered}, reader, producer); err == nil {
		t.Fatal("altered original result accepted")
	}
	wrong := producer
	wrong.UserID = reader.UserID
	if err := c.AuthorizeSharedReportResultRead(t.Context(), model.ReportQueryResultAuthorization{Query: query, Result: qr}, reader, wrong); err == nil {
		t.Fatal("false producer accepted")
	}
	wrong.WorkspaceID = "foreign"
	if err := c.AuthorizeSharedReportResultRead(t.Context(), model.ReportQueryResultAuthorization{Query: query, Result: qr}, reader, wrong); err == nil {
		t.Fatal("foreign producer accepted")
	}
	server.Close()
	f.close()
	f.open()
	c, server = openAnalysisRPC(t, f, reader)
	read(true)
	admin.assign("headquarters_admin")
	admin.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": businessWebPassword}, 200)
	admin.session()
	records, err := c.QueryBusinessRecords(t.Context(), agent.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name"}, PageSize: 1, Sort: []agent.ConversationBusinessSort{{Field: "name", Direction: "asc"}}}, reader)
	if err != nil || len(records.Items) != 1 {
		t.Fatal(records, err)
	}
	routes := f.runtime.Routes()
	scoped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Workspace-ID", f.cfg.IdentityWorkspaceID)
		routes.ServeHTTP(w, r)
	})
	managedIdentityRequest(t, scoped, admin.cookies["domainry_agent_access"].Value, http.MethodPost, "/records/customer/items/"+records.Items[0].ID+"/actions/customer.rename", map[string]any{"data": map[string]any{"name": "Updated Cross-user Customer", "expected_updated_at": records.Items[0].Version}}, "shared-result-source-change", http.StatusOK)
	admin.assign("results_reader")
	if err := c.AuthorizeSharedReportResultRead(t.Context(), model.ReportQueryResultAuthorization{Query: query, Result: qr}, reader, producer); err == nil {
		t.Fatal("changed source retained foreign report")
	}
	if err := c.AuthorizeSharedAnalysisResultRead(t.Context(), model.AnalysisResultAuthorization{Request: request, Result: analysis}, reader, producer); err == nil {
		t.Fatal("changed source retained foreign aggregate")
	}
}
