package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agent "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/businessrpc"
	agentmodule "github.com/domainry/domainry-agent/module"
	agentweb "github.com/domainry/domainry-agent/web"
	"github.com/domainry/domainry-agent/webhost"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	bootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

func reportToolsManifest(m map[string]any) {
	for _, value := range m["roles"].([]any) {
		role := value.(map[string]any)
		if role["key"] == "headquarters_admin" || role["key"] == "business_field_restricted" || role["key"] == "business_restricted" {
			role["permissions"] = append(role["permissions"].([]any), map[string]any{"permission_key": toolsdk.ReportQueryDefinitions()[0].ActionKey, "data_scope": "owner"})
		}
	}
}

type reportToolsModel struct{ businessWebModel }

func (reportToolsModel) ConversationModelIdentity() agent.ConversationModelIdentity {
	return agent.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "report-tools", Fingerprint: "report-tools-v1"}
}
func (reportToolsModel) StreamConversationStep(_ context.Context, in agent.ConversationStepRequest, emit func(agent.ConversationModelEvent) error) (agent.ConversationStepResult, error) {
	answer := func(text string) (agent.ConversationStepResult, error) {
		if err := emit(agent.ConversationModelEvent{Type: "text.delta", Delta: text}); err != nil {
			return agent.ConversationStepResult{}, err
		}
		return agent.ConversationStepResult{Message: agent.ConversationStepMessage{Role: "assistant", Content: text}, FinishReason: "stop"}, nil
	}
	available := false
	for _, d := range in.Tools {
		available = available || d.Key == toolsdk.ReportQueryToolKey
	}
	if !available {
		return answer("当前没有可用报表工具，未执行查询。")
	}
	call := func(id string, args any) (agent.ConversationStepResult, error) {
		raw, _ := json.Marshal(args)
		return agent.ConversationStepResult{Message: agent.ConversationStepMessage{Role: "assistant", ToolCalls: []agent.ConversationToolCall{{ID: id, Name: toolsdk.ReportQueryToolKey, Arguments: string(raw)}}}, FinishReason: "tool_calls"}, nil
	}
	last := in.Messages[len(in.Messages)-1]
	if last.Role != "tool" {
		return call("report-catalog-1", map[string]any{"operation": "catalog", "page_size": 1})
	}
	var latest struct {
		Operation string                         `json:"operation"`
		Catalog   *reportmodel.ReportCatalog     `json:"catalog"`
		Result    *reportmodel.ReportQueryResult `json:"result"`
	}
	rows := []string{}
	count := 0
	for _, message := range in.Messages {
		if message.Role == "user" {
			rows = nil
			count = 0
			continue
		}
		if message.Role != "tool" {
			continue
		}
		var result toolsdk.Result
		latest.Catalog, latest.Result = nil, nil
		if json.Unmarshal([]byte(message.Content), &result) != nil || result.Status != "completed" || json.Unmarshal(result.Content, &latest) != nil {
			return agent.ConversationStepResult{}, fmt.Errorf("report tool did not return completed owner data: %s", message.Content)
		}
		if latest.Result != nil {
			if latest.Result.Source.Proof == "" || latest.Result.Source.Complete {
				return agent.ConversationStepResult{}, fmt.Errorf("paged report lost source or overstated completeness")
			}
			for _, row := range latest.Result.Summary.Rows {
				rows = append(rows, row.Dimensions["name"])
			}
			count++
		}
	}
	if latest.Operation == "catalog" {
		if latest.Catalog == nil {
			return agent.ConversationStepResult{}, fmt.Errorf("catalog missing")
		}
		for _, item := range latest.Catalog.Reports {
			if item.Key == "customer_names" {
				return call("report-page-1", map[string]any{"operation": "query", "report_key": item.Key, "page_size": 1})
			}
		}
		if latest.Catalog.NextCursor != "" {
			return call("report-catalog-2", map[string]any{"operation": "catalog", "cursor": latest.Catalog.NextCursor, "page_size": 1})
		}
		return answer("当前目录中没有可查询的客户名称报表。")
	}
	if latest.Result == nil {
		return agent.ConversationStepResult{}, fmt.Errorf("query result missing")
	}
	if latest.Result.Summary.NextCursor != "" {
		return call(fmt.Sprintf("report-page-%d", count+1), map[string]any{"operation": "query", "report_key": latest.Result.Source.ReportKey, "cursor": latest.Result.Summary.NextCursor, "page_size": 1})
	}
	return answer("已逐页查询客户名称报表：" + strings.Join(rows, "、") + "。来源：" + latest.Result.Source.ReportKey + "；固定上限 " + fmt.Sprint(latest.Result.Source.RowLimit) + " 行。每页自身不等于完整报表，发布报表结果不代表全部原始记录。")
}

