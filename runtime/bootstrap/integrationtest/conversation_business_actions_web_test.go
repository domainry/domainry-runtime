package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func businessActionWebManifest(manifest map[string]any) {
	for _, value := range manifest["objects"].([]any) {
		object := value.(map[string]any)
		if object["key"] == "customer" {
			object["fields"] = append(object["fields"].([]any), map[string]any{"key": "status", "name": "状态", "type": "select", "options": []any{map[string]any{"value": "active", "label": "启用"}, map[string]any{"value": "inactive", "label": "停用"}}, "default_value": "active"})
		}
	}
	for _, value := range manifest["seed_records"].([]any) {
		seed := value.(map[string]any)
		if seed["object_key"] == "customer" {
			seed["data"].(map[string]any)["status"] = "active"
		}
	}
	manifest["actions"] = []any{
		map[string]any{"key": "customer.register", "object_key": "customer", "label": "创建客户", "kind": "object_create", "preconditions": []any{}, "audit_event": "customer_registered", "payload_fields": []any{
			map[string]any{"key": "name", "type": "text", "required": true}, map[string]any{"key": "owner", "type": "user", "default_value": "admin"},
		}},
		map[string]any{"key": "customer.rename", "object_key": "customer", "label": "客户改名", "kind": "record_update", "preconditions": []any{}, "audit_event": "customer_renamed", "optimistic_concurrency": true, "payload_fields": []any{map[string]any{"key": "name", "type": "text", "required": true}}},
		map[string]any{"key": "customer.deactivate", "object_key": "customer", "label": "停用客户", "kind": "transition_state", "preconditions": []any{"status == active"}, "audit_event": "customer_deactivated", "optimistic_concurrency": true, "payload_fields": []any{map[string]any{"key": "status", "type": "select", "options": []any{"inactive"}, "required": true}}},
	}
	for _, value := range manifest["roles"].([]any) {
		role := value.(map[string]any)
		if role["key"] != "headquarters_admin" {
			continue
		}
		for _, key := range []string{"customer.register", "customer.deactivate", agentsdk.ConversationToolActionPrefix + "invoke_action", agentsdk.ConversationInteractionPermission().Key} {
			scope := "owner"
			// Module endpoint grants enable the operation. Agent's repository
			// still enforces the actual conversation owner on every request.
			if strings.HasPrefix(key, "agent.") {
				scope = "all"
			}
			role["permissions"] = append(role["permissions"].([]any), map[string]any{"permission_key": key, "data_scope": scope})
		}
		role["field_permissions"] = append(role["field_permissions"].([]any), map[string]any{"object_key": "customer", "field_key": "status", "read": true, "write": false, "export": false})
		encoded, _ := json.Marshal(role)
		var restricted map[string]any
		_ = json.Unmarshal(encoded, &restricted)
		restricted["key"], restricted["name"] = "business_action_restricted", "Business action restricted"
		permissions := []any{}
		for _, grant := range restricted["permissions"].([]any) {
			if grant.(map[string]any)["permission_key"] != "customer.rename" {
				permissions = append(permissions, grant)
			}
		}
		restricted["permissions"] = permissions
		manifest["roles"] = append(manifest["roles"].([]any), restricted)
	}
}

// Only model decisions are fixtures. IDs, action versions, row versions and
// mutation acknowledgements all come from the real Runtime tool results.
type businessActionWebModel struct{ businessWebModel }

func (businessActionWebModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "business-actions-web", Fingerprint: "business-actions-web-v1"}
}

