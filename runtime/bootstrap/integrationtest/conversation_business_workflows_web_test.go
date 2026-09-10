package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
)

func businessWorkflowWebManifest(manifest map[string]any) {
	trigger := map[string]any{"type": "manual"}
	manifest["workflows"] = []any{map[string]any{
		"key": "agent_review", "name": "会话业务审核", "enabled": true, "trigger": trigger, "trigger_contract": trigger, "action": map[string]any{"type": "workflow_graph"},
		"input_fields": []any{map[string]any{"key": "reason", "name": "审核原因", "type": "text", "required": true}},
		"graph": map[string]any{"version": 2, "nodes": []any{
			map[string]any{"id": "start", "type": "trigger", "name": "发起审核"},
			map[string]any{"id": "review", "type": "approval", "name": "经理审批", "contract": map[string]any{"approval": map[string]any{"mode": "any", "resolver_mode": "union", "empty_assignee_policy": "fail", "resolvers": []any{map[string]any{"type": "users", "user_ids": []any{"admin"}}}}}},
		}, "edges": []any{map[string]any{"id": "start-review", "source": "start", "target": "review"}}},
	}}
	for _, value := range manifest["roles"].([]any) {
		role := value.(map[string]any)
		if role["key"] != "headquarters_admin" {
			continue
		}
		keys := append([]string{"workflow.agent_review.run", agentsdk.ConversationToolActionPrefix + "workflow_start", agentsdk.ConversationToolActionPrefix + "workflow_get", agentsdk.ConversationInteractionPermission().Key}, integrationWorkflowTaskDecisionPermissions()...)
		for _, key := range keys {
			role["permissions"] = append(role["permissions"].([]any), map[string]any{"permission_key": key, "data_scope": "all"})
		}
	}
}

type businessWorkflowWebModel struct{ businessWebModel }

func (businessWorkflowWebModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "business-workflows-web", Fingerprint: "business-workflows-web-v1"}
}
func (businessWorkflowWebModel) StreamConversationStep(_ context.Context, in agentsdk.ConversationStepRequest, emit func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
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
		if strings.HasPrefix(last.Content, "查询流程 ") {
			return tool("workflow_get", "progress", agentsdk.ConversationWorkflowGet{WorkflowKey: "agent_review", ProcessID: strings.TrimPrefix(last.Content, "查询流程 ")})
		}
		return tool("business_catalog", "contract", agentsdk.ConversationBusinessCatalogQuery{Kind: "workflows", WorkflowKey: "agent_review"})
	}
	var result agentsdk.ConversationToolResult
	var evidence agentsdk.ConversationBusinessEvidence
	if json.Unmarshal([]byte(last.Content), &result) != nil || result.Status != "completed" || json.Unmarshal(result.Content, &evidence) != nil {
		return answer("流程工具未完成：" + result.ErrorCode)
	}
	switch evidence.Operation {
	case "business_catalog":
		var page agentsdk.ConversationBusinessCatalogPage
		if json.Unmarshal(evidence.Data, &page) != nil || len(page.Workflows) != 1 || page.Workflows[0].ExecutionVersion == "" {
			return answer("流程没有可执行契约")
		}
		return tool("workflow_start", "start", agentsdk.ConversationWorkflowStart{WorkflowKey: page.Workflows[0].Key, Version: page.Workflows[0].ExecutionVersion, Data: json.RawMessage(`{"reason":"会话端到端验收"}`)})
	case "workflow_start":
		var receipt agentsdk.ConversationWorkflowReceipt
		if json.Unmarshal(evidence.Data, &receipt) != nil || receipt.Status != "accepted" {
			return answer("启动结果未确认")
		}
		return tool("workflow_get", "progress", agentsdk.ConversationWorkflowGet{WorkflowKey: receipt.WorkflowKey, ProcessID: receipt.ProcessID})
	case "workflow_get":
		return answer("实际流程状态：" + string(evidence.Data))
	}
	return agentsdk.ConversationStepResult{}, fmt.Errorf("unexpected workflow tool")
}

func TestConversationBusinessWorkflowThroughIdentityWebAndRestart(t *testing.T) {
	t.Setenv("RUNTIME_BUSINESS_LIVE", "")
	runConversationBusinessWorkflowAcceptance(t, false)
}

func TestLiveConversationBusinessWorkflow(t *testing.T) {
	if os.Getenv("RUNTIME_BUSINESS_LIVE") != "1" {
		t.Skip("explicit live workflow acceptance only")
	}
	runConversationBusinessWorkflowAcceptance(t, true)
}

