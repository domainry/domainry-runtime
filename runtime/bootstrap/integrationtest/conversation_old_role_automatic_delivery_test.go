package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	agent "github.com/domainry/domainry-agent-sdk"
	model "github.com/domainry/domainry-report-sdk/model"
)

type mixedOldRoleDeliveryModel struct{ oldRoleSourceModel }
type mixedOldRoleRequest struct {
	AgentID    string                              `json:"agent_id"`
	Brief      agent.ConversationTaskBrief         `json:"brief"`
	References []agent.ConversationResultReference `json:"references"`
}

func (mixedOldRoleDeliveryModel) StreamConversationStep(ctx context.Context, in agent.ConversationStepRequest, emit func(agent.ConversationModelEvent) error) (agent.ConversationStepResult, error) {
	tool := func(id, name string, args any) (agent.ConversationStepResult, error) {
		return agent.ConversationStepResult{FinishReason: "tool_calls", Message: agent.ConversationStepMessage{Role: "assistant", ToolCalls: []agent.ConversationToolCall{{ID: id, Name: name, Arguments: sharedProfessionalJSON(args)}}}}, nil
	}
	stop := func() (agent.ConversationStepResult, error) {
		return agent.ConversationStepResult{FinishReason: "stop", Message: agent.ConversationStepMessage{Role: "assistant", Content: "原报表与分析已核对并交付，等待账号验收。"}}, nil
	}
	var request mixedOldRoleRequest
	var refs []agent.ConversationResultReference
	var detail agent.ConversationDelegationDetail
	id := ""
	dispatched, delivered := false, false
	pages := map[int][]agent.ConversationDelegationSourceSlice{}
	for _, m := range in.Messages {
		if m.Role == "user" && strings.HasPrefix(m.Content, "Old role source:\n") {
			return (oldRoleSourceModel{}).StreamConversationStep(ctx, in, emit)
		}
		if m.Role == "user" && strings.HasPrefix(m.Content, "Delegate mixed old receipts:\n") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(m.Content, "Delegate mixed old receipts:\n")), &request); err != nil {
				return agent.ConversationStepResult{}, err
			}
		}
		if m.Role == "user" {
			if _, after, ok := strings.Cut(m.Content, "Mixed old receipts:\n"); ok {
				if err := json.Unmarshal([]byte(strings.SplitN(after, "\n", 2)[0]), &refs); err != nil {
					return agent.ConversationStepResult{}, err
				}
			}
		}
		if match := regexp.MustCompile(`Delegation ID: (delegation_[a-z0-9]+)`).FindStringSubmatch(m.Content); len(match) > 1 {
			id = match[1]
		}
		if m.Role != "tool" {
			continue
		}
		var wire agent.ConversationToolResult
		if json.Unmarshal([]byte(m.Content), &wire) != nil || wire.Status != "completed" {
			return agent.ConversationStepResult{}, fmt.Errorf("mixed old-role tool %s failed: %s", m.ToolCallID, wire.ErrorCode)
		}
		dispatched = dispatched || m.ToolCallID == "mixed-old-dispatch"
		delivered = delivered || m.ToolCallID == "mixed-old-deliver"
		if m.ToolCallID == "mixed-old-agreement" {
			if err := json.Unmarshal(wire.Content, &detail); err != nil {
				return agent.ConversationStepResult{}, err
			}
		}
		for i := 0; i < 2; i++ {
			if strings.HasPrefix(m.ToolCallID, fmt.Sprintf("mixed-old-page-%d-", i)) {
				var page agent.ConversationDelegationSourceSlice
				if err := json.Unmarshal(wire.Content, &page); err != nil {
					return agent.ConversationStepResult{}, err
				}
				pages[i] = append(pages[i], page)
			}
		}
	}
	if request.AgentID != "" {
		if dispatched {
			return stop()
		}
		if request.Brief.Constraints == nil {
			request.Brief.Constraints = []string{}
		}
		if request.Brief.Assumptions == nil {
			request.Brief.Assumptions = []string{}
		}
		roots := []agent.ConversationRunReference{}
		for _, ref := range request.References {
			roots = append(roots, agent.ConversationRunReference{ConversationID: ref.ConversationID, RunID: ref.RunID, BeforeStep: ref.Step + 2})
		}
		return tool("mixed-old-dispatch", "agent_delegate", map[string]any{"agent_id": request.AgentID, "purpose": "Compare two explicitly admitted original old-role receipts", "brief": request.Brief, "input": "Mixed old receipts:\n" + sharedProfessionalJSON(request.References), "requirements": agent.ConversationAgentRequirements{Tools: []string{"delegation_source_read"}, Sources: roots}, "budget": agent.ConversationTaskBudget{MaxSteps: 12, MaxToolCalls: 12, MaxOutputBytes: 8192, TimeoutSeconds: 180}})
	}
	if id == "" {
		return (oldRoleSourceModel{}).StreamConversationStep(ctx, in, emit)
	}
	if delivered {
		return stop()
	}
	if len(refs) != 2 {
		return agent.ConversationStepResult{}, fmt.Errorf("two original references were not supplied")
	}
	originals := make([]agent.ConversationToolResult, 2)
	for i, ref := range refs {
		sort.Slice(pages[i], func(a, b int) bool { return pages[i][a].Offset < pages[i][b].Offset })
		offset, complete := 0, false
		var raw strings.Builder
		for _, page := range pages[i] {
			if page.DelegationID != id || page.Reference != ref || page.Offset != offset || complete {
				return agent.ConversationStepResult{}, fmt.Errorf("original source pages do not form one exact ordered result")
			}
			raw.WriteString(page.JSONText)
			offset, complete = page.NextOffset, page.Complete
		}
		if !complete {
			return tool(fmt.Sprintf("mixed-old-page-%d-%d", i, offset), "delegation_source_read", agent.ConversationDelegationSourceRead{ID: id, ConversationResultRead: agent.ConversationResultRead{Reference: ref, Offset: offset, MaxBytes: 8192}})
		}
		if err := json.Unmarshal([]byte(raw.String()), &originals[i]); err != nil {
			return agent.ConversationStepResult{}, err
		}
	}
	if detail.ID == "" {
		return tool("mixed-old-agreement", "delegation_get", map[string]string{"id": id})
	}
	var report struct {
		Result model.ReportQueryResult `json:"result"`
	}
	var analysis struct {
		Result model.AnalysisResult `json:"result"`
	}
	if json.Unmarshal(originals[0].Content, &report) != nil || report.Result.Summary.Truncated || json.Unmarshal(originals[1].Content, &analysis) != nil || len(analysis.Result.Rows) != 1 || analysis.Result.Rows[0].Values["total"] == nil {
		return agent.ConversationStepResult{}, fmt.Errorf("original report or analysis is incomplete")
	}
	var total int64
	for _, row := range report.Result.Summary.Rows {
		value, err := strconv.ParseInt(row.Dimensions["balance"], 10, 64)
		if err != nil {
			return agent.ConversationStepResult{}, err
		}
		total += value
	}
	if *analysis.Result.Rows[0].Values["total"] != strconv.FormatInt(total, 10) {
		return agent.ConversationStepResult{}, fmt.Errorf("original report and aggregate disagree")
	}
	conditions := []agent.ConversationConditionAssessment{}
	for i, ref := range refs {
		conditions = append(conditions, agent.ConversationConditionAssessment{Condition: i, Verdict: "met", Basis: "Read the complete unchanged original result and compared its total", Receipts: []agent.ConversationResultReference{ref}})
	}
	return tool("mixed-old-deliver", "delegation_update", map[string]any{"id": id, "update": map[string]any{"expected_revision": detail.Revision, "action": "deliver", "reason": "Compared complete admitted original report and analysis without executing either tool", "delivery": agent.ConversationDelegationDelivery{BriefVersion: detail.Brief.Version, AgreementRevision: detail.AgreementRevision, Summary: fmt.Sprintf("Original report and analysis both equal %d", total), Data: json.RawMessage(sharedProfessionalJSON(map[string]int64{"report_total": total, "analysis_total": total})), Conditions: conditions, Evidence: []agent.ConversationRunReference{}, Unresolved: []string{}}}})
}

