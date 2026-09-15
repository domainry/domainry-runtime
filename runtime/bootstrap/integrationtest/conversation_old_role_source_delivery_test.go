package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	agent "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	"github.com/domainry/domainry-runtime/runtime/bootstrap"
)

// Only the model wire is deterministic. Sources, proof generation, execution
// ledgers, Identity role selection, publication and all HTTP reads remain real.
type oldRoleSourceModel struct{ businessWebModel }

func (oldRoleSourceModel) StreamConversationStep(_ context.Context, in agent.ConversationStepRequest, _ func(agent.ConversationModelEvent) error) (agent.ConversationStepResult, error) {
	for _, message := range in.Messages {
		if message.Role == "tool" {
			var result agent.ConversationToolResult
			if json.Unmarshal([]byte(message.Content), &result) != nil || result.Status != "completed" {
				return agent.ConversationStepResult{}, fmt.Errorf("old-role source did not complete")
			}
			return agent.ConversationStepResult{FinishReason: "stop", Message: agent.ConversationStepMessage{Role: "assistant", Content: "原回执已保存。"}}, nil
		}
	}
	for _, message := range in.Messages {
		if message.Role != "user" || !strings.HasPrefix(message.Content, "Old role source:\n") {
			continue
		}
		var call agent.ConversationToolCall
		if err := json.Unmarshal([]byte(strings.TrimPrefix(message.Content, "Old role source:\n")), &call); err != nil {
			return agent.ConversationStepResult{}, err
		}
		return agent.ConversationStepResult{FinishReason: "tool_calls", Message: agent.ConversationStepMessage{Role: "assistant", ToolCalls: []agent.ConversationToolCall{call}}}, nil
	}
	return agent.ConversationStepResult{FinishReason: "stop", Message: agent.ConversationStepMessage{Role: "assistant", Content: "等待账号提交并核对原回执。"}}, nil
}

func oldProfessionalSourceRoles(m map[string]any) {
	for _, value := range m["roles"].([]any) {
		role := value.(map[string]any)
		if role["key"] != "headquarters_admin" {
			continue
		}
		for _, key := range []string{"c_old_report", "b_old_analysis"} {
			raw, _ := json.Marshal(role)
			var clone map[string]any
			_ = json.Unmarshal(raw, &clone)
			clone["key"], clone["name"] = key, key
			m["roles"] = append(m["roles"].([]any), clone)
		}
		for _, value := range m["roles"].([]any) {
			reader := value.(map[string]any)
			if reader["key"] != "a_results_reader" {
				continue
			}
			raw, _ := json.Marshal(reader)
			var clone map[string]any
			_ = json.Unmarshal(raw, &clone)
			clone["key"], clone["name"] = "a_results_other_reader", "Equivalent receipt reader"
			m["roles"] = append(m["roles"].([]any), clone)
			break
		}
		return
	}
}

func TestUnpublishedMixedOldRoleReportAndAnalysisFirstDeliveryThroughRealHTTP(t *testing.T) {
	verifyUnpublishedMixedOldRoleDelivery(t, false)
}

func TestLegacyManualMixedOldRoleDeliveryExplicitRepublishingThroughRealHTTP(t *testing.T) {
	verifyUnpublishedMixedOldRoleDelivery(t, false, true)
}

func TestLegacyMixedOldRoleAgreementExplicitRepublishingThroughRealHTTP(t *testing.T) {
	verifyUnpublishedMixedOldRoleDelivery(t, true, true)
}

func TestLegacyAgreementUpdateRestartResumeAndOriginalHistoryThroughRealHTTP(t *testing.T) {
	verifyUnpublishedMixedOldRoleDelivery(t, true, true, true)
}

