package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	agent "github.com/domainry/domainry-agent-sdk"
	report "github.com/domainry/domainry-report-sdk/model"
)

type multipleOwnerSource struct {
	DependencyID string                            `json:"dependency_id"`
	Kind         string                            `json:"kind"`
	Reference    agent.ConversationResultReference `json:"reference"`
}

type multipleOwnerDependencyModel struct{ oldRoleSourceModel }

// The model reads only fields that the Provider actually sends in Content.
// Sources, Identity, the execution ledger and all original proof checks are real.
func (m multipleOwnerDependencyModel) StreamConversationStep(ctx context.Context, in agent.ConversationStepRequest, emit func(agent.ConversationModelEvent) error) (agent.ConversationStepResult, error) {
	var sources []multipleOwnerSource
	for _, message := range in.Messages {
		if message.Role == "user" {
			if _, after, found := strings.Cut(message.Content, "Multiple owner sources:\n"); found {
				if err := json.Unmarshal([]byte(strings.SplitN(after, "\n", 2)[0]), &sources); err != nil {
					return agent.ConversationStepResult{}, err
				}
			}
		}
	}
	if len(sources) == 0 {
		return m.oldRoleSourceModel.StreamConversationStep(ctx, in, emit)
	}
	tool := func(id, name string, args any) (agent.ConversationStepResult, error) {
		return agent.ConversationStepResult{FinishReason: "tool_calls", Message: agent.ConversationStepMessage{Role: "assistant", ToolCalls: []agent.ConversationToolCall{{ID: id, Name: name, Arguments: sharedProfessionalJSON(args)}}}}, nil
	}
	id := ""
	pages := map[int][]agent.ConversationDelegationSourceSlice{}
	locators := map[int]agent.ConversationResultReference{}
	var detail agent.ConversationDelegationDetail
	for _, message := range in.Messages {
		if match := regexp.MustCompile(`Delegation ID: (delegation_[a-z0-9]+)`).FindStringSubmatch(message.Content); len(match) > 1 {
			id = match[1]
		}
		if message.Role != "tool" {
			continue
		}
		var wire struct {
			agent.ConversationToolResult
			Reference agent.ConversationResultReference `json:"reference"`
		}
		if err := json.Unmarshal([]byte(message.Content), &wire); err != nil || wire.Status != "completed" {
			return agent.ConversationStepResult{}, fmt.Errorf("dependency tool %s did not complete: %s", message.ToolCallID, wire.ErrorCode)
		}
		if message.ToolCallID == "multi-owner-deliver" {
			return agent.ConversationStepResult{FinishReason: "stop", Message: agent.ConversationStepMessage{Role: "assistant", Content: "不同所有者的原回执已核对并交付。"}}, nil
		}
		if message.ToolCallID == "multi-owner-agreement" {
			if err := json.Unmarshal(wire.Content, &detail); err != nil {
				return agent.ConversationStepResult{}, err
			}
		}
		for i := range sources {
			if strings.HasPrefix(message.ToolCallID, fmt.Sprintf("multi-owner-page-%d-", i)) {
				var page agent.ConversationDelegationSourceSlice
				if err := json.Unmarshal(wire.Content, &page); err != nil {
					return agent.ConversationStepResult{}, err
				}
				pages[i] = append(pages[i], page)
				if page.Complete {
					locators[i] = wire.Reference
				}
			}
		}
	}
	if id == "" {
		return agent.ConversationStepResult{}, fmt.Errorf("missing current admitted delegation")
	}
	data := map[string]int64{}
	conditions := []agent.ConversationConditionAssessment{}
	for i, source := range sources {
		sort.Slice(pages[i], func(a, b int) bool { return pages[i][a].Offset < pages[i][b].Offset })
		offset, complete := 0, false
		var raw strings.Builder
		for _, page := range pages[i] {
			if page.DelegationID != id || page.DependencyID != source.DependencyID || page.Reference != source.Reference || page.Offset != offset || complete {
				return agent.ConversationStepResult{}, fmt.Errorf("dependency pages changed their original scope or bytes")
			}
			raw.WriteString(page.JSONText)
			offset, complete = page.NextOffset, page.Complete
		}
		if !complete {
			return tool(fmt.Sprintf("multi-owner-page-%d-%d", i, offset), "delegation_source_read", agent.ConversationDelegationSourceRead{ID: id, DependencyID: source.DependencyID, ConversationResultRead: agent.ConversationResultRead{Reference: source.Reference, Offset: offset, MaxBytes: 8192}})
		}
		var original agent.ConversationToolResult
		if err := json.Unmarshal([]byte(raw.String()), &original); err != nil {
			return agent.ConversationStepResult{}, err
		}
		var total int64
		switch source.Kind {
		case "report":
			var body struct {
				Result report.ReportQueryResult `json:"result"`
			}
			if err := json.Unmarshal(original.Content, &body); err != nil || body.Result.Summary.Truncated {
				return agent.ConversationStepResult{}, fmt.Errorf("original report is incomplete")
			}
			for _, row := range body.Result.Summary.Rows {
				value, err := strconv.ParseInt(row.Dimensions["balance"], 10, 64)
				if err != nil {
					return agent.ConversationStepResult{}, err
				}
				total += value
			}
		case "analysis":
			var body struct {
				Result report.AnalysisResult `json:"result"`
			}
			if err := json.Unmarshal(original.Content, &body); err != nil || len(body.Result.Rows) != 1 || body.Result.Rows[0].Values["total"] == nil {
				return agent.ConversationStepResult{}, fmt.Errorf("original analysis is incomplete")
			}
			value, err := strconv.ParseInt(*body.Result.Rows[0].Values["total"], 10, 64)
			if err != nil {
				return agent.ConversationStepResult{}, err
			}
			total = value
		default:
			return agent.ConversationStepResult{}, fmt.Errorf("unknown original source kind")
		}
		if locators[i].SHA256 == "" {
			return agent.ConversationStepResult{}, fmt.Errorf("provider-visible recorded reading receipt missing")
		}
		data[source.Kind+"_total"] = total
		conditions = append(conditions, agent.ConversationConditionAssessment{Condition: i, Verdict: "met", Basis: "Read every exact original page under its adopted upstream agreement", Receipts: []agent.ConversationResultReference{locators[i]}})
	}
	if detail.ID == "" {
		return tool("multi-owner-agreement", "delegation_get", map[string]string{"id": id})
	}
	conditions = append(conditions, agent.ConversationConditionAssessment{Condition: len(sources), Verdict: "met", Basis: "Computed totals from the complete original report and analysis"})
	delivery := agent.ConversationDelegationDelivery{BriefVersion: detail.Brief.Version, AgreementRevision: detail.AgreementRevision, Summary: "Original source totals " + sharedProfessionalJSON(data), Data: json.RawMessage(sharedProfessionalJSON(data)), Conditions: conditions, Evidence: []agent.ConversationRunReference{}, Unresolved: []string{}}
	return tool("multi-owner-deliver", "delegation_update", map[string]any{"id": id, "update": map[string]any{"expected_revision": detail.Revision, "action": "deliver", "reason": "Read independent upstream original receipts without repeating professional execution", "delivery": delivery}})
}