func (businessActionWebModel) StreamConversationStep(_ context.Context, in agentsdk.ConversationStepRequest, emit func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	command := ""
	for _, message := range in.Messages {
		if message.Role == "user" && strings.HasPrefix(message.Content, "验收：") {
			command = strings.TrimPrefix(message.Content, "验收：")
		}
	}
	actionKey, target := "customer.rename", "Agent Created"
	switch command {
	case "创建":
		actionKey = "customer.register"
	case "并发停用":
		actionKey, target = "customer.deactivate", "Agent Renamed"
	case "停用":
		actionKey, target = "customer.deactivate", "Concurrent Name"
	case "网页停用":
		actionKey, target = "customer.deactivate", "Agent Renamed"
	case "撤权":
		target = "Acme"
	}
	tool := func(name, id string, args any) (agentsdk.ConversationStepResult, error) {
		raw, _ := json.Marshal(args)
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: id, Name: name, Arguments: string(raw)}}}, FinishReason: "tool_calls"}, nil
	}
	answer := func(value string) (agentsdk.ConversationStepResult, error) {
		if err := emit(agentsdk.ConversationModelEvent{Type: "text.delta", Delta: value}); err != nil {
			return agentsdk.ConversationStepResult{}, err
		}
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: value}, FinishReason: "stop"}, nil
	}
	last := in.Messages[len(in.Messages)-1]
	if last.Role != "tool" {
		return tool("business_catalog", "action-contract", agentsdk.ConversationBusinessCatalogQuery{Kind: "actions", ObjectKey: "customer", ActionKey: actionKey})
	}
	var operation agentsdk.ConversationBusinessOperation
	var record agentsdk.ConversationBusinessRecord
	var latest agentsdk.ConversationBusinessEvidence
	for _, message := range in.Messages {
		if message.Role != "tool" {
			continue
		}
		var result agentsdk.ConversationToolResult
		var evidence agentsdk.ConversationBusinessEvidence
		if err := json.Unmarshal([]byte(message.Content), &result); err != nil {
			return agentsdk.ConversationStepResult{}, err
		}
		if result.Status != "completed" {
			return answer("动作未完成：" + result.ErrorCode)
		}
		if err := json.Unmarshal(result.Content, &evidence); err != nil {
			return agentsdk.ConversationStepResult{}, err
		}
		if message.ToolCallID == "action-contract" {
			var page agentsdk.ConversationBusinessCatalogPage
			if json.Unmarshal(evidence.Data, &page) != nil || len(page.Actions) != 1 || page.Actions[0].ExecutionVersion == "" {
				return agentsdk.ConversationStepResult{}, fmt.Errorf("missing actual execution contract")
			}
			operation = page.Actions[0]
		}
		if message.ToolCallID == "target" {
			var page agentsdk.ConversationBusinessRecordPage
			if json.Unmarshal(evidence.Data, &page) != nil || len(page.Items) != 1 || page.Items[0].Version == "" {
				return agentsdk.ConversationStepResult{}, fmt.Errorf("missing unique target and version")
			}
			record = page.Items[0]
		}
		if message.ToolCallID == last.ToolCallID {
			latest = evidence
		}
	}
	switch last.ToolCallID {
	case "action-contract":
		if actionKey != "customer.register" {
			if !operation.OptimisticConcurrency || !strings.Contains(string(operation.InputSchema), "expected_updated_at") {
				return agentsdk.ConversationStepResult{}, fmt.Errorf("missing published concurrency input")
			}
			return tool("query_records", "target", agentsdk.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name", "status"}, Filters: []agentsdk.ConversationBusinessFilter{{Field: "name", Operator: "eq", Value: json.RawMessage(fmt.Sprintf("%q", target))}}, PageSize: 2})
		}
		fallthrough
	case "target":
		data := map[string]any{"name": "Agent Created"}
		if record.ID != "" {
			data["name"], data["expected_updated_at"] = "Agent Renamed", record.Version
		}
		if actionKey == "customer.deactivate" {
			delete(data, "name")
			data["status"] = "inactive"
		}
		raw, _ := json.Marshal(data)
		return tool("invoke_action", "mutate", agentsdk.ConversationBusinessAction{ObjectKey: "customer", ActionKey: actionKey, Version: operation.ExecutionVersion, RecordID: record.ID, Data: raw})
	case "mutate":
		var receipt agentsdk.ConversationBusinessActionResult
		if json.Unmarshal(latest.Data, &receipt) != nil || receipt.Status != "completed" || receipt.InvocationID == "" {
			return agentsdk.ConversationStepResult{}, fmt.Errorf("missing actual action acknowledgement")
		}
		id := receipt.RecordID
		if actionKey == "customer.register" {
			if len(receipt.CreatedRecords) != 1 {
				return agentsdk.ConversationStepResult{}, fmt.Errorf("create returned no actual reference")
			}
			id = receipt.CreatedRecords[0].RecordID
		}
		return tool("get_record", "verify", agentsdk.ConversationBusinessGet{ObjectKey: "customer", RecordID: id, Fields: []string{"name", "status"}})
	case "verify":
		return answer("已读取业务实际结果：" + string(latest.Data))
	}
	return agentsdk.ConversationStepResult{}, fmt.Errorf("unexpected action model step")
}