func verifyUnpublishedMixedOldRoleDelivery(t *testing.T, automatic bool, republish ...bool) {
	t.Helper()
	var model agent.ConversationModel = oldRoleSourceModel{}
	if automatic {
		model = mixedOldRoleDeliveryModel{}
	}
	f := newBusinessWebFixtureWithModel(t, model, businessRPCManifest, businessReportManifest, analysisToolsManifest, sharedResultUserRoles, sharedProfessionalCollaborationRoles, resultReadRoles, sameUserProfessionalReaderRoles, oldProfessionalSourceRoles)
	b := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	b.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	b.assign("headquarters_admin")
	b.session()
	assign := func(selected string, retained ...string) {
		t.Helper()
		assignments := []any{map[string]any{"role_id": selected}, map[string]any{"role_id": "headquarters_admin"}}
		for _, key := range retained {
			assignments = append(assignments, map[string]any{"role_id": key})
		}
		managedIdentityRequest(t, sharedResultIdentityHandler(f), b.cookies["domainry_agent_access"].Value, "PUT", "/identity/users/admin/account-and-roles", map[string]any{"user": map[string]any{"name": "Admin", "email": "admin@example.com", "status": "active"}, "assignments": assignments}, fmt.Sprintf("old-source-role-%d", time.Now().UnixNano()), 200)
		b.call("POST", "/auth/refresh", map[string]any{}, 200)
		b.session()
		me, err := f.identity.Authentication().CurrentSession(t.Context(), identity.CurrentSessionRequest{AccessToken: b.cookies["domainry_agent_access"].Value})
		if err != nil || me.DefaultRole != selected {
			t.Fatal("fixture did not select the actual source/reader role", selected, me.DefaultRole, err)
		}
	}
	produce := func(client, name, args string) (agent.ConversationResultReference, agent.ConversationToolResult) {
		t.Helper()
		var conversation agent.Conversation
		if err := json.Unmarshal(b.call("POST", "/agent/conversations", agent.ConversationCreate{ClientID: client}, 200).Body.Bytes(), &conversation); err != nil {
			t.Fatal(err)
		}
		run := b.runWithMessage(conversation.ID, "Old role source:\n"+sharedProfessionalJSON(agent.ConversationToolCall{ID: client, Name: name, Arguments: args}))
		for _, step := range run.Steps {
			for _, call := range step.Calls {
				if call.ResultReference == nil || call.Name != name {
					continue
				}
				ref := *call.ResultReference
				var page agent.ConversationResultSlice
				if err := json.Unmarshal(b.call("POST", "/agent/conversations/"+ref.ConversationID+"/runs/"+ref.RunID+"/result", agent.ConversationResultRead{Reference: ref, MaxBytes: 8192}, 200).Body.Bytes(), &page); err != nil || !page.Complete {
					t.Fatal("original result was not completely read", page, err)
				}
				var result agent.ConversationToolResult
				if err := json.Unmarshal([]byte(page.JSONText), &result); err != nil {
					t.Fatal(err)
				}
				return ref, result
			}
		}
		t.Fatal("actual source run did not expose its server-issued reference", run)
		return agent.ConversationResultReference{}, agent.ConversationToolResult{}
	}
	assign("c_old_report")
	reportRef, originalReport := produce("old-role-report", "report_query", `{"operation":"query","report_key":"customer_balances"}`)
	assign("b_old_analysis", "c_old_report")
	analysisRef, originalAnalysis := produce("old-role-analysis", "analysis_run", `{"operation":"run","spec":{"dataset_key":"customer","measures":[{"key":"total","function":"sum","field":"balance"}]}}`)
	var report struct {
		Result reportmodel.ReportQueryResult `json:"result"`
	}
	var analysis struct {
		Result reportmodel.AnalysisResult `json:"result"`
	}
	if json.Unmarshal(originalReport.Content, &report) != nil || json.Unmarshal(originalAnalysis.Content, &analysis) != nil || len(analysis.Result.Rows) != 1 || analysis.Result.Rows[0].Values["total"] == nil {
		t.Fatal("actual report and analysis values are missing")
	}
	var sum int64
	for _, row := range report.Result.Summary.Rows {
		value, err := strconv.ParseInt(row.Dimensions["balance"], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		sum += value
	}
	if report.Result.Summary.Truncated || sum != 70 || *analysis.Result.Rows[0].Values["total"] != strconv.FormatInt(sum, 10) {
		t.Fatal("original complete query and analysis do not both equal 70", report, analysis)
	}
	assign("a_results_reader", "b_old_analysis", "c_old_report")
	mode := "owner"
	receiverTools := []string{}
	if automatic {
		receiverTools = []string{"delegation_source_read", "delegation_get", "delegation_update"}
	}
	var receiver agent.ConversationAgent
	if err := json.Unmarshal(b.call("POST", "/agent/agents", agent.ConversationAgentWrite{ClientID: "old-source-reader", Name: "Original receipt reader", Instructions: "Read and submit original receipts", Tools: receiverTools, SkillKeys: []string{}, ModelKey: "default", Enabled: true, MaxConcurrent: 1, DelegationExecution: &mode}, 200).Body.Bytes(), &receiver); err != nil || receiver.DelegationRoleKey != "a_results_reader" {
		t.Fatal("receiver did not bind the independent results-only role", receiver, err)
	}
	var source agent.Conversation
	if err := json.Unmarshal(b.call("POST", "/agent/conversations", agent.ConversationCreate{ClientID: "mixed-old-source-delegation"}, 200).Body.Bytes(), &source); err != nil {
		t.Fatal(err)
	}
	brief := agent.ConversationTaskBrief{Version: 1, Goal: "Submit exact old report and analysis results", Deliverable: "Both original receipts", CompletionConditions: []string{"Original report", "Original analysis"}, VerificationRules: []agent.ConversationCompletionRule{{Condition: 0, Kind: "receipt", Tool: "report_query", ResultSchema: json.RawMessage(`{"type":"object","required":["result"]}`)}, {Condition: 1, Kind: "receipt", Tool: "analysis_run", ResultSchema: json.RawMessage(`{"type":"object","required":["result"]}`)}}}
	if automatic {
		verifyMixedOldRoleAutomaticDelivery(t, b, receiver, source, brief, []agent.ConversationResultReference{reportRef, analysisRef}, []agent.ConversationToolResult{originalReport, originalAnalysis}, assign, republish...)
		return
	}
	var detail agent.ConversationDelegationDetail
	if err := json.Unmarshal(b.call("POST", "/agent/delegations", agent.ConversationDelegationCreate{ClientID: "mixed-old-source", ConversationID: source.ID, AgentID: receiver.ID, Purpose: "Read original professional receipts", Brief: brief}, 200).Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(90 * time.Second)
	for detail.Task == nil || detail.Task.Status != "completed" {
		if time.Now().After(deadline) {
			t.Fatal("receiver did not complete its independent read task", detail)
		}
		time.Sleep(100 * time.Millisecond)
		if err := json.Unmarshal(b.call("GET", "/agent/delegations/"+detail.ID, nil, 200).Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
	}
	readPath := "/agent/delegations/" + detail.ID + "/delivery-result"
	for _, ref := range []agent.ConversationResultReference{reportRef, analysisRef} {
		b.call("POST", readPath, agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: ref}}, 403)
		b.call("POST", "/agent/conversations/"+ref.ConversationID+"/runs/"+ref.RunID+"/result", agent.ConversationResultRead{Reference: ref}, 403)
	}
	delivery := agent.ConversationDelegationDelivery{BriefVersion: detail.Brief.Version, AgreementRevision: detail.AgreementRevision, Summary: "Two unchanged original professional receipts", Conditions: []agent.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "Original report receipt", Receipts: []agent.ConversationResultReference{reportRef}}, {Condition: 1, Verdict: "met", Basis: "Original aggregate receipt", Receipts: []agent.ConversationResultReference{analysisRef}}}}
	// Failed first publication must leave the delegation and receipt grants intact.
	assign("a_results_reader", "b_old_analysis")
	b.call("POST", "/agent/delegations/"+detail.ID+"/decisions", agent.ConversationDelegationUpdate{ClientID: "revoked-old-source-delivery", ExpectedRevision: detail.Revision, Action: "deliver", Reason: "Try to publish a source after its original role was removed", Delivery: &delivery}, 403)
	assign("a_results_reader", "b_old_analysis", "c_old_report")
	for _, ref := range []agent.ConversationResultReference{reportRef, analysisRef} {
		b.call("POST", readPath, agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: ref}}, 403)
	}
	// The detail projection can observe the newly completed task after loading
	// its delegation. Refresh the CAS revision after terminal observation and
	// role changes rather than submitting that earlier admission/poll version.
	var current agent.ConversationDelegationDetail
	if err := json.Unmarshal(b.call("GET", "/agent/delegations/"+detail.ID, nil, 200).Body.Bytes(), &current); err != nil || current.Delivery != nil {
		t.Fatal("failed first publication exposed a delivery", current, err)
	}
	detail = current
	if err := json.Unmarshal(b.call("POST", "/agent/delegations/"+detail.ID+"/decisions", agent.ConversationDelegationUpdate{ClientID: "first-mixed-old-source-delivery", ExpectedRevision: detail.Revision, Action: "deliver", Reason: "Publish two unchanged original receipts under the current results-only role", Delivery: &delivery}, 200).Body.Bytes(), &detail); err != nil || detail.Verification == nil || !detail.Verification.Ready {
		t.Fatal("first mixed-role submission did not verify both original proofs", detail, err)
	}
	read := func(want int) {
		t.Helper()
		for i, ref := range []agent.ConversationResultReference{reportRef, analysisRef} {
			response := b.call("POST", readPath, agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: ref, MaxBytes: 8192}}, want)
			if want != 200 {
				continue
			}
			var page agent.ConversationResultSlice
			var result agent.ConversationToolResult
			if json.Unmarshal(response.Body.Bytes(), &page) != nil || !page.Complete || json.Unmarshal([]byte(page.JSONText), &result) != nil || sharedProfessionalJSON(result) != sharedProfessionalJSON([]agent.ConversationToolResult{originalReport, originalAnalysis}[i]) {
				t.Fatal("shared read changed the original receipt", page)
			}
		}
	}
	read(200)
	if len(republish) > 0 && republish[0] {
		verifyExplicitLegacyMixedSourceRepublishing(t, f, b, detail, read, assign)
	}
	for _, ref := range []agent.ConversationResultReference{reportRef, analysisRef} {
		var raw agent.ConversationRun
		if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+ref.ConversationID+"/runs/"+ref.RunID, nil, 200).Body.Bytes(), &raw); err != nil || raw.AccessError == "" || len(raw.Steps) != 0 {
			t.Fatal("mixed-role publication exposed ordinary execution data", raw, err)
		}
		b.call("POST", "/agent/conversations/"+ref.ConversationID+"/runs/"+ref.RunID+"/result", agent.ConversationResultRead{Reference: ref}, 403)
	}
	f.close()
	f.open()
	b.session()
	read(200)
	assign("a_results_field_denied", "a_results_reader", "b_old_analysis", "c_old_report")
	sourceOwner, err := bootstrap.ConversationBusinessSource(f.runtime)
	if err != nil {
		t.Fatal(err)
	}
	owner, ok := sourceOwner.(interface {
		AuthorizeSharedAnalysisResultRead(context.Context, reportmodel.AnalysisResultAuthorization, agent.ConversationAuthority, agent.ConversationAuthority) error
	})
	if !ok {
		t.Fatal("actual owner shared analysis reader unavailable")
	}
	actualReader := agent.ConversationAuthority{Known: true, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, UserID: "admin", RoleKey: "a_results_field_denied"}
	originalProducer := actualReader
	originalProducer.RoleKey = "b_old_analysis"
	var denied *agent.Error
	if err := owner.AuthorizeSharedAnalysisResultRead(t.Context(), reportmodel.AnalysisResultAuthorization{Request: analysis.Result.Spec, Result: analysis.Result}, actualReader, originalProducer); !errors.As(err, &denied) || denied.Class != "forbidden" {
		t.Fatal("independent owner read did not classify current field denial", err)
	}
	read(403)
	assign("a_results_reader", "b_old_analysis", "c_old_report")
	read(200)
	assign("a_results_reader", "b_old_analysis")
	b.call("POST", readPath, agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: reportRef}}, 403)
	b.call("POST", readPath, agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: analysisRef}}, 200)
}