func runConversationBusinessWorkflowAcceptance(t *testing.T, live bool) {
	f := newBusinessWebFixtureWithModel(t, businessWorkflowWebModel{}, businessWorkflowWebManifest)
	b := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	var routes http.Handler
	reset := func() {
		current := f.runtime.Routes()
		routes = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Set("X-Workspace-ID", f.cfg.IdentityWorkspaceID)
			current.ServeHTTP(w, r)
		})
	}
	reset()
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	b.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	b.assign("headquarters_admin")
	login := func() {
		b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": businessWebPassword}, 200)
		b.session()
	}
	login()
	var c agentsdk.Conversation
	var waiting agentsdk.ConversationRun
	if live {
		if err := json.Unmarshal(b.call("POST", "/agent/conversations", map[string]any{"client_id": "live-workflow", "title": "真实模型流程验收"}, 200).Body.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		var run agentsdk.ConversationRun
		if err := json.Unmarshal(b.call("POST", "/agent/conversations/"+c.ID+"/messages", map[string]any{"client_message_id": "start", "message": "请发起一次‘会话业务审核’流程，审核原因为‘会话端到端验收’。先查看当前可用流程及输入要求，只启动一次；受理后查询实际流程进度，说明是否仍需审批。"}, 202).Body.Bytes(), &run); err != nil {
			t.Fatal(err)
		}
		waiting = b.actionWait(c.ID, run.ID, "waiting_confirmation")
	} else {
		c, waiting = b.startAction("启动流程")
	}
	if waiting.Interaction == nil || waiting.Interaction.Tool != "workflow_start" {
		t.Fatal(waiting)
	}
	f.close()
	f.open()
	reset()
	login()
	done := b.approveAction(c, waiting)
	var receipt agentsdk.ConversationWorkflowReceipt
	var state agentsdk.ConversationWorkflowState
	starts := 0
	for _, step := range done.Steps {
		for _, call := range step.Calls {
			var e agentsdk.ConversationBusinessEvidence
			if json.Unmarshal([]byte(call.ResultPreview), &e) != nil {
				continue
			}
			switch call.Name {
			case "workflow_start":
				starts++
				_ = json.Unmarshal(e.Data, &receipt)
			case "workflow_get":
				_ = json.Unmarshal(e.Data, &state)
			}
		}
	}
	if starts != 1 || receipt.Status != "accepted" || receipt.ProcessID == "" || state.ProcessID != receipt.ProcessID || state.Status != "waiting" || state.Terminal {
		t.Fatal(receipt, state)
	}
	var detail workflowapplication.ParticipantWorkflowProcessDetailDTO
	raw := managedIdentityRequest(t, routes, b.cookies["domainry_agent_access"].Value, "GET", "/workflow/processes/"+receipt.ProcessID, nil, "", 200).Body.Bytes()
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Tasks) != 1 || detail.Tasks[0].Status != "open" {
		t.Fatal("workflow was not actually waiting for approval", string(raw))
	}
	managedIdentityRequest(t, routes, b.cookies["domainry_agent_access"].Value, "POST", "/workflow/tasks/"+detail.Tasks[0].ID+"/approve", map[string]any{"comment": "通过实际业务审批"}, "workflow-approval", 200)
	var next agentsdk.ConversationRun
	if err := json.Unmarshal(b.call("POST", "/agent/conversations/"+c.ID+"/messages", map[string]any{"client_message_id": "progress", "message": "查询流程 " + receipt.ProcessID}, 202).Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	completed := b.actionWait(c.ID, next.ID, "completed")
	var finalState agentsdk.ConversationWorkflowState
	for _, step := range completed.Steps {
		for _, call := range step.Calls {
			if call.Name == "workflow_start" {
				t.Fatal("query restarted workflow")
			}
			if call.Name == "workflow_get" && call.Status == "completed" {
				var e agentsdk.ConversationBusinessEvidence
				_ = json.Unmarshal([]byte(call.ResultPreview), &e)
				_ = json.Unmarshal(e.Data, &finalState)
			}
		}
	}
	if finalState.ProcessID != receipt.ProcessID || !finalState.Terminal || finalState.BusinessOutcome != "approved" {
		t.Fatal("did not read completed workflow outcome", completed)
	}
	f.close()
	f.open()
	reset()
	login()
	var messages agentsdk.ConversationMessagePage
	if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+c.ID+"/messages", nil, 200).Body.Bytes(), &messages); err != nil {
		t.Fatal(err)
	}
	if len(messages.Items) != 5 || messages.Items[2].AccessError != "" || messages.Items[2].Content != done.Steps[len(done.Steps)-1].Text || messages.Items[4].AccessError != "" || messages.Items[4].Content != completed.Steps[len(completed.Steps)-1].Text {
		t.Fatal("historical and current progress lost after restart", messages)
	}
	b.assign("business_restricted")
	messages = agentsdk.ConversationMessagePage{}
	if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+c.ID+"/messages", nil, 200).Body.Bytes(), &messages); err != nil {
		t.Fatal(err)
	}
	if messages.Items[2].AccessError == "" || messages.Items[4].AccessError == "" {
		t.Fatal("revoked workflow remained visible")
	}
	b.assign("headquarters_admin")
	messages = agentsdk.ConversationMessagePage{}
	if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+c.ID+"/messages", nil, 200).Body.Bytes(), &messages); err != nil {
		t.Fatal(err)
	}
	if messages.Items[2].AccessError != "" || messages.Items[4].AccessError != "" {
		t.Fatal("restored workflow not accessible")
	}
	if live {
		if !strings.Contains(messages.Items[2].Content, "审批") || (!strings.Contains(messages.Items[4].Content, "通过") && !strings.Contains(messages.Items[4].Content, "批准") && !strings.Contains(messages.Items[4].Content, "approved")) {
			t.Fatal("model did not report actual approval outcome", messages)
		}
		if dir := os.Getenv("RUNTIME_BUSINESS_EVIDENCE_DIR"); dir != "" {
			raw, _ := json.MarshalIndent([]agentsdk.ConversationRun{done, completed}, "", "  ")
			if err := os.WriteFile(filepath.Join(dir, "business-workflows-live.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	processes := func() []workflowapplication.ParticipantWorkflowProcessDTO {
		reset()
		login()
		var items []workflowapplication.ParticipantWorkflowProcessDTO
		response := managedIdentityRequest(t, routes, b.cookies["domainry_agent_access"].Value, "GET", "/workflow/processes?limit=50", nil, "", 200)
		if err := json.Unmarshal(response.Body.Bytes(), &items); err != nil {
			t.Fatal(err)
		}
		return items
	}
	if items := processes(); len(items) != 1 || items[0].ID != receipt.ProcessID || items[0].BusinessOutcome != "approved" {
		t.Fatal("workflow effect lost or duplicated", items)
	}
	t.Log("Actual workflow acceptance, approval, progress, duplicate confirmation, restart and revocation verified")
	if os.Getenv("RUNTIME_BUSINESS_BROWSER") == "1" {
		var browserProcessID string
		serveBusinessBrowserAcceptance(t, f, b, c.ID, map[string]func() any{
			"approve-workflow": func() any {
				items := processes()
				if len(items) != 2 || browserProcessID != "" {
					t.Fatal("browser must create exactly one additional workflow before approval", items)
				}
				for _, item := range items {
					if item.ID == receipt.ProcessID {
						continue
					}
					var detail workflowapplication.ParticipantWorkflowProcessDetailDTO
					response := managedIdentityRequest(t, routes, b.cookies["domainry_agent_access"].Value, "GET", "/workflow/processes/"+item.ID, nil, "", 200)
					if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
						t.Fatal(err)
					}
					if item.Status != "waiting" || len(detail.Tasks) != 1 || detail.Tasks[0].Status != "open" {
						t.Fatal("browser workflow did not actually wait for approval", detail)
					}
					managedIdentityRequest(t, routes, b.cookies["domainry_agent_access"].Value, "POST", "/workflow/tasks/"+detail.Tasks[0].ID+"/approve", map[string]any{"comment": "浏览器验收实际审批"}, "workflow-browser-approval", 200)
					browserProcessID = item.ID
				}
				return map[string]string{"process_id": browserProcessID}
			},
		})
		items := processes()
		if browserProcessID == "" || len(items) != 2 {
			t.Fatal("browser workflow acceptance incomplete or repeated a start", items)
		}
		for _, item := range items {
			if item.BusinessOutcome != "approved" || item.CompletedAt == "" {
				t.Fatal("browser workflow did not complete actual approval", item)
			}
		}
		messages = agentsdk.ConversationMessagePage{}
		if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+c.ID+"/messages", nil, 200).Body.Bytes(), &messages); err != nil {
			t.Fatal(err)
		}
		// Read the browser's persisted runs, not its displayed prose, to prove
		// it queried the second instance both before and after actual approval.
		seen := map[string]bool{}
		waitingSeen, completedSeen, rejectionSeen := false, false, false
		for _, message := range messages.Items {
			if message.RunID == "" || seen[message.RunID] {
				continue
			}
			seen[message.RunID] = true
			var run agentsdk.ConversationRun
			if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+c.ID+"/runs/"+message.RunID, nil, 200).Body.Bytes(), &run); err != nil {
				t.Fatal(err)
			}
			if run.Interaction != nil && run.Interaction.Tool == "workflow_start" && run.Interaction.Status == "rejected" {
				rejectionSeen = true
			}
			for _, step := range run.Steps {
				for _, call := range step.Calls {
					if call.Name != "workflow_get" || call.Status != "completed" {
						continue
					}
					var e agentsdk.ConversationBusinessEvidence
					var state agentsdk.ConversationWorkflowState
					if json.Unmarshal([]byte(call.ResultPreview), &e) != nil || json.Unmarshal(e.Data, &state) != nil || state.ProcessID != browserProcessID {
						continue
					}
					waitingSeen = waitingSeen || state.Status == "waiting" && !state.Terminal
					completedSeen = completedSeen || state.Terminal && state.BusinessOutcome == "approved"
				}
			}
		}
		if !waitingSeen || !completedSeen || !rejectionSeen {
			t.Fatalf("browser workflow state or rejection not persisted: waiting=%v completed=%v rejected=%v", waitingSeen, completedSeen, rejectionSeen)
		}
		t.Log("Browser confirmed start, actual approval, progress and rejected additional start verified against Runtime and persisted Agent runs")
	}
}
