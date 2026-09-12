package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/businessrpc"
	model "github.com/domainry/domainry-report-sdk/model"
	bootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap"
	toolsdk "github.com/domainry/domainry-tools-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

func analysisToolsManifest(m map[string]any) {
	for _, value := range m["roles"].([]any) {
		role := value.(map[string]any)
		if role["key"] == "headquarters_admin" {
			raw, _ := json.Marshal(role)
			var revoked map[string]any
			_ = json.Unmarshal(raw, &revoked)
			revoked["key"], revoked["name"] = "analysis_tool_revoked", "Analysis tool revoked"
			m["roles"] = append(m["roles"].([]any), revoked)
		}
		if role["key"] == "headquarters_admin" || role["key"] == "business_field_restricted" || role["key"] == "business_restricted" {
			role["permissions"] = append(role["permissions"].([]any), map[string]any{"permission_key": toolsdk.AnalysisDefinitions()[0].ActionKey, "data_scope": "owner"})
		}
	}
}

func openAnalysisRPC(t *testing.T, f *businessWebFixture, a agent.ConversationAuthority) (*businessrpc.Client, *httptest.Server) {
	t.Helper()
	source, err := bootstrap.ConversationBusinessSource(f.runtime)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := source.(businessrpc.AnalysisReader); !ok {
		t.Fatal("analysis owner port absent")
	}
	const token = "analysis-private-service-token-fixture"
	h, err := bootstrap.ConversationBusinessHandler(f.runtime, token)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(h)
	c, err := businessrpc.Open(t.Context(), businessrpc.ClientOptions{BaseURL: s.URL, Token: token, Scope: businessrpc.Scope{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, ApplicationKey: f.cfg.IdentityAudience, IdentityIssuer: f.identity.Descriptor().Issuer}, ExpectedSourceIdentity: source.BusinessSourceIdentity(), ExpectedContractSHA256: businessrpc.ContractSHA256()})
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	return c, s
}

