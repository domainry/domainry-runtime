package integrationtest

import (
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
	agentmodule "github.com/domainry/domainry-agent/module"
	agentweb "github.com/domainry/domainry-agent/web"
	"github.com/domainry/domainry-agent/webhost"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

func runAnalysisConversation(t *testing.T, b *businessBrowser, id, message string) agent.ConversationRun {
	t.Helper()
	var run agent.ConversationRun
	if err := json.Unmarshal(b.call("POST", "/agent/conversations/"+id+"/messages", map[string]any{"client_message_id": fmt.Sprintf("analysis-message-%d", time.Now().UnixNano()), "message": message}, 202).Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Minute)
	for {
		if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+id+"/runs/"+run.ID, nil, 200).Body.Bytes(), &run); err != nil {
			t.Fatal(err)
		}
		if run.Status == "completed" {
			return run
		}
		if run.Status == "failed" || run.Status == "waiting_user" || time.Now().After(deadline) {
			t.Fatalf("analysis conversation failed: %+v", run)
		}
		time.Sleep(2 * time.Second)
	}
}

func prepareAnalysisConversationFixture(t *testing.T) *businessWebFixture {
	f := prepareBusinessWebFixtureWithModel(t, analysisToolsModel{}, businessRPCManifest, businessReportManifest, analysisToolsManifest)
	f.options.ConversationOptions = agentmodule.ConversationOptions{Lease: 30 * time.Second, Poll: 100 * time.Millisecond, MaxSteps: 8, ContextBytes: 256 * 1024}
	f.open()
	return f
}

func TestAnalysisBrowserWithRealOwner(t *testing.T) {
	if os.Getenv("RUNTIME_BUSINESS_BROWSER") != "1" {
		t.Skip("opt-in real browser acceptance")
	}
	f := prepareAnalysisConversationFixture(t)
	b := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	b.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	b.assign("headquarters_admin")
	b.session()
	serveBusinessBrowserAcceptance(t, f, b, "")
}

func TestAnalysisConversationWithRealOwnerAndRevalidation(t *testing.T) {
	for _, mode := range []string{"module", "service-product"} {
		t.Run(mode, func(t *testing.T) {
			f := prepareAnalysisConversationFixture(t)
			var product *webhost.Host
			var service *httptest.Server
			productPath := filepath.Join(t.TempDir(), "analysis-product.db")
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
				a := agent.ConversationAuthority{Known: true, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, UserID: "admin"}
				client, s := openAnalysisRPC(t, f, a)
				service = s
				var err error
				product, err = webhost.Open(t.Context(), webhost.Options{AnalysisTools: true, ReportTools: true, DatabasePath: productPath, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, ApplicationKey: f.cfg.IdentityAudience, IdentityBinding: f.identity, Agent: agentmodule.Options{ConversationProvider: analysisToolsModel{}, ConversationOptions: agentmodule.ConversationOptions{Business: client, Lease: 30 * time.Second, Poll: 100 * time.Millisecond, MaxSteps: 8, ContextBytes: 256 * 1024}}})
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
			if err := json.Unmarshal(b.call("POST", "/agent/conversations", map[string]any{"client_id": "n02-analysis"}, 200).Body.Bytes(), &conversation); err != nil {
				t.Fatal(err)
			}
			checkRun := func(run agent.ConversationRun) {
				t.Helper()
				text := run.Steps[len(run.Steps)-1].Text
				for _, fact := range []string{"3 条授权记录，总计 60", "均值 20.000000（非空 3 条）", "对比基准 20、当前 30、差值 10.000000", "Acme=20.00", "Beta=40.00", "Gamma=60.00", "2 条触发", "来源：customer"} {
					if !strings.Contains(text, fact) {
						t.Fatal("actual owner fact missing", fact, text)
					}
				}
				calls := 0
				limited := 0
				for _, step := range run.Steps {
					for _, call := range step.Calls {
						if call.Name == toolsdk.AnalysisRunToolKey {
							calls++
							if call.Status == "failed" && call.ErrorCode == "backend.report.analysis.result_limit_exceeded" {
								limited++
							} else if call.Status != "completed" {
								t.Fatal(call)
							}
						}
					}
				}
				if calls != 6 || limited != 1 {
					t.Fatal("expected catalog, precise limit failure and four complete analyses", calls, limited)
				}
			}
			checkRun(runAnalysisConversation(t, b, conversation.ID, "先读取获准数据集目录，再对客户余额执行汇总、对比、趋势和表格计算，保留统计口径和来源。"))
			t.Log("catalog plus all four owner analyses completed")
			history := func(hidden bool, index int) {
				t.Helper()
				var messages agent.ConversationMessagePage
				if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+conversation.ID+"/messages", nil, 200).Body.Bytes(), &messages); err != nil || len(messages.Items) <= index {
					t.Fatal(messages, err)
				}
				message := messages.Items[index]
				if hidden {
					if message.AccessError == "" || strings.Contains(message.Content, "总计 60") {
						t.Fatal("revoked source remained visible", message)
					}
				} else if message.AccessError != "" || !strings.Contains(message.Content, "总计 60") {
					t.Fatal("saved analysis missing", message)
				}
			}
			history(false, 1)
			closeProduct()
			f.close()
			f.open()
			openProduct()
			b.session()
			history(false, 1)
			b.assign("business_field_restricted")
			b.call("POST", "/auth/refresh", map[string]any{}, 200)
			b.session()
			history(true, 1)
			b.assign("headquarters_admin")
			b.call("POST", "/auth/refresh", map[string]any{}, 200)
			b.session()
			history(true, 1)
			checkRun(runAnalysisConversation(t, b, conversation.ID, "权限恢复后重新分析，不引用失效的旧结果。"))
			history(false, 3)
			t.Log("complete restart, field revocation, stale proof and fresh restored-permission analysis checked")
			b.assign("business_restricted")
			b.call("POST", "/auth/refresh", map[string]any{}, 200)
			b.session()
			unavailable := runAnalysisConversation(t, b, conversation.ID, "再次分析客户余额。")
			if !strings.Contains(unavailable.Steps[len(unavailable.Steps)-1].Text, "未执行分析") {
				t.Fatal("unavailable analysis claimed execution", unavailable)
			}
			t.Log("actual Report / Runtime / Identity / Tools / Agent / SQLite and public RPC; SDK model decisions are deterministic, no model vendor request")
			if mode == "module" && os.Getenv("RUNTIME_BUSINESS_BROWSER") == "1" {
				b.assign("headquarters_admin")
				b.call("POST", "/auth/refresh", map[string]any{}, 200)
				b.session()
				serveBusinessBrowserAcceptance(t, f, b, conversation.ID)
			}
		})
	}
}
