package integrationtest

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestConversationBusinessSnapshotSurvivesActionUpdateAndIdentityRestart(t *testing.T) {
	// This exercises actual Identity, Runtime actions, Agent HTTP and SQLite;
	// only model decisions are deterministic. It is not invoke_action delivery.
	t.Setenv("RUNTIME_BUSINESS_LIVE", "")
	f := newBusinessWebFixture(t)
	routes := f.runtime.Routes()
	scopedRoutes := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Workspace-ID", f.cfg.IdentityWorkspaceID)
		routes.ServeHTTP(w, r)
	})
	b := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	b.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	b.assign("headquarters_admin")
	// The ordinary Runtime business endpoint validates the token's policy
	// revision. Obtain a new token after assigning the role, as its UI does.
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": businessWebPassword}, 200)
	b.session()
	var c agentsdk.Conversation
	if err := json.Unmarshal(b.call("POST", "/agent/conversations", map[string]any{"client_id": "business-snapshot-update"}, 200).Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	run := b.run(c.ID)
	var target agentsdk.ConversationBusinessGet
	for _, step := range run.Steps {
		for _, call := range step.Calls {
			if call.Name == "get_record" {
				if err := json.Unmarshal([]byte(call.Arguments), &target); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(call.ResultPreview, "host_proof") {
					t.Fatal("real host did not attest its read result")
				}
			}
		}
	}
	if target.ObjectKey != "customer" || target.RecordID == "" {
		t.Fatal("no actual customer read")
	}
	oldAnswer := run.Steps[len(run.Steps)-1].Text
	if !strings.Contains(oldAnswer, "Acme") {
		t.Fatal("missing initial customer fact")
	}
	check := func(hidden bool) {
		t.Helper()
		var page agentsdk.ConversationMessagePage
		if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+c.ID+"/messages", nil, 200).Body.Bytes(), &page); err != nil || len(page.Items) < 2 {
			t.Fatal(page, err)
		}
		message := page.Items[1]
		if hidden {
			if message.AccessError == "" || strings.Contains(message.Content, "Acme") {
				t.Fatal("revoked saved answer remained visible")
			}
		} else if message.AccessError != "" || message.Content != oldAnswer {
			t.Fatal("historical answer changed or was hidden", message)
		}
	}
	check(false)
	managedIdentityRequest(t, scopedRoutes, b.cookies["domainry_agent_access"].Value, http.MethodPost, "/records/customer/items/"+target.RecordID+"/actions/customer.rename", map[string]any{"data": map[string]any{"name": "Updated Acme"}}, "snapshot-rename-customer", http.StatusOK)
	check(false)
	updated := b.runWithMessage(c.ID, "再次查询全部客户，保留此前讨论并报告当前名称和余额。")
	if !strings.Contains(updated.Steps[len(updated.Steps)-1].Text, "Updated Acme") {
		t.Fatal("follow-up did not use updated source")
	}
	check(false)
	f.close()
	f.open()
	b.session()
	check(false)
	for _, role := range []string{"business_field_restricted", "headquarters_admin", "business_restricted", "headquarters_admin"} {
		b.assign(role)
		b.session()
		check(role != "headquarters_admin")
	}
}