func prepareReportToolsFixture(t *testing.T) *businessWebFixture {
	f := prepareBusinessWebFixtureWithModel(t, reportToolsModel{}, businessRPCManifest, businessReportManifest, reportToolsManifest)
	f.options.ConversationOptions.Lease = 30 * time.Second
	f.options.ConversationOptions.Poll = 100 * time.Millisecond
	f.options.ConversationOptions.MaxSteps = 8
	f.options.ConversationOptions.ContextBytes = 256 * 1024
	return f
}

func runReportTools(t *testing.T, b *businessBrowser, id, message string) agent.ConversationRun {
	t.Helper()
	var run agent.ConversationRun
	if err := json.Unmarshal(b.call("POST", "/agent/conversations/"+id+"/messages", map[string]any{"client_message_id": fmt.Sprintf("report-message-%d", time.Now().UnixNano()), "message": message}, 202).Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	// Source authorization rechecks all saved report pages. The race build is
	// substantially slower; avoid hammering that read path while the worker
	// finalizes the same run. This changes only the acceptance observer.
	deadline := time.Now().Add(10 * time.Minute)
	for {
		if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+id+"/runs/"+run.ID, nil, 200).Body.Bytes(), &run); err != nil {
			t.Fatal(err)
		}
		if run.Status == "completed" {
			return run
		}
		if run.Status == "failed" || run.Status == "waiting_user" || time.Now().After(deadline) {
			t.Fatalf("report conversation failed: %+v", run)
		}
		time.Sleep(2 * time.Second)
	}
}

