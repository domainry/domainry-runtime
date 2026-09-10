package integrationtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestLiveConversationBusinessActions(t *testing.T) {
	if os.Getenv("RUNTIME_BUSINESS_LIVE") != "1" {
		t.Skip("explicit real model acceptance only")
	}
	f := newBusinessWebFixtureWithModel(t, businessActionWebModel{}, businessActionWebManifest)
	b := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	var routes http.Handler
	resetRoutes := func() {
		current := f.runtime.Routes()
		routes = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Set("X-Workspace-ID", f.cfg.IdentityWorkspaceID)
			current.ServeHTTP(w, r)
		})
	}
	resetRoutes()
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	b.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	b.assign("headquarters_admin")
	login := func() {
		b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": businessWebPassword}, 200)
		b.session()
	}
	login()
	list := func() []agentsdk.ConversationBusinessRecord {
		var page struct {
			Items []agentsdk.ConversationBusinessRecord `json:"items"`
		}
		raw := managedIdentityRequest(t, routes, b.cookies["domainry_agent_access"].Value, "GET", "/records/customer?page=1&page_size=20", nil, "", 200).Body.Bytes()
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		return page.Items
	}
	baseline := len(list())
	var target, lastConversation string
	var completed []agentsdk.ConversationRun
	for index, stage := range []struct{ action, prompt, name, status string }{
		{"customer.register", "请创建一个名为 Agent Live Created 的客户。先查看可用业务目录和创建动作的完整输入说明，使用动作提供的默认值；只创建这一条客户，创建后读取实际记录并报告名称和状态。", "Agent Live Created", "active"},
		{"customer.rename", "请找到名为 Agent Live Created 的客户，将它改名为 Agent Live Renamed。先发现获准动作，读取目标及最新版本，再调用业务动作；只改这一条客户，完成后读取并报告实际名称。", "Agent Live Renamed", "active"},
		{"customer.deactivate", "请将名为 Agent Live Renamed 的客户停用（状态设为 inactive）。先查看动作说明并读取最新记录版本，再调用业务动作；完成后读取实际记录并报告名称和状态。", "Agent Live Renamed", "inactive"},
	} {
		var c agentsdk.Conversation
		if err := json.Unmarshal(b.call("POST", "/agent/conversations", map[string]any{"client_id": fmt.Sprintf("live-action-%d", index), "title": stage.action + " 真实模型验收"}, 200).Body.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		var run agentsdk.ConversationRun
		if err := json.Unmarshal(b.call("POST", "/agent/conversations/"+c.ID+"/messages", map[string]any{"client_message_id": "action", "message": stage.prompt}, 202).Body.Bytes(), &run); err != nil {
			t.Fatal(err)
		}
		waiting := b.actionWait(c.ID, run.ID, "waiting_confirmation")
		var proposed agentsdk.ConversationBusinessAction
		if waiting.Interaction == nil || waiting.Interaction.Tool != "invoke_action" || json.Unmarshal([]byte(waiting.Interaction.Arguments), &proposed) != nil || proposed.ObjectKey != "customer" || proposed.ActionKey != stage.action || proposed.Version == "" || proposed.RecordID != target {
			t.Fatal("real model proposed a different operation", waiting.Interaction)
		}
		var payload map[string]any
		if err := json.Unmarshal(proposed.Data, &payload); err != nil {
			t.Fatal(err)
		}
		if index < 2 && payload["name"] != stage.name || index == 2 && payload["status"] != stage.status || index > 0 && (payload["expected_updated_at"] == nil || payload["expected_updated_at"] == "") {
			t.Fatal("real model proposed different values or omitted concurrency", payload)
		}
		if index == 0 {
			f.close()
			f.open()
			resetRoutes()
			login()
		}
		done := b.approveAction(c, waiting)
		completed = append(completed, done)
		mutations := 0
		for _, step := range done.Steps {
			for _, call := range step.Calls {
				if call.Name != "invoke_action" {
					continue
				}
				if call.Status != "completed" || call.ErrorCode != "" {
					t.Fatal("real action failed", call)
				}
				mutations++
				var evidence agentsdk.ConversationBusinessEvidence
				var receipt agentsdk.ConversationBusinessActionResult
				if json.Unmarshal([]byte(call.ResultPreview), &evidence) != nil || json.Unmarshal(evidence.Data, &receipt) != nil || receipt.Status != "completed" || receipt.InvocationID == "" {
					t.Fatal("missing actual live receipt", call.ResultPreview)
				}
				if index == 0 {
					if len(receipt.CreatedRecords) != 1 {
						t.Fatal(receipt)
					}
					target = receipt.CreatedRecords[0].RecordID
				}
			}
		}
		if mutations != 1 || target == "" {
			t.Fatal("unexpected number of real mutations", mutations)
		}
		rows, found := list(), false
		for _, row := range rows {
			if row.ID == target {
				found = string(row.Data["name"]) == fmt.Sprintf("%q", stage.name) && string(row.Data["status"]) == fmt.Sprintf("%q", stage.status)
			}
		}
		if !found || len(rows) != baseline+1 {
			t.Fatal("real model action did not produce the expected single-record state", stage.action, rows)
		}
		lastConversation = c.ID
		t.Logf("Real model business action verified: %s", stage.action)
	}
	f.close()
	f.open()
	resetRoutes()
	login()
	rows := list()
	if len(rows) != baseline+1 {
		t.Fatal("real business actions repeated after restart")
	}
	for _, run := range completed {
		var page agentsdk.ConversationMessagePage
		if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+run.ConversationID+"/messages", nil, 200).Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) < 3 || page.Items[len(page.Items)-1].AccessError != "" {
			t.Fatal("live result unavailable after restart", page)
		}
	}
	if dir := os.Getenv("RUNTIME_BUSINESS_EVIDENCE_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.MarshalIndent(completed, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "business-actions-live.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(completed[2].Steps[len(completed[2].Steps)-1].Text, "Agent Live Renamed") {
		t.Fatal("model answer omitted actual target")
	}
	if os.Getenv("RUNTIME_BUSINESS_BROWSER") == "1" {
		serveBusinessBrowserAcceptance(t, f, b, lastConversation)
	}
}