func (b *businessBrowser) actionWait(conversationID, runID, status string) agentsdk.ConversationRun {
	b.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	pollInterval := 10 * time.Millisecond
	if b.f.identityFactory != nil {
		pollInterval = 500 * time.Millisecond
	}
	if os.Getenv("RUNTIME_BUSINESS_LIVE") == "1" {
		deadline = time.Now().Add(4 * time.Minute)
		// Real model calls can take minutes. Keep status polling below the
		// actual workspace request limit while retaining production admission.
		pollInterval = time.Second
	}
	for {
		var run agentsdk.ConversationRun
		response := b.call("GET", "/agent/conversations/"+conversationID+"/runs/"+runID, nil, 200)
		if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
			b.t.Fatal(err)
		}
		if run.Status == status {
			return run
		}
		if run.Status == "failed" || run.Status == "cancelled" || run.Status == "needs_reconciliation" || run.Status == "completed" || time.Now().After(deadline) {
			b.t.Fatalf("action run did not reach %s: %s", status, response.Body.String())
		}
		time.Sleep(pollInterval)
	}
}

func (b *businessBrowser) startAction(command string) (agentsdk.Conversation, agentsdk.ConversationRun) {
	b.t.Helper()
	var c agentsdk.Conversation
	if err := json.Unmarshal(b.call("POST", "/agent/conversations", map[string]any{"client_id": fmt.Sprintf("action-%d", time.Now().UnixNano())}, 200).Body.Bytes(), &c); err != nil {
		b.t.Fatal(err)
	}
	var run agentsdk.ConversationRun
	if err := json.Unmarshal(b.call("POST", "/agent/conversations/"+c.ID+"/messages", map[string]any{"client_message_id": "action", "message": "验收：" + command}, 202).Body.Bytes(), &run); err != nil {
		b.t.Fatal(err)
	}
	return c, b.actionWait(c.ID, run.ID, "waiting_confirmation")
}

func (b *businessBrowser) approveAction(c agentsdk.Conversation, run agentsdk.ConversationRun) agentsdk.ConversationRun {
	b.t.Helper()
	response := agentsdk.ConversationInteractionResponse{InteractionID: run.Interaction.ID, ClientID: "approve", ExpectedRevision: run.Interaction.Revision, Decision: "approve"}
	path := "/agent/conversations/" + c.ID + "/runs/" + run.ID
	b.call("POST", path+"/respond", response, 200)
	completed := b.actionWait(c.ID, run.ID, "completed")
	b.call("POST", path+"/respond", response, 200)
	return completed
}