func TestReportToolConversationWithRealOwnerAndRevalidation(t *testing.T) {
	for _, mode := range []string{"module", "service-product"} {
		t.Run(mode, func(t *testing.T) {
			f := prepareReportToolsFixture(t)
			f.open()
			var product *webhost.Host
			var service *httptest.Server
			productPath := filepath.Join(t.TempDir(), "report-product.db")
			closeProduct := func() {
				if product != nil {
					if err := product.Close(t.Context()); err != nil {
						t.Fatal(err)
					}
					product = nil
				}
				if service != nil {
					service.Close()
					service = nil
				}
			}
			t.Cleanup(closeProduct)
			openProduct := func() {
				if mode != "service-product" {
					return
				}
				source, err := bootstrap.ConversationBusinessSource(f.runtime)
				if err != nil {
					t.Fatal(err)
				}
				handler, err := bootstrap.ConversationBusinessHandler(f.runtime, "n01-report-product-service-secret")
				if err != nil {
					t.Fatal(err)
				}
				service = httptest.NewServer(handler)
				client, err := businessrpc.Open(t.Context(), businessrpc.ClientOptions{BaseURL: service.URL, Token: "n01-report-product-service-secret", ExpectedSourceIdentity: source.BusinessSourceIdentity(), ExpectedContractSHA256: businessrpc.ContractSHA256(), Scope: businessrpc.Scope{RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, ApplicationKey: f.cfg.IdentityAudience, IdentityIssuer: f.identity.Descriptor().Issuer}})
				if err != nil {
					t.Fatal(err)
				}
				product, err = webhost.Open(t.Context(), webhost.Options{ReportTools: true, DatabasePath: productPath, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, ApplicationKey: f.cfg.IdentityAudience, IdentityBinding: f.identity, Agent: agentmodule.Options{ConversationProvider: reportToolsModel{}, ConversationOptions: agentmodule.ConversationOptions{Business: client, Lease: 30 * time.Second, Poll: 100 * time.Millisecond, MaxSteps: 8, ContextBytes: 256 * 1024}}})
				if err != nil {
					t.Fatal(err)
				}
				f.handler, err = agentweb.NewHandler(agentweb.Options{Identity: product.Identity, Agent: product.Agent, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, ApplicationKey: f.cfg.IdentityAudience, Origin: businessWebOrigin, Files: f.files})
				if err != nil {
					t.Fatal(err)
				}
			}
			openProduct()
			b := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
			b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
			b.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
			b.assign("headquarters_admin")
			b.session()
			var conversation agent.Conversation
			if err := json.Unmarshal(b.call("POST", "/agent/conversations", map[string]any{"client_id": "n01-report-query"}, 200).Body.Bytes(), &conversation); err != nil {
				t.Fatal(err)
			}
			run := runReportTools(t, b, conversation.ID, "查询我能看到的客户名称报表，先读取目录，再逐页查询，保留来源和完整性。")
			t.Log("initial paged report conversation completed")
			if !strings.Contains(run.Steps[len(run.Steps)-1].Text, "Acme、Beta、Gamma") {
				t.Fatal("actual report rows missing", run)
			}
			calls := 0
			for _, step := range run.Steps {
				for _, call := range step.Calls {
					if call.Name == toolsdk.ReportQueryToolKey {
						calls++
						if call.Status != "completed" {
							t.Fatal("report call failed", call)
						}
					}
				}
			}
			if calls != 5 {
				t.Fatal("expected two catalog pages and three actual query pages", calls)
			}
			history := func(hidden bool, index ...int) {
				var messages agent.ConversationMessagePage
				if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+conversation.ID+"/messages", nil, 200).Body.Bytes(), &messages); err != nil || len(messages.Items) < 2 {
					t.Fatal(messages, err)
				}
				position := 1
				if len(index) != 0 {
					position = index[0]
				}
				message := messages.Items[position]
				if hidden {
					if message.AccessError == "" || strings.Contains(message.Content, "Acme") {
						t.Fatal("old result remained visible", message)
					}
				} else if message.AccessError != "" || !strings.Contains(message.Content, "Acme") {
					t.Fatal("saved report disappeared", message)
				}
			}
			history(false)
			closeProduct()
			f.close()
			f.open()
			openProduct()
			b.session()
			history(false)
			b.assign("business_field_restricted")
			b.session()
			history(true)
			b.assign("headquarters_admin")
			b.session()
			history(true)
			fresh := runReportTools(t, b, conversation.ID, "权限恢复后重新查询报表，不引用失效的旧结果。")
			t.Log("full restart and field revocation checked; restored-permission query completed")
			if !strings.Contains(fresh.Steps[len(fresh.Steps)-1].Text, "Acme、Beta、Gamma") {
				t.Fatal("fresh authorized query failed", fresh)
			}
			history(false, 3)
			b.call("POST", "/auth/refresh", map[string]any{}, 200)
			source, err := bootstrap.ConversationBusinessSource(f.runtime)
			if err != nil {
				t.Fatal(err)
			}
			authority := agent.ConversationAuthority{Known: true, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, UserID: "admin"}
			records, err := source.QueryBusinessRecords(t.Context(), agent.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name"}, PageSize: 1, Sort: []agent.ConversationBusinessSort{{Field: "name", Direction: "asc"}}}, authority)
			if err != nil || len(records.Items) != 1 {
				t.Fatal(records, err)
			}
			routes := f.runtime.Routes()
			scoped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.Header.Set("X-Workspace-ID", f.cfg.IdentityWorkspaceID)
				routes.ServeHTTP(w, r)
			})
			managedIdentityRequest(t, scoped, b.cookies["domainry_agent_access"].Value, http.MethodPost, "/records/customer/items/"+records.Items[0].ID+"/actions/customer.rename", map[string]any{"data": map[string]any{"name": "Updated Acme", "expected_updated_at": records.Items[0].Version}}, "report-tool-change-source", http.StatusOK)
			history(true, 3)
			updated := runReportTools(t, b, conversation.ID, "源数据变化后重新查询报表。")
			t.Log("actual source mutation hides old result; fresh-data query completed")
			if !strings.Contains(updated.Steps[len(updated.Steps)-1].Text, "Beta、Gamma、Updated Acme") {
				t.Fatal("new report used stale data", updated)
			}
			b.assign("business_restricted")
			b.call("POST", "/auth/refresh", map[string]any{}, 200)
			b.session()
			unavailable := runReportTools(t, b, conversation.ID, "再次查询客户报表。")
			if !strings.Contains(unavailable.Steps[len(unavailable.Steps)-1].Text, "未执行查询") {
				t.Fatal("unavailable report claimed execution", unavailable)
			}
			t.Log("real Report, Tools, Agent, Identity and SQLite: 5 calls per query, saved conversation, complete restart, field/read revocation, real data update hides saved answer, fresh query after restored permission/data update; model decisions use an in-process deterministic SDK fixture")
			if mode == "module" && os.Getenv("RUNTIME_BUSINESS_BROWSER") == "1" {
				b.assign("headquarters_admin")
				b.call("POST", "/auth/refresh", map[string]any{}, 200)
				b.session()
				serveBusinessBrowserAcceptance(t, f, b, conversation.ID)
			}
		})
	}
}