func TestMixedOldRoleSourceToolDispatchReadAndAutomaticDelivery(t *testing.T) {
	verifyUnpublishedMixedOldRoleDelivery(t, true)
}

func verifyMixedOldRoleAutomaticDelivery(t *testing.T, b *businessBrowser, receiver agent.ConversationAgent, source agent.Conversation, brief agent.ConversationTaskBrief, refs []agent.ConversationResultReference, originals []agent.ConversationToolResult, assign func(string, ...string), republishContract ...bool) {
	t.Helper()
	roots := []agent.ConversationRunReference{}
	for _, ref := range refs {
		roots = append(roots, agent.ConversationRunReference{ConversationID: ref.ConversationID, RunID: ref.RunID, BeforeStep: ref.Step + 2})
	}
	match := func(want bool) {
		t.Helper()
		var page agent.ConversationAgentMatchPage
		if err := json.Unmarshal(b.call("POST", "/agent/agents/matches", agent.ConversationAgentMatchRequest{ConversationID: source.ID, Requirements: agent.ConversationAgentRequirements{Tools: []string{"delegation_source_read"}, Sources: roots}}, 200).Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if item.AgentID == receiver.ID {
				if item.CanAccept != want || item.SourceAccess != map[bool]string{true: "verified", false: "denied"}[want] {
					t.Fatal("matching did not recheck each original role", item)
				}
				return
			}
		}
		t.Fatal("explicit receiver missing from source match")
	}
	assign("a_results_reader", "b_old_analysis")
	match(false)
	assign("a_results_reader", "b_old_analysis", "c_old_report")
	match(true)
	for _, ref := range refs {
		b.call("POST", "/agent/conversations/"+ref.ConversationID+"/runs/"+ref.RunID+"/result", agent.ConversationResultRead{Reference: ref}, 403)
	}
	var sent agent.ConversationRun
	if err := json.Unmarshal(b.call("POST", "/agent/conversations/"+source.ID+"/messages", agent.ConversationSend{ClientMessageID: "mixed-old-tool-dispatch", Message: "Delegate mixed old receipts:\n" + sharedProfessionalJSON(mixedOldRoleRequest{AgentID: receiver.ID, Brief: brief, References: refs})}, 202).Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	var detail agent.ConversationDelegationDetail
	var execution agent.ConversationRun
	for deadline := time.Now().Add(240 * time.Second); time.Now().Before(deadline); {
		var current agent.ConversationRun
		if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+source.ID+"/runs/"+sent.ID, nil, 200).Body.Bytes(), &current); err != nil {
			t.Fatal(err)
		}
		if i := current.Interaction; i != nil && i.Status == "pending" {
			b.call("POST", "/agent/conversations/"+source.ID+"/runs/"+sent.ID+"/respond", agent.ConversationInteractionResponse{InteractionID: i.ID, ClientID: "approve-" + i.ID, ExpectedRevision: i.Revision, Decision: "approve"}, 200)
		}
		var page agent.ConversationDelegationPage
		if err := json.Unmarshal(b.call("GET", "/agent/delegations", nil, 200).Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > 0 {
			if err := json.Unmarshal(b.call("GET", "/agent/delegations/"+page.Items[0].ID, nil, 200).Body.Bytes(), &detail); err != nil {
				t.Fatal(err)
			}
			if detail.Task != nil && detail.Task.ExecutionRunID != "" {
				if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+detail.ConversationID+"/runs/"+detail.Task.ExecutionRunID, nil, 200).Body.Bytes(), &execution); err != nil {
					t.Fatal(err)
				}
				if execution.Interaction != nil && execution.Interaction.Status == "pending" {
					t.Fatal("original source read or delivery unexpectedly required confirmation", execution.Interaction)
				}
				if execution.Terminal() {
					break
				}
			}
		} else if current.Terminal() {
			t.Fatal("issuer Agent did not dispatch mixed sources", current)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if execution.Status != "completed" || detail.Delivery == nil || detail.Verification == nil || !detail.Verification.Ready || !strings.Contains(detail.Delivery.Summary, "70") {
		t.Fatal("mixed original automatic delivery incomplete", detail, execution)
	}
	readCount := 0
	wrapperRefs := []agent.ConversationResultReference{}
	for _, step := range execution.Steps {
		for _, call := range step.Calls {
			switch call.Name {
			case "delegation_source_read":
				readCount++
				if call.ResultReference == nil {
					t.Fatal("source reader did not record an immutable page reference")
				}
				wrapperRefs = append(wrapperRefs, *call.ResultReference)
			case "delegation_get", "delegation_update":
			default:
				t.Fatal("receiver executed a professional or unrelated tool", call.Name)
			}
		}
	}
	if readCount < 2 || execution.Agent == nil || execution.Agent.DelegationRoleKey != "a_results_reader" {
		t.Fatal("receiver did not independently read originals with its bound reading role", readCount, execution.Agent)
	}
	for i, condition := range detail.Delivery.Conditions {
		if len(condition.Receipts) != 1 || condition.Receipts[0] != refs[i] {
			t.Fatal("delivery replaced original references with wrapper receipts", condition)
		}
	}
	read := func(want int) {
		t.Helper()
		for i, ref := range refs {
			response := b.call("POST", "/agent/delegations/"+detail.ID+"/delivery-result", agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: ref, MaxBytes: 8192}}, want)
			if want == 200 {
				var slice agent.ConversationResultSlice
				var result agent.ConversationToolResult
				if json.Unmarshal(response.Body.Bytes(), &slice) != nil || !slice.Complete || json.Unmarshal([]byte(slice.JSONText), &result) != nil || sharedProfessionalJSON(result) != sharedProfessionalJSON(originals[i]) {
					t.Fatal("automatic delivery changed original complete values", slice)
				}
			}
		}
	}
	read(200)
	// Run completion and the task/delegation finish transaction are observed
	// separately. Reload the relationship after completed task observation so
	// account acceptance never uses the earlier run-poll CAS revision.
	for deadline := time.Now().Add(30 * time.Second); ; {
		if err := json.Unmarshal(b.call("GET", "/agent/delegations/"+detail.ID, nil, 200).Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
		if detail.Task != nil && detail.Task.Status == "completed" {
			if err := json.Unmarshal(b.call("GET", "/agent/delegations/"+detail.ID, nil, 200).Body.Bytes(), &detail); err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("task finish did not commit", detail)
		}
		time.Sleep(25 * time.Millisecond)
	}
	b.call("POST", "/agent/delegations/"+detail.ID+"/decisions", agent.ConversationDelegationUpdate{ClientID: "accept-mixed-old-auto", ExpectedRevision: detail.Revision, Action: "accept_delivery", Reason: "Read both complete original results and checked their actual equal totals", Review: &agent.ConversationDeliveryReview{DeliveryDigest: detail.Verification.DeliveryDigest}}, 200)
	b.f.close()
	downgradeMixedSourcePublicationFixture(t, b.f, detail.ID)
	if len(republishContract) > 0 && republishContract[0] {
		downgradeAgreementRequirementsFixture(t, b.f, detail.ID)
	}
	b.f.open()
	b.session()
	read(200)
	// Current reading policy is independent of the frozen execution role.
	// Same-identity records have no subject row; their immutable task/run
	// authority and the publication's original role must survive this switch.
	assign("a_results_other_reader", "a_results_reader", "b_old_analysis", "c_old_report")
	if len(republishContract) > 0 && republishContract[0] {
		verifyLegacyAgreementRepublishing(t, b, detail.ID, assign)
	}
	read(200)
	for _, ref := range wrapperRefs {
		var page agent.ConversationResultSlice
		var result agent.ConversationToolResult
		var originalPage agent.ConversationDelegationSourceSlice
		if json.Unmarshal(b.call("POST", "/agent/conversations/"+ref.ConversationID+"/runs/"+ref.RunID+"/result", agent.ConversationResultRead{Reference: ref, MaxBytes: 8192}, 200).Body.Bytes(), &page) != nil || !page.Complete || json.Unmarshal([]byte(page.JSONText), &result) != nil || json.Unmarshal(result.Content, &originalPage) != nil || originalPage.DelegationID != detail.ID {
			t.Fatal("equivalent reader could not reauthorize the original source page", page)
		}
	}
	if len(republishContract) > 1 && republishContract[1] {
		verifyLegacyAgreementLifecycle(t, b, detail.ID, execution.ID, wrapperRefs, refs, assign)
	}
	assign("a_results_other_reader", "b_old_analysis", "c_old_report")
	read(403)
	assign("a_results_reader", "b_old_analysis", "c_old_report")
	read(200)
	assign("a_results_field_denied", "a_results_reader", "b_old_analysis", "c_old_report")
	read(403)
	assign("a_results_reader", "b_old_analysis", "c_old_report")
	read(200)
	assign("a_results_reader", "b_old_analysis")
	b.call("POST", "/agent/delegations/"+detail.ID+"/delivery-result", agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: refs[0]}}, 403)
	b.call("POST", "/agent/delegations/"+detail.ID+"/delivery-result", agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: refs[1]}}, 200)
	t.Log("Actual old report and analysis runs, immutable roots, original-role matching, model tool dispatch, bound reading-only execution, complete paginated originals, automatic exact-reference delivery, account acceptance, legacy contract required-root recovery from exact original admission call and delivery recovery from immutable actor, equivalent current reader and original page replay, publisher/original-role/field revocation and restart verified; no receiver professional execution")
}