func TestAnalysisToolRPCWithRealOwnerIdentityAndRestart(t *testing.T) {
	f := newBusinessWebFixture(t, businessRPCManifest, businessReportManifest, analysisToolsManifest, func(m map[string]any) {
		for _, value := range m["seed_records"].([]any) {
			seed := value.(map[string]any)
			data := seed["data"].(map[string]any)
			if seed["object_key"] == "customer" && data["name"] == "Acme" {
				data["balance"] = json.Number("9007199254740993")
			}
		}
		m["seed_records"] = append(m["seed_records"].([]any), map[string]any{"object_key": "customer", "owner_user_id": "admin", "data": map[string]any{"__seed_key": "analysis_null", "name": "Null Balance", "owner": "admin"}})
	})
	b := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	b.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	b.assign("headquarters_admin")
	b.session()
	a := agent.ConversationAuthority{Known: true, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, UserID: "admin"}
	c, server := openAnalysisRPC(t, f, a)
	defer func() { server.Close() }()
	adapter := &toolmodule.AnalysisAdapter{Source: func() toolmodule.AnalysisSource { return c }, Authorize: func(ctx context.Context, r toolsdk.Request) (toolsdk.Authorization, error) {
		return c.AuthorizeConversationTool(ctx, r)
	}}
	registry := toolmodule.NewRegistry()
	if err := adapter.Register(registry); err != nil {
		t.Fatal(err)
	}
	selected, err := registry.Select([]string{toolsdk.AnalysisRunToolKey})
	if err != nil {
		t.Fatal(err)
	}
	defs, err := selected.ConversationTools(t.Context(), a)
	if err != nil || len(defs) != 1 {
		t.Fatal("tool discovery", defs, err)
	}
	r := toolsdk.Request{Authority: a, Definition: toolsdk.AnalysisDefinitions()[0], Call: toolsdk.Call{Name: toolsdk.AnalysisRunToolKey, ID: "analysis-real-1", Arguments: `{"operation":"catalog","dataset_key":"customer"}`}}
	catalog, err := selected.InvokeConversationTool(t.Context(), r)
	if err != nil || strings.Contains(string(catalog.Content), "SELECT") || strings.Contains(string(catalog.Content), "required_permissions") {
		t.Fatal("catalog", err)
	}
	request := model.AnalysisRequest{DatasetKey: "customer", Mode: "aggregate", Measures: []model.AnalysisMeasure{{Key: "total", Field: "balance", Function: "sum"}, {Key: "mean", Field: "balance", Function: "avg"}, {Key: "rows", Function: "count"}}}
	raw, _ := json.Marshal(map[string]any{"operation": "run", "spec": request})
	r.Call.Arguments = string(raw)
	run := func() toolsdk.Result {
		t.Helper()
		result, err := selected.InvokeConversationTool(t.Context(), r)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	result := run()
	var envelope struct {
		Result model.AnalysisResult `json:"result"`
	}
	if err := json.Unmarshal(result.Content, &envelope); err != nil {
		t.Fatal(err)
	}
	data := envelope.Result
	if !data.Source.Complete || data.Source.Proof == "" || data.Source.InputCounts["dataset"] != "4" || len(data.Rows) != 1 {
		t.Fatal("wrong full authorized input", data)
	}
	row := data.Rows[0]
	if row.Values["total"] == nil || *row.Values["total"] != "9007199254741043" || row.NonNullCounts["mean"] != "3" || *row.Values["rows"] != "4" {
		t.Fatal("large integer, NULL denominator or owner scope lost", string(result.Content))
	}
	if err := selected.AuthorizeConversationToolResult(t.Context(), r, result); err != nil {
		t.Fatal(err)
	}
	limited := request
	limited.GroupBy = []string{"name"}
	limited.MaxRows = 1
	_, err = c.RunAnalysis(t.Context(), limited, a)
	var coded *agent.Error
	if !errors.As(err, &coded) || coded.Code != "backend.report.analysis.result_limit_exceeded" {
		t.Fatal("full result limit lost across owner/RPC", err)
	}
	server.Close()
	f.close()
	f.open()
	c, server = openAnalysisRPC(t, f, a)
	if err := selected.AuthorizeConversationToolResult(t.Context(), r, result); err != nil {
		t.Fatal("full owner/identity restart lost proof", err)
	}
	for _, role := range []string{"analysis_tool_revoked", "business_field_restricted", "business_restricted"} {
		b.assign(role)
		if _, err := selected.InvokeConversationTool(t.Context(), r); err == nil {
			t.Fatal("revoked execution", role)
		}
		if err := selected.AuthorizeConversationToolResult(t.Context(), r, result); err == nil {
			t.Fatal("revoked saved result", role)
		}
		if role == "business_field_restricted" {
			visible, err := c.AnalysisCatalog(t.Context(), model.AnalysisCatalogRequest{DatasetKey: "customer"}, a)
			if err != nil {
				t.Fatal(err)
			}
			for _, dataset := range visible.Datasets {
				for _, column := range dataset.Columns {
					if column.Key == "balance" {
						t.Fatal("revoked field leaked")
					}
				}
			}
		}
	}
	b.assign("headquarters_admin")
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": businessWebPassword}, 200)
	b.session()
	result = run()
	_ = json.Unmarshal(result.Content, &envelope)
	records, err := c.QueryBusinessRecords(t.Context(), agent.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name"}, PageSize: 1, Sort: []agent.ConversationBusinessSort{{Field: "name", Direction: "asc"}}}, a)
	if err != nil || len(records.Items) != 1 {
		t.Fatal(records, err)
	}
	routes := f.runtime.Routes()
	scoped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Workspace-ID", f.cfg.IdentityWorkspaceID)
		routes.ServeHTTP(w, r)
	})
	managedIdentityRequest(t, scoped, b.cookies["domainry_agent_access"].Value, http.MethodPost, "/records/customer/items/"+records.Items[0].ID+"/actions/customer.rename", map[string]any{"data": map[string]any{"name": "Updated Analysis Acme", "expected_updated_at": records.Items[0].Version}}, "analysis-source-change", http.StatusOK)
	// Source fingerprints include record modification times as well as selected
	// values, so an actual business update invalidates the saved proof.
	if err := selected.AuthorizeConversationToolResult(t.Context(), r, result); err == nil {
		t.Fatal("source record update did not invalidate original proof")
	}
	fresh, err := c.RunAnalysis(t.Context(), request, a)
	if err != nil || fresh.Source.DataVersion == envelope.Result.Source.DataVersion {
		t.Fatal("current source version not observed", err)
	}
	other := a
	other.WorkspaceID = "other-workspace"
	if _, err := c.RunAnalysis(t.Context(), request, other); err == nil {
		t.Fatal("cross workspace accepted")
	}
	if err := c.AuthorizeAnalysisResult(t.Context(), model.AnalysisResultAuthorization{Request: request, Result: fresh}, other); err == nil {
		t.Fatal("cross workspace proof accepted")
	}
	t.Log("real Tools registry / SDK HTTP / Runtime / Report / Identity / SQLite: complete owner-scoped input, exact large integer and NULL denominator, result-limit error, current tool/field/read revocation, disk restart, source mutation and cross-workspace rejection")
}