func TestConversationBusinessActionsThroughIdentityWebAndRestart(t *testing.T) {
	t.Setenv("RUNTIME_BUSINESS_LIVE", "")
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
	count := func() int {
		var page struct {
			Items []json.RawMessage `json:"items"`
		}
		raw := managedIdentityRequest(t, routes, b.cookies["domainry_agent_access"].Value, "GET", "/records/customer?page=1&page_size=20", nil, "", 200).Body.Bytes()
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		return len(page.Items)
	}
	baseline := count()
	c, waiting := b.startAction("创建")
	if waiting.Interaction == nil || !strings.Contains(waiting.Interaction.Arguments, "Agent Created") || count() != baseline {
		t.Fatal("creation happened before concrete confirmation")
	}
	f.close()
	f.open()
	resetRoutes()
	login()
	reloaded := b.actionWait(c.ID, waiting.ID, "waiting_confirmation")
	if reloaded.Interaction.ID != waiting.Interaction.ID || reloaded.Interaction.Arguments != waiting.Interaction.Arguments {
		t.Fatal("confirmation changed across restart")
	}
	created := b.approveAction(c, reloaded)
	if count() != baseline+1 || !strings.Contains(created.Steps[len(created.Steps)-1].Text, "Agent Created") {
		t.Fatal("create did not commit exactly one actual customer", created)
	}
	read := func(id string) agentsdk.ConversationBusinessRecord {
		var record struct {
			ID        string                     `json:"id"`
			UpdatedAt string                     `json:"updated_at"`
			Data      map[string]json.RawMessage `json:"data"`
		}
		raw := managedIdentityRequest(t, routes, b.cookies["domainry_agent_access"].Value, "GET", "/records/customer/items/"+id, nil, "", 200).Body.Bytes()
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		return agentsdk.ConversationBusinessRecord{ID: record.ID, Version: record.UpdatedAt, Data: record.Data}
	}
	renameConversation, renameWaiting := b.startAction("改名")
	var rename agentsdk.ConversationBusinessAction
	if err := json.Unmarshal([]byte(renameWaiting.Interaction.Arguments), &rename); err != nil {
		t.Fatal(err)
	}
	before := read(rename.RecordID)
	renamedRun := b.approveAction(renameConversation, renameWaiting)
	after := read(rename.RecordID)
	if string(before.Data["name"]) != `"Agent Created"` || string(after.Data["name"]) != `"Agent Renamed"` || before.Version == after.Version || count() != baseline+1 {
		raw, _ := json.Marshal(renamedRun)
		t.Fatalf("rename did not update actual record once: %s", raw)
	}
	conflictConversation, conflictWaiting := b.startAction("并发停用")
	managedIdentityRequest(t, routes, b.cookies["domainry_agent_access"].Value, "POST", "/records/customer/items/"+rename.RecordID+"/actions/customer.rename", map[string]any{"data": map[string]any{"name": "Concurrent Name", "expected_updated_at": after.Version}}, "concurrent-business-edit", 200)
	conflict := b.approveAction(conflictConversation, conflictWaiting)
	if !strings.Contains(conflict.Steps[len(conflict.Steps)-1].Text, "business_record_version_conflict") || string(read(rename.RecordID).Data["status"]) != `"active"` {
		t.Fatal("stale confirmation overwrote a concurrent change", conflict)
	}
	transitionConversation, transitionWaiting := b.startAction("停用")
	b.approveAction(transitionConversation, transitionWaiting)
	if string(read(rename.RecordID).Data["status"]) != `"inactive"` {
		t.Fatal("transition did not persist")
	}
	deniedConversation, deniedWaiting := b.startAction("撤权")
	b.assign("business_action_restricted")
	login()
	response := agentsdk.ConversationInteractionResponse{InteractionID: deniedWaiting.Interaction.ID, ClientID: "approve", ExpectedRevision: deniedWaiting.Interaction.Revision, Decision: "approve"}
	b.call("POST", "/agent/conversations/"+deniedConversation.ID+"/runs/"+deniedWaiting.ID+"/respond", response, 403)
	b.assign("headquarters_admin")
	login()
	b.approveAction(deniedConversation, deniedWaiting)
	f.close()
	f.open()
	resetRoutes()
	login()
	if count() != baseline+1 || string(read(rename.RecordID).Data["status"]) != `"inactive"` {
		t.Fatal("business effect lost or repeated on restart")
	}
	var page agentsdk.ConversationMessagePage
	if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+renameConversation.ID+"/messages", nil, 200).Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || page.Items[2].AccessError != "" || !strings.Contains(page.Items[2].Content, "Agent Renamed") {
		t.Fatal("saved acknowledgement or historical read lost after later mutations and restart", page)
	}
}

func TestConversationBusinessActionsBrowser(t *testing.T) {
	if os.Getenv("RUNTIME_BUSINESS_ACTIONS_BROWSER") != "1" {
		t.Skip("explicit browser acceptance only")
	}
	t.Setenv("RUNTIME_BUSINESS_LIVE", "")
	f := newBusinessWebFixtureWithModel(t, businessActionWebModel{}, businessActionWebManifest)
	b := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	b.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	b.assign("headquarters_admin")
	b.session()
	c, waiting := b.startAction("创建")
	if waiting.Interaction == nil {
		t.Fatal("no persisted confirmation for browser")
	}
	serveBusinessBrowserAcceptance(t, f, b, c.ID)
	// Check the effects after the actual browser has created, renamed and
	// deactivated the same customer through three separately confirmed runs.
	routes := f.runtime.Routes()
	scoped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Workspace-ID", f.cfg.IdentityWorkspaceID)
		routes.ServeHTTP(w, r)
	})
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": businessWebPassword}, 200)
	var page struct {
		Items []struct {
			ID   string         `json:"id"`
			Data map[string]any `json:"data"`
		} `json:"items"`
	}
	raw := managedIdentityRequest(t, scoped, b.cookies["domainry_agent_access"].Value, "GET", "/records/customer?page=1&page_size=20", nil, "", 200).Body.Bytes()
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	matched := 0
	for _, row := range page.Items {
		if row.Data["name"] == "Agent Renamed" && row.Data["status"] == "inactive" {
			matched++
		}
	}
	if len(page.Items) != 4 || matched != 1 {
		t.Fatal("browser did not create, update and transition exactly one customer", string(raw))
	}
	completed := b.actionWait(c.ID, waiting.ID, "completed")
	if completed.Interaction != nil && completed.Interaction.Status == "pending" {
		t.Fatal("browser confirmation remained pending")
	}
	t.Log("Browser create/update/transition verified against actual Runtime records")
}