func TestMultipleOwnerProfessionalDependenciesReadOriginalsAndDeliverThroughRealRuntime(t *testing.T) {
	verifyMultipleOwnerDependencyRuntime(t, multipleOwnerIdentityNone)
}

type multipleOwnerIdentityChecks uint8

const (
	multipleOwnerIdentityNone multipleOwnerIdentityChecks = iota
	multipleOwnerIdentityInactive
	multipleOwnerIdentityErasure
)

func verifyMultipleOwnerDependencyRuntime(t *testing.T, identityChecks multipleOwnerIdentityChecks) {
	t.Helper()
	var model agent.ConversationModel = multipleOwnerDependencyModel{}
	var gate *inactiveIdentityModelGate
	if identityChecks != multipleOwnerIdentityNone {
		gate = newInactiveIdentityModelGate()
		t.Cleanup(gate.release)
		model = inactiveIdentityDependencyModel{multipleOwnerDependencyModel{}, gate}
	}
	customize := []func(map[string]any){businessRPCManifest, businessReportManifest, analysisToolsManifest, sharedResultUserRoles, sharedProfessionalCollaborationRoles, resultReadRoles, sameUserProfessionalReaderRoles, oldProfessionalSourceRoles}
	if identityChecks == multipleOwnerIdentityErasure {
		customize = append(customize, multipleOwnerLifecycleRoles)
	}
	f := prepareBusinessWebFixtureWithModel(t, model, customize...)
	if identityChecks == multipleOwnerIdentityErasure {
		f.dataExchangeFactory = multipleOwnerLifecycleDataExchangeFactory()
	}
	f.open()
	a := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	a.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	a.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	a.assign("headquarters_admin")
	a.session()
	b := createSharedResultProducer(t, f, a)
	assign := func(browser *businessBrowser, user, email, selected string, retained ...string) {
		t.Helper()
		assignments := []any{}
		seen := map[string]bool{}
		for _, key := range append([]string{selected, "headquarters_admin"}, retained...) {
			if !seen[key] {
				assignments = append(assignments, map[string]any{"role_id": key})
				seen[key] = true
			}
		}
		managedIdentityRequest(t, sharedResultIdentityHandler(f), a.cookies["domainry_agent_access"].Value, "PUT", "/identity/users/"+user+"/account-and-roles", map[string]any{"user": map[string]any{"name": user, "email": email, "status": "active"}, "assignments": assignments}, fmt.Sprintf("multi-owner-roles-%d", time.Now().UnixNano()), 200)
		browser.call("POST", "/auth/refresh", map[string]any{}, 200)
		browser.session()
	}
	created := managedIdentityRequest(t, sharedResultIdentityHandler(f), a.cookies["domainry_agent_access"].Value, "POST", "/identity/users", map[string]any{"id": "dependency_reader", "name": "Dependency Reader", "email": "dependency@example.com", "status": "active"}, "multi-owner-create-reader", 201)
	var credential struct {
		InitialPassword string `json:"initial_password"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &credential); err != nil || credential.InitialPassword == "" {
		t.Fatal(err)
	}
	managedIdentityRequest(t, sharedResultIdentityHandler(f), a.cookies["domainry_agent_access"].Value, "PUT", "/identity/users/dependency_reader/account-and-roles", map[string]any{"user": map[string]any{"name": "Dependency Reader", "email": "dependency@example.com", "status": "active"}, "assignments": []any{map[string]any{"role_id": "a_results_reader"}, map[string]any{"role_id": "headquarters_admin"}}}, "multi-owner-bind-reader", 200)
	c := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	c.call("POST", "/auth/login", map[string]any{"login": "dependency@example.com", "password": credential.InitialPassword}, 200)
	c.call("POST", "/auth/password/change", map[string]any{"current_password": credential.InitialPassword, "new_password": businessWebPassword}, 200)
	c.session()
	produce := func(browser *businessBrowser, client, name, args string) (agent.Conversation, agent.ConversationResultReference, agent.ConversationToolResult) {
		t.Helper()
		var conversation agent.Conversation
		if err := json.Unmarshal(browser.call("POST", "/agent/conversations", agent.ConversationCreate{ClientID: client}, 200).Body.Bytes(), &conversation); err != nil {
			t.Fatal(err)
		}
		run := browser.runWithMessage(conversation.ID, "Old role source:\n"+sharedProfessionalJSON(agent.ConversationToolCall{ID: client, Name: name, Arguments: args}))
		for _, step := range run.Steps {
			for _, call := range step.Calls {
				if call.Name != name || call.ResultReference == nil {
					continue
				}
				ref := *call.ResultReference
				var page agent.ConversationResultSlice
				if err := json.Unmarshal(browser.call("POST", "/agent/conversations/"+conversation.ID+"/runs/"+run.ID+"/result", agent.ConversationResultRead{Reference: ref, MaxBytes: 8192}, 200).Body.Bytes(), &page); err != nil || !page.Complete {
					t.Fatal("original fixture result incomplete", err)
				}
				var original agent.ConversationToolResult
				if err := json.Unmarshal([]byte(page.JSONText), &original); err != nil {
					t.Fatal(err)
				}
				return conversation, ref, original
			}
		}
		t.Fatal("professional source did not execute", run)
		return conversation, agent.ConversationResultReference{}, agent.ConversationToolResult{}
	}
	assign(a, "admin", "admin@example.com", "c_old_report")
	reportConversation, reportRef, originalReport := produce(a, "multiple-owner-report", "report_query", `{"operation":"query","report_key":"customer_balances"}`)
	assign(b, "professional_source", "professional@example.com", "b_old_analysis")
	_, analysisRef, originalAnalysis := produce(b, "multiple-owner-analysis", "analysis_run", `{"operation":"run","spec":{"dataset_key":"customer","measures":[{"key":"total","function":"sum","field":"balance"}]}}`)
	assign(a, "admin", "admin@example.com", "a_results_reader", "c_old_report")
	assign(b, "professional_source", "professional@example.com", "a_results_reader", "b_old_analysis")
	mode := "owner"
	profile := func(browser *businessBrowser, client string, users, keys []string) agent.ConversationAgent {
		t.Helper()
		var profile agent.ConversationAgent
		if err := json.Unmarshal(browser.call("POST", "/agent/agents", agent.ConversationAgentWrite{ClientID: client, Name: client, Instructions: "Read explicitly admitted originals and submit recorded evidence", Tools: keys, SkillKeys: []string{}, ModelKey: "default", Enabled: true, MaxConcurrent: 2, SharedWithUserIDs: &users, DelegationExecution: &mode}, 200).Body.Bytes(), &profile); err != nil || profile.DelegationRoleKey != "a_results_reader" {
			t.Fatal("actual receiving role", profile, err)
		}
		return profile
	}
	bProfile := profile(b, "multiple-owner-incoming", []string{"admin"}, []string{})
	cProfile := profile(c, "multiple-owner-reader", []string{"professional_source"}, []string{"delegation_source_read", "delegation_get", "delegation_update"})
	create := func(browser *businessBrowser, input agent.ConversationDelegationCreate) agent.ConversationDelegationDetail {
		t.Helper()
		t.Log("Admit", input.ClientID, "from", input.ConversationID, "to", input.AgentID)
		var detail agent.ConversationDelegationDetail
		if err := json.Unmarshal(browser.call("POST", "/agent/delegations", input, 200).Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
		executor := c
		if input.AgentID == bProfile.ID {
			executor = b
		}
		for deadline := time.Now().Add(240 * time.Second); ; {
			if err := json.Unmarshal(executor.call("GET", "/agent/delegations/"+detail.ID, nil, 200).Body.Bytes(), &detail); err != nil {
				t.Fatal(err)
			}
			if detail.Task != nil && detail.Task.Status == "completed" {
				if err := json.Unmarshal(executor.call("GET", "/agent/delegations/"+detail.ID, nil, 200).Body.Bytes(), &detail); err != nil {
					t.Fatal(err)
				}
				return detail
			}
			if detail.Task != nil && (detail.Task.Status == "failed" || detail.Task.Status == "cancelled") {
				var run agent.ConversationRun
				if detail.Task.ExecutionRunID != "" {
					if err := json.Unmarshal(executor.call("GET", "/agent/conversations/"+detail.ConversationID+"/runs/"+detail.Task.ExecutionRunID, nil, 200).Body.Bytes(), &run); err != nil {
						t.Fatal(err)
					}
				}
				t.Fatalf("actual dependency task %s: task error=%s run error=%s steps=%s", detail.Task.Status, detail.Task.ErrorCode, run.ErrorCode, sharedProfessionalJSON(run.Steps))
			}
			if time.Now().After(deadline) {
				t.Fatal("actual dependency task did not complete", detail)
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	brief := agent.ConversationTaskBrief{Version: 1, Goal: "Adopt independent professional originals", Deliverable: "Original report and analysis", CompletionConditions: []string{"Receive originals"}}
	root := create(a, agent.ConversationDelegationCreate{ClientID: "multiple-owner-root", ConversationID: reportConversation.ID, AgentID: bProfile.ID, Purpose: "Explicitly admit original report", Brief: brief, Requirements: agent.ConversationAgentRequirements{Sources: []agent.ConversationRunReference{{ConversationID: reportRef.ConversationID, RunID: reportRef.RunID, BeforeStep: reportRef.Step + 2}}}})
	// The original publisher explicitly includes the third actual reader.
	participants := []agent.ConversationDelegationParticipantInput{{UserID: "dependency_reader", Operations: []string{"view"}}}
	var grantedRoot agent.ConversationDelegationDetail
	if err := json.Unmarshal(a.call("POST", "/agent/delegations/"+root.ID+"/decisions", agent.ConversationDelegationUpdate{ClientID: "multiple-owner-root-audience", ExpectedRevision: root.Revision, Action: "set_participants", Reason: "Share this original upstream with the actual dependency reader", Participants: &participants}, 200).Body.Bytes(), &grantedRoot); err != nil {
		t.Fatal(err)
	}
	root.Revision = grantedRoot.Revision
	b.call("GET", "/agent/conversations/"+root.ConversationID, nil, 200)
	var directory agent.ConversationAgentPage
	if err := json.Unmarshal(b.call("GET", "/agent/agents", nil, 200).Body.Bytes(), &directory); err != nil {
		t.Fatal(err)
	}
	visible := false
	for _, candidate := range directory.Items {
		visible = visible || candidate.ID == cProfile.ID
	}
	if !visible {
		t.Fatal("actual middle actor cannot see explicitly shared receiving configuration", directory)
	}
	b.call("POST", "/agent/agents/matches", agent.ConversationAgentMatchRequest{ConversationID: root.ConversationID, Requirements: agent.ConversationAgentRequirements{Sources: []agent.ConversationRunReference{{ConversationID: analysisRef.ConversationID, RunID: analysisRef.RunID, BeforeStep: analysisRef.Step + 2}}}}, 200)
	c.call("GET", "/agent/delegations/"+root.ID+"/executions", nil, 403)
	c.call("POST", "/agent/delegations/"+root.ID+"/decisions", agent.ConversationDelegationUpdate{ClientID: "multiple-owner-no-upstream-control", ExpectedRevision: root.Revision, Action: "cancel", Reason: "A view-only audience cannot manage the original upstream"}, 403)
	upstream := create(b, agent.ConversationDelegationCreate{ClientID: "multiple-owner-upstream", ConversationID: root.ConversationID, AgentID: cProfile.ID, Purpose: "Explicitly admit independently owned analysis", Brief: brief, Requirements: agent.ConversationAgentRequirements{Sources: []agent.ConversationRunReference{{ConversationID: analysisRef.ConversationID, RunID: analysisRef.RunID, BeforeStep: analysisRef.Step + 2}}}})
	sources := []multipleOwnerSource{{DependencyID: root.ID, Kind: "report", Reference: reportRef}, {DependencyID: upstream.ID, Kind: "analysis", Reference: analysisRef}}
	conditions := []string{"Read complete original report", "Read complete original analysis", "Calculate actual totals"}
	rules := []agent.ConversationCompletionRule{}
	for i, source := range sources {
		rules = append(rules, agent.ConversationCompletionRule{Condition: i, Kind: "receipt", Tool: "delegation_source_read", ArgumentsSchema: json.RawMessage(sharedProfessionalJSON(map[string]any{"type": "object", "properties": map[string]any{"dependency_id": map[string]any{"const": source.DependencyID}, "reference": map[string]any{"const": source.Reference}}, "required": []string{"dependency_id", "reference"}})), ResultSchema: json.RawMessage(sharedProfessionalJSON(map[string]any{"type": "object", "properties": map[string]any{"dependency_id": map[string]any{"const": source.DependencyID}, "reference": map[string]any{"const": source.Reference}, "complete": map[string]any{"const": true}}, "required": []string{"dependency_id", "reference", "complete"}}))})
	}
	rules = append(rules, agent.ConversationCompletionRule{Condition: 2, Kind: "data", Schema: json.RawMessage(`{"type":"object","properties":{"report_total":{"const":70},"analysis_total":{"const":70}},"required":["report_total","analysis_total"]}`)})
	finalBrief := agent.ConversationTaskBrief{Version: 1, Goal: "Read both separately published originals and compare actual totals", Deliverable: "Verified totals and original reading receipts", CompletionConditions: conditions, VerificationRules: rules}
	readerProfile := profile(c, "multiple-owner-final-reader", []string{}, []string{"delegation_source_read", "delegation_get", "delegation_update"})
	final := create(c, agent.ConversationDelegationCreate{ClientID: "multiple-owner-final", ConversationID: upstream.ConversationID, AgentID: readerProfile.ID, Purpose: "Read two adopted upstream original results", Brief: finalBrief, Input: "Multiple owner sources:\n" + sharedProfessionalJSON(sources), Dependencies: []agent.ConversationDependencyInput{{DelegationID: root.ID, BriefVersion: root.Brief.Version, AgreementRevision: root.AgreementRevision}, {DelegationID: upstream.ID, BriefVersion: upstream.Brief.Version, AgreementRevision: upstream.AgreementRevision}}, Budget: agent.ConversationTaskBudget{MaxSteps: 12, MaxToolCalls: 12, MaxOutputBytes: 8192, TimeoutSeconds: 180}})
	if final.Delivery == nil || final.Verification == nil || !final.Verification.Ready || !strings.Contains(final.Delivery.Summary, "70") {
		t.Fatal("independent source reading did not verify actual delivery", final)
	}
	for _, source := range sources {
		c.call("POST", "/agent/conversations/"+source.Reference.ConversationID+"/runs/"+source.Reference.RunID+"/result", agent.ConversationResultRead{Reference: source.Reference}, 404)
	}
	var execution agent.ConversationRun
	if err := json.Unmarshal(c.call("GET", "/agent/conversations/"+final.ConversationID+"/runs/"+final.Task.ExecutionRunID, nil, 200).Body.Bytes(), &execution); err != nil {
		t.Fatal(err)
	}
	readCalls := 0
	for _, step := range execution.Steps {
		for _, call := range step.Calls {
			if call.Name == "delegation_source_read" {
				readCalls++
			}
			if call.Name != "delegation_source_read" && call.Name != "delegation_get" && call.Name != "delegation_update" {
				t.Fatal("reader repeated professional execution", call.Name)
			}
		}
	}
	if readCalls < 2 || execution.Agent == nil || execution.Agent.DelegationRoleKey != "a_results_reader" {
		t.Fatal("actual independent read execution", execution)
	}
	read := func(wants ...int) {
		t.Helper()
		for i, condition := range final.Delivery.Conditions[:2] {
			want := wants[0]
			if len(wants) > i {
				want = wants[i]
			}
			if len(condition.Receipts) != 1 {
				t.Fatal("missing actual recorded reading receipt", condition)
			}
			ref := condition.Receipts[0]
			response := c.call("POST", "/agent/delegations/"+final.ID+"/delivery-result", agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: ref, MaxBytes: 8192}}, want)
			if want == 200 {
				var page agent.ConversationResultSlice
				var wrapper agent.ConversationToolResult
				var originalPage agent.ConversationDelegationSourceSlice
				var original agent.ConversationToolResult
				if json.Unmarshal(response.Body.Bytes(), &page) != nil || !page.Complete || json.Unmarshal([]byte(page.JSONText), &wrapper) != nil || json.Unmarshal(wrapper.Content, &originalPage) != nil || originalPage.DependencyID != sources[i].DependencyID || originalPage.Reference != sources[i].Reference || json.Unmarshal([]byte(originalPage.JSONText), &original) != nil || sharedProfessionalJSON(original) != sharedProfessionalJSON([]agent.ConversationToolResult{originalReport, originalAnalysis}[i]) {
					t.Fatal("original proof or reading receipt bytes changed", page)
				}
			}
		}
	}
	read(200)
	c.call("POST", "/agent/delegations/"+final.ID+"/decisions", agent.ConversationDelegationUpdate{ClientID: "multiple-owner-accept", ExpectedRevision: final.Revision, Action: "accept_delivery", Reason: "Read complete originals and checked both actual totals", Review: &agent.ConversationDeliveryReview{DeliveryDigest: final.Verification.DeliveryDigest}}, 200)
	f.close()
	f.open()
	a.session()
	b.session()
	c.session()
	read(200)
	assign(a, "admin", "admin@example.com", "a_results_reader")
	read(403)
	assign(a, "admin", "admin@example.com", "a_results_reader", "c_old_report")
	read(200)
	assign(a, "admin", "admin@example.com", "headquarters_admin", "c_old_report")
	read(403)
	assign(a, "admin", "admin@example.com", "a_results_reader", "c_old_report")
	read(200)
	assign(b, "professional_source", "professional@example.com", "a_results_reader")
	read(200, 403)
	assign(b, "professional_source", "professional@example.com", "a_results_reader", "b_old_analysis")
	read(200)
	assign(b, "professional_source", "professional@example.com", "headquarters_admin", "b_old_analysis")
	read(200, 403)
	assign(b, "professional_source", "professional@example.com", "a_results_reader", "b_old_analysis")
	read(200)
	assign(c, "dependency_reader", "dependency@example.com", "a_results_field_denied", "a_results_reader")
	read(403)
	assign(c, "dependency_reader", "dependency@example.com", "a_results_reader")
	read(200)
	setAudience := func(client string, grants []agent.ConversationDelegationParticipantInput) {
		t.Helper()
		var current agent.ConversationDelegationDetail
		if err := json.Unmarshal(a.call("GET", "/agent/delegations/"+root.ID, nil, 200).Body.Bytes(), &current); err != nil {
			t.Fatal(err)
		}
		a.call("POST", "/agent/delegations/"+root.ID+"/decisions", agent.ConversationDelegationUpdate{ClientID: client, ExpectedRevision: current.Revision, Action: "set_participants", Reason: "Explicitly change the original upstream reading audience", Participants: &grants}, 200)
	}
	setAudience("multiple-owner-withdraw-audience", []agent.ConversationDelegationParticipantInput{})
	read(403)
	setAudience("multiple-owner-restore-audience", participants)
	read(200)
	var accepted agent.ConversationDelegationDetail
	if err := json.Unmarshal(c.call("GET", "/agent/delegations/"+final.ID, nil, 200).Body.Bytes(), &accepted); err != nil || accepted.Delivery == nil || accepted.Task == nil || accepted.Task.Status != "completed" || accepted.Task.ExecutionRunID != final.Task.ExecutionRunID || accepted.Status != "accepted_delivery" || sharedProfessionalJSON(accepted.Delivery) != sharedProfessionalJSON(final.Delivery) {
		t.Fatalf("accepted delivery changed after source revocation: status=%s task=%s delivery-equal=%t decode=%v", accepted.Status, sharedProfessionalJSON(accepted.Task), sharedProfessionalJSON(accepted.Delivery) == sharedProfessionalJSON(final.Delivery), err)
	}
	if identityChecks == multipleOwnerIdentityInactive {
		verifyInactiveMultiOwnerIdentities(t, f, a, b, c, reportConversation, cProfile, final, gate, read, profile)
	}
	if identityChecks == multipleOwnerIdentityErasure {
		verifyMultiOwnerSubjectErasure(t, f, a, b, c, reportConversation, root, final, gate, read, profile)
	}
	t.Log("Two actual professional owners, independent original and publication roles, explicit third audience, adopted upstream namespaces, Provider-visible own reading receipts, automatic verified delivery, original SHA/bytes, private endpoint denial, no repeated professional call, original-role/publisher-role/reader-field/audience withdrawal, account acceptance and restart verified")
}
