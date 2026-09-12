package integrationtest

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// The fixture expands one actual published object-create contract into two
// concrete operations. Action versions and result reads remain Runtime-owned.
type operationScopeBusinessModel struct{ businessActionWebModel }

func (operationScopeBusinessModel) StreamConversationStep(ctx context.Context, in agentsdk.ConversationStepRequest, emit func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	out, err := (businessActionWebModel{}).StreamConversationStep(ctx, in, emit)
	if err != nil || len(out.Message.ToolCalls) != 1 || out.Message.ToolCalls[0].Name != "invoke_action" {
		return out, err
	}
	call := out.Message.ToolCalls[0]
	var action agentsdk.ConversationBusinessAction
	if json.Unmarshal([]byte(call.Arguments), &action) != nil || action.ActionKey != "customer.register" {
		return out, err
	}
	action.Data = json.RawMessage(`{"name":"Scope First Customer"}`)
	raw, err := json.Marshal(action)
	if err != nil {
		return agentsdk.ConversationStepResult{}, err
	}
	out.Message.ToolCalls = []agentsdk.ConversationToolCall{{ID: call.ID + "-first", Name: call.Name, Arguments: string(raw)}, call}
	return out, nil
}

func TestConversationListedOperationsThroughRuntimeIdentityAndRestart(t *testing.T) {
	t.Setenv("RUNTIME_BUSINESS_LIVE", "")
	f := newBusinessWebFixtureWithModel(t, operationScopeBusinessModel{}, businessActionWebManifest)
	b := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	b.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	b.assign("headquarters_admin")
	login := func() {
		b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": businessWebPassword}, 200)
		b.session()
	}
	login()
	records := func() map[string]int {
		current := f.runtime.Routes()
		routes := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Set("X-Workspace-ID", f.cfg.IdentityWorkspaceID)
			current.ServeHTTP(w, r)
		})
		var page struct {
			Items []struct {
				Data map[string]json.RawMessage `json:"data"`
			} `json:"items"`
		}
		raw := managedIdentityRequest(t, routes, b.cookies["domainry_agent_access"].Value, "GET", "/records/customer?page=1&page_size=20", nil, "", 200).Body.Bytes()
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		result := map[string]int{}
		for _, item := range page.Items {
			var name string
			if err := json.Unmarshal(item.Data["name"], &name); err != nil {
				t.Fatal(err)
			}
			result[name]++
		}
		return result
	}
	before := records()
	c, waiting := b.startAction("创建")
	if waiting.Interaction == nil || len(waiting.Interaction.Operations) != 2 {
		t.Fatal("concrete business scope missing")
	}
	if records()["Scope First Customer"] != before["Scope First Customer"] {
		t.Fatal("effect before user scope approval")
	}
	f.close()
	f.open()
	login()
	stored := b.actionWait(c.ID, waiting.ID, "waiting_confirmation")
	if stored.Interaction.ID != waiting.Interaction.ID || len(stored.Interaction.Operations) != 2 {
		t.Fatal("business scope did not survive full Runtime/Identity restart")
	}
	response := agentsdk.ConversationInteractionResponse{InteractionID: stored.Interaction.ID, ExpectedRevision: stored.Interaction.Revision, ClientID: "approve-two-customers", Decision: "approve", Scope: "listed_operations"}
	path := "/agent/conversations/" + c.ID + "/runs/" + waiting.ID
	b.call("POST", path+"/respond", response, 200)
	done := b.actionWait(c.ID, waiting.ID, "completed")
	b.call("POST", path+"/respond", response, 200)
	after := records()
	if after["Scope First Customer"] != before["Scope First Customer"]+1 || after["Agent Created"] != before["Agent Created"]+1 {
		t.Fatalf("scope effects do not match exact grant: before=%v after=%v", before, after)
	}
	if done.Interaction == nil || done.Interaction.ApprovedScope != "listed_operations" {
		t.Fatal("scope consent receipt missing")
	}
	f.close()
	f.open()
	login()
	final := records()
	if final["Scope First Customer"] != after["Scope First Customer"] || final["Agent Created"] != after["Agent Created"] {
		t.Fatal("scope replayed business effects on restart")
	}
	t.Logf("Runtime/Identity actual actions: one grouped response, two distinct customer records, duplicate response and restart keep one effect each; run=%s", done.ID)
}
