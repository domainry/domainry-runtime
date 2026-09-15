package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	agent "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	bootstrapruntime "github.com/domainry/domainry-runtime/runtime/bootstrap/runtime"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

type sharedBusinessWriteDeliveryModel struct{ businessWebModel }

func (sharedBusinessWriteDeliveryModel) ConversationModelIdentity() agent.ConversationModelIdentity {
	return agent.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "shared-business-write-delivery", Fingerprint: "shared-business-write-delivery-v1"}
}

// Only decisions are fixtures: contracts, confirmations, identities, effects,
// workflow state and receipt references come from the actual host execution.
func (sharedBusinessWriteDeliveryModel) StreamConversationStep(_ context.Context, in agent.ConversationStepRequest, _ func(agent.ConversationModelEvent) error) (agent.ConversationStepResult, error) {
	tool := func(id, name string, args any) (agent.ConversationStepResult, error) {
		return agent.ConversationStepResult{FinishReason: "tool_calls", Message: agent.ConversationStepMessage{Role: "assistant", ToolCalls: []agent.ConversationToolCall{{ID: id, Name: name, Arguments: sharedProfessionalJSON(args)}}}}, nil
	}
	stop := func() (agent.ConversationStepResult, error) {
		return agent.ConversationStepResult{FinishReason: "stop", Message: agent.ConversationStepMessage{Role: "assistant", Content: "已提交客户创建与流程受理原回执，流程仍等待审批。"}}, nil
	}
	target, id, dispatched, delivered := "", "", false, false
	var detail agent.ConversationDelegationDetail
	refs := map[string]agent.ConversationResultReference{}
	data := map[string]json.RawMessage{}
	for _, message := range in.Messages {
		if message.Role == "user" && strings.HasPrefix(message.Content, "Delegate business writes:\n") {
			target = strings.TrimPrefix(message.Content, "Delegate business writes:\n")
		}
		if match := regexp.MustCompile(`Delegation ID: (delegation_[a-z0-9]+)`).FindStringSubmatch(message.Content); len(match) > 1 {
			id = match[1]
		}
		if message.Role != "tool" {
			continue
		}
		var wire struct {
			agent.ConversationToolResult
			Reference *agent.ConversationResultReference `json:"reference"`
		}
		if json.Unmarshal([]byte(message.Content), &wire) != nil || wire.Status != "completed" {
			return agent.ConversationStepResult{}, fmt.Errorf("business write tool failed: %s", message.ToolCallID)
		}
		dispatched = dispatched || message.ToolCallID == "write-dispatch"
		delivered = delivered || message.ToolCallID == "write-deliver"
		if wire.Reference != nil {
			refs[message.ToolCallID] = *wire.Reference
		}
		var evidence agent.ConversationBusinessEvidence
		if json.Unmarshal(wire.Content, &evidence) == nil && evidence.Version == 1 {
			data[message.ToolCallID] = evidence.Data
		}
		if message.ToolCallID == "write-agreement" {
			if err := json.Unmarshal(wire.Content, &detail); err != nil {
				return agent.ConversationStepResult{}, err
			}
		}
	}
	keys := []string{"business_catalog", "invoke_action", "business_catalog", "workflow_start", "workflow_get"}
	callIDs := []string{"write-action-contract", "write-create", "write-workflow-contract", "write-start", "write-progress"}
	if target != "" && id == "" {
		if dispatched {
			return stop()
		}
		brief := agent.ConversationTaskBrief{Version: 1, Goal: "Create one customer and start one review after the actual execution user confirms each exact call", Deliverable: "Original contracts, creation receipt, workflow acceptance and actual waiting state", Constraints: []string{}, Assumptions: []string{}, CompletionConditions: []string{"Discover current action", "Create one customer", "Discover current workflow", "Start one review", "Report actual pending approval"}}
		for i, key := range keys {
			rule := agent.ConversationCompletionRule{Condition: i, Kind: "receipt", Tool: key}
			if key == "workflow_start" {
				rule.Completion = "accepted"
			}
			brief.VerificationRules = append(brief.VerificationRules, rule)
		}
		return tool("write-dispatch", "agent_delegate", map[string]any{"agent_id": target, "purpose": "Delegate confirmed business writes to the configuration owner's independent identity", "brief": brief, "budget": agent.ConversationTaskBudget{MaxSteps: 12, MaxToolCalls: 12, MaxOutputBytes: 8192, TimeoutSeconds: 90}, "input": "Create one customer named Agent shared confirmed customer; start one agent_review for shared confirmed review; request exact confirmations, deliver original receipts and report the actual pending approval", "requirements": agent.ConversationAgentRequirements{Tools: []string{"business_catalog", "invoke_action", "workflow_start", "workflow_get"}}})
	}
	if id == "" || delivered {
		return stop()
	}
	if _, ok := refs[callIDs[0]]; !ok {
		return tool(callIDs[0], keys[0], agent.ConversationBusinessCatalogQuery{Kind: "actions", ObjectKey: "customer", ActionKey: "customer.register"})
	}
	if _, ok := refs[callIDs[1]]; !ok {
		var catalog agent.ConversationBusinessCatalogPage
		if json.Unmarshal(data[callIDs[0]], &catalog) != nil || len(catalog.Actions) != 1 || catalog.Actions[0].ExecutionVersion == "" {
			return agent.ConversationStepResult{}, fmt.Errorf("actual action contract missing")
		}
		return tool(callIDs[1], keys[1], agent.ConversationBusinessAction{ObjectKey: "customer", ActionKey: "customer.register", Version: catalog.Actions[0].ExecutionVersion, Data: json.RawMessage(`{"name":"Agent shared confirmed customer","owner":"professional_source"}`)})
	}
	if _, ok := refs[callIDs[2]]; !ok {
		return tool(callIDs[2], keys[2], agent.ConversationBusinessCatalogQuery{Kind: "workflows", WorkflowKey: "agent_review"})
	}
	if _, ok := refs[callIDs[3]]; !ok {
		var catalog agent.ConversationBusinessCatalogPage
		if json.Unmarshal(data[callIDs[2]], &catalog) != nil || len(catalog.Workflows) != 1 || catalog.Workflows[0].ExecutionVersion == "" {
			return agent.ConversationStepResult{}, fmt.Errorf("actual workflow contract missing")
		}
		return tool(callIDs[3], keys[3], agent.ConversationWorkflowStart{WorkflowKey: "agent_review", Version: catalog.Workflows[0].ExecutionVersion, Data: json.RawMessage(`{"reason":"Agent shared confirmed review"}`)})
	}
	if _, ok := refs[callIDs[4]]; !ok {
		var receipt agent.ConversationWorkflowReceipt
		if json.Unmarshal(data[callIDs[3]], &receipt) != nil || receipt.Status != "accepted" || receipt.ProcessID == "" {
			return agent.ConversationStepResult{}, fmt.Errorf("actual workflow acceptance missing")
		}
		return tool(callIDs[4], keys[4], agent.ConversationWorkflowGet{WorkflowKey: receipt.WorkflowKey, ProcessID: receipt.ProcessID})
	}
	if detail.ID == "" {
		return tool("write-agreement", "delegation_get", map[string]string{"id": id})
	}
	conditions := []agent.ConversationConditionAssessment{}
	for i, callID := range callIDs {
		conditions = append(conditions, agent.ConversationConditionAssessment{Condition: i, Verdict: "met", Basis: "Source-owned original " + keys[i] + " receipt", Receipts: []agent.ConversationResultReference{refs[callID]}})
	}
	return tool("write-deliver", "delegation_update", map[string]any{"id": id, "update": map[string]any{"expected_revision": detail.Revision, "action": "deliver", "reason": "Confirmed and completed creation and review acceptance exactly once", "delivery": agent.ConversationDelegationDelivery{BriefVersion: detail.Brief.Version, AgreementRevision: detail.AgreementRevision, Summary: "Original creation and workflow acceptance; business approval pending", Data: json.RawMessage(`{"business_receipts":5,"workflow_complete":false}`), Conditions: conditions, Evidence: []agent.ConversationRunReference{}, Unresolved: []string{}}}})
}

// This fixture uses the same trusted project-role port as embedded Runtime
// bootstrap. It changes grants inside the admitted role, preserving its identity.
func withdrawSharedBusinessProducerExecution(t *testing.T, f *businessWebFixture) func() {
	t.Helper()
	raw, err := os.ReadFile(f.cfg.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest manifestmodel.ManifestSchema
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	baseline, err := bootstrapruntime.RuntimeInstallationWorkspaceProjectRoleCatalog(manifest.Objects, manifest.Roles, f.cfg.IdentityWorkspaceID, f.cfg.IdentityAudience)
	if err != nil {
		t.Fatal(err)
	}
	for i, role := range manifest.Roles {
		if role.Key != "headquarters_admin" {
			continue
		}
		permissions := role.Permissions[:0:0]
		for _, p := range role.Permissions {
			if strings.HasPrefix(p.PermissionKey, agent.ConversationToolActionPrefix) || strings.HasPrefix(p.PermissionKey, "workflow.") && strings.HasSuffix(p.PermissionKey, ".run") || strings.HasPrefix(p.PermissionKey, "customer.") && p.PermissionKey != "customer.read" {
				continue
			}
			permissions = append(permissions, p)
		}
		manifest.Roles[i].Permissions = permissions
	}
	catalog, err := bootstrapruntime.RuntimeInstallationWorkspaceProjectRoleCatalog(manifest.Objects, manifest.Roles, f.cfg.IdentityWorkspaceID, f.cfg.IdentityAudience)
	if err != nil {
		t.Fatal(err)
	}
	publish := func(catalog identity.ProjectRoleCatalog) {
		t.Helper()
		publisher, ok := f.identity.(identity.ProjectRoleCatalogPublisher)
		if !ok {
			t.Fatal("embedded Identity project role publisher unavailable")
		}
		if _, err := publisher.PublishProjectRoles(t.Context(), catalog); err != nil {
			t.Fatal(err)
		}
	}
	// Keep persisted configuration aligned so restart cannot restore revoked
	// grants. Preserve all unrelated metadata and field/reference policies.
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["roles"], err = json.Marshal(manifest.Roles)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.cfg.ManifestPath, changed, 0600); err != nil {
		t.Fatal(err)
	}
	publish(catalog)
	return func() {
		if err := os.WriteFile(f.cfg.ManifestPath, raw, 0600); err != nil {
			t.Fatal(err)
		}
		publish(baseline)
	}
}

func TestCrossUserBusinessWritesAgentConfirmationsAutomaticDeliverySharedOriginalsAndRestart(t *testing.T) {
	f := newBusinessWebFixtureWithModel(t, sharedBusinessWriteDeliveryModel{}, businessRPCManifest, independentBusinessReadRoles, sharedResultUserRoles, sharedProfessionalCollaborationRoles, sharedBusinessReceiptRoles, sharedBusinessIssuerTools)
	issuer := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	issuer.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	issuer.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	issuer.assign("headquarters_admin")
	issuer.session()
	receiver := createSharedResultProducer(t, f, issuer)
	users, mode := []string{"admin"}, "owner"
	var professional agent.ConversationAgent
	if err := json.Unmarshal(receiver.call("POST", "/agent/agents", agent.ConversationAgentWrite{ClientID: "shared-business-write-agent", Name: "Independent confirmed business writer", Instructions: "Discover current contracts; create and start exactly once with actual user confirmations; deliver original receipts; workflow approval is still pending", Tools: []string{"business_catalog", "invoke_action", "workflow_start", "workflow_get", "delegation_get", "delegation_update"}, SkillKeys: []string{}, ModelKey: "default", Enabled: true, MaxConcurrent: 1, SharedWithUserIDs: &users, DelegationExecution: &mode}, 200).Body.Bytes(), &professional); err != nil || professional.DelegationRoleKey != "headquarters_admin" {
		t.Fatal("business write binding", professional, err)
	}
	producer := agent.ConversationAuthority{Known: true, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, UserID: "professional_source", RoleKey: "headquarters_admin"}
	c, server := openAnalysisRPC(t, f, producer)
	defer func() { server.Close() }()
	countCreated := func() int {
		t.Helper()
		page, err := c.QueryBusinessRecords(t.Context(), agent.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name"}, Filters: []agent.ConversationBusinessFilter{{Field: "name", Operator: "eq", Value: json.RawMessage(`"Agent shared confirmed customer"`)}}, PageSize: 25}, producer)
		if err != nil {
			t.Fatal(err)
		}
		return len(page.Items)
	}
	countProcesses := func() int {
		t.Helper()
		routes := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Set("X-Workspace-ID", f.cfg.IdentityWorkspaceID)
			f.runtime.Routes().ServeHTTP(w, r)
		})
		var page []struct {
			ID string `json:"id"`
		}
		raw := managedIdentityRequest(t, routes, receiver.cookies["domainry_agent_access"].Value, "GET", "/workflow/processes?limit=50", nil, "", 200).Body.Bytes()
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		return len(page)
	}
	if countCreated() != 0 || countProcesses() != 0 {
		t.Fatal("write effects present before dispatch")
	}
	issuer.assign("shared_business_reader")
	issuer.session()
	var source agent.Conversation
	if err := json.Unmarshal(issuer.call("POST", "/agent/conversations", agent.ConversationCreate{ClientID: "shared-business-write-dispatch", Title: "Delegate confirmed business writes"}, 200).Body.Bytes(), &source); err != nil {
		t.Fatal(err)
	}
	var sent agent.ConversationRun
	if err := json.Unmarshal(issuer.call("POST", "/agent/conversations/"+source.ID+"/messages", agent.ConversationSend{ClientMessageID: "write-tool-dispatch", Message: "Delegate business writes:\n" + professional.ID}, 202).Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	var detail agent.ConversationDelegationDetail
	var execution agent.ConversationRun
	approved := map[string]agent.ConversationInteractionResponse{}
	deadline := time.Now().Add(90 * time.Second)
	executing := false
	for time.Now().Before(deadline) {
		var current agent.ConversationRun
		if err := json.Unmarshal(issuer.call("GET", "/agent/conversations/"+source.ID+"/runs/"+sent.ID, nil, 200).Body.Bytes(), &current); err != nil {
			t.Fatal(err)
		}
		if i := current.Interaction; i != nil && i.Status == "pending" {
			issuer.call("POST", "/agent/conversations/"+source.ID+"/runs/"+sent.ID+"/respond", agent.ConversationInteractionResponse{InteractionID: i.ID, ClientID: "approve-" + i.ID, ExpectedRevision: i.Revision, Decision: "approve"}, 200)
		}
		var page agent.ConversationDelegationPage
		if err := json.Unmarshal(issuer.call("GET", "/agent/delegations", nil, 200).Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == 0 {
			if current.Terminal() {
				t.Fatal("write Agent did not dispatch", current)
			}
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if err := json.Unmarshal(receiver.call("GET", "/agent/delegations/"+page.Items[0].ID, nil, 200).Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
		if detail.Task == nil || detail.Task.ExecutionRunID == "" {
			continue
		}
		if !executing {
			executing = true
			deadline = time.Now().Add(90 * time.Second)
		}
		if err := json.Unmarshal(receiver.call("GET", "/agent/conversations/"+detail.ConversationID+"/runs/"+detail.Task.ExecutionRunID, nil, 200).Body.Bytes(), &execution); err != nil {
			t.Fatal(err)
		}
		if i := execution.Interaction; i != nil && i.Status == "pending" {
			if len(approved) == 0 {
				participants := []agent.ConversationDelegationParticipantInput{{UserID: producer.UserID, Operations: []string{"view", "manage"}}}
				issuer.call("POST", "/agent/delegations/"+detail.ID+"/decisions", agent.ConversationDelegationUpdate{ClientID: "receiver-confirmation-control", ExpectedRevision: detail.Revision, Action: "set_participants", Reason: "Allow the actual execution user to confirm its exact private calls", Participants: &participants}, 200)
			}
			if i.Tool != "invoke_action" && i.Tool != "workflow_start" {
				t.Fatal("unexpected write confirmation", i)
			}
			if _, duplicate := approved[i.Tool]; duplicate {
				t.Fatal("write requested a second confirmation", i.Tool)
			}
			if i.Tool == "invoke_action" && (countCreated() != 0 || !strings.Contains(i.Arguments, "Agent shared confirmed customer")) {
				t.Fatal("creation before exact confirmation", i)
			}
			if i.Tool == "workflow_start" && (countCreated() != 1 || countProcesses() != 0 || !strings.Contains(i.Arguments, "Agent shared confirmed review")) {
				t.Fatal("start before exact confirmation", i)
			}
			response := agent.ConversationInteractionResponse{InteractionID: i.ID, ClientID: "receiver-approve-" + i.ID, ExpectedRevision: i.Revision, Decision: "approve"}
			// The initiating account cannot approve the private receiver's call.
			issuer.call("POST", "/agent/conversations/"+detail.ConversationID+"/runs/"+execution.ID+"/respond", response, 404)
			receiver.call("POST", "/agent/conversations/"+detail.ConversationID+"/runs/"+execution.ID+"/respond", response, 200)
			approved[i.Tool] = response
		}
		if execution.Terminal() {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if execution.Status != "completed" || detail.Delivery == nil || len(approved) != 2 || detail.ExecutionSubject == nil || detail.ExecutionSubject.UserID != producer.UserID {
		t.Fatalf("confirmed automatic business delivery incomplete: task=%s run=%s delivery=%s", sharedProfessionalJSON(detail.Task), sharedProfessionalJSON(execution), sharedProfessionalJSON(detail.Delivery))
	}
	var created agent.ConversationBusinessActionResult
	var started agent.ConversationWorkflowReceipt
	var state agent.ConversationWorkflowState
	for _, step := range execution.Steps {
		for _, call := range step.Calls {
			var e agent.ConversationBusinessEvidence
			if json.Unmarshal([]byte(call.ResultPreview), &e) != nil {
				continue
			}
			switch call.Name {
			case "invoke_action":
				if call.Confirmation == nil || call.Confirmation.RespondedBy != producer.UserID {
					t.Fatal("creation confirmation lost actual user", call)
				}
				_ = json.Unmarshal(e.Data, &created)
			case "workflow_start":
				if call.Completion != "accepted" || call.Confirmation == nil || call.Confirmation.RespondedBy != producer.UserID {
					t.Fatal("workflow acceptance/confirmation changed", call)
				}
				_ = json.Unmarshal(e.Data, &started)
			case "workflow_get":
				_ = json.Unmarshal(e.Data, &state)
			}
		}
	}
	if created.Status != "completed" || len(created.CreatedRecords) != 1 || started.Status != "accepted" || state.ProcessID != started.ProcessID || state.Status != "waiting" || state.Terminal || countCreated() != 1 || countProcesses() != 1 {
		t.Fatal("actual effects or pending approval incorrect", created, started, state)
	}
	for _, response := range approved {
		receiver.call("POST", "/agent/conversations/"+detail.ConversationID+"/runs/"+execution.ID+"/respond", response, 200)
	}
	path := "/agent/delegations/" + detail.ID
	var published agent.ConversationDelegationDetail
	if err := json.Unmarshal(issuer.call("GET", path, nil, 200).Body.Bytes(), &published); err != nil || published.Verification == nil || !published.Verification.Ready || published.Delivery == nil {
		reader := producer
		reader.UserID, reader.RoleKey = "admin", "shared_business_reader"
		for _, step := range execution.Steps {
			for _, call := range step.Calls {
				var e agent.ConversationBusinessEvidence
				if json.Unmarshal([]byte(call.ResultPreview), &e) == nil && e.Version == 1 {
					t.Logf("Original shared business source %s: %v", call.Name, c.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer))
				}
			}
		}
		t.Fatalf("cross-user write delivery verification: verification=%s error=%v", sharedProfessionalJSON(published.Verification), err)
	}
	refs := []agent.ConversationResultReference{}
	for _, condition := range published.Delivery.Conditions {
		refs = append(refs, condition.Receipts...)
	}
	if len(refs) != 5 {
		t.Fatal("five original receipts missing", refs)
	}
	read := func(want int) {
		t.Helper()
		for _, ref := range refs {
			issuer.call("POST", path+"/delivery-result", agent.ConversationDeliveryResultRead{ConversationResultRead: agent.ConversationResultRead{Reference: ref}}, want)
		}
	}
	read(200)
	sharedRead := verifyProfessionalExecutionSharingSteps(t, issuer, receiver, detail, execution, refs, 8)
	if err := json.Unmarshal(issuer.call("GET", path, nil, 200).Body.Bytes(), &published); err != nil {
		t.Fatal(err)
	}
	issuer.call("POST", path+"/decisions", agent.ConversationDelegationUpdate{ClientID: "accept-confirmed-business-writes", ExpectedRevision: published.Revision, Action: "accept_delivery", Reason: "Checked actual confirmations, effects, original pages and pending business approval", Review: &agent.ConversationDeliveryReview{DeliveryDigest: published.Verification.DeliveryDigest}}, 200)
	// Removing producing tool/action/start rights retains explicit source reads.
	restoreProducerExecution := withdrawSharedBusinessProducerExecution(t, f)
	for _, step := range execution.Steps {
		for _, call := range step.Calls {
			var auth agent.ConversationToolAuthorization
			var err error
			switch call.Name {
			case "invoke_action":
				var q agent.ConversationBusinessAction
				if err := json.Unmarshal([]byte(call.Arguments), &q); err != nil {
					t.Fatal(err)
				}
				auth, err = c.AuthorizeBusinessAction(t.Context(), q, producer)
			case "workflow_start":
				var q agent.ConversationWorkflowStart
				if err := json.Unmarshal([]byte(call.Arguments), &q); err != nil {
					t.Fatal(err)
				}
				auth, err = c.AuthorizeWorkflowStart(t.Context(), q, producer)
			default:
				continue
			}
			if err == nil && auth.Granted {
				t.Fatal("producer execution grant survived explicit publication", call.Name)
			}
		}
	}
	read(200)
	sharedRead(200)
	server.Close()
	f.close()
	f.open()
	issuer.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": businessWebPassword}, 200)
	issuer.session()
	read(200)
	sharedRead(200)
	issuer.assign("shared_business_receipt_owner")
	issuer.session()
	read(403)
	sharedRead(403)
	issuer.assign("shared_business_reader")
	issuer.session()
	read(200)
	issuer.assign("shared_business_data_denied")
	issuer.session()
	read(403)
	sharedRead(403)
	issuer.assign("shared_business_reader")
	issuer.session()
	read(200)
	assignSharedResultProducer(t, f, issuer, "shared_business_data_denied")
	read(403)
	sharedRead(403)
	assignSharedResultProducer(t, f, issuer, "headquarters_admin")
	restoreProducerExecution()
	receiver.call("POST", "/auth/login", map[string]any{"login": "professional@example.com", "password": businessWebPassword}, 200)
	receiver.session()
	c, server = openAnalysisRPC(t, f, producer)
	if countCreated() != 1 || countProcesses() != 1 {
		t.Fatal("original writes duplicated during reading/restart")
	}
	routes := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Workspace-ID", f.cfg.IdentityWorkspaceID)
		f.runtime.Routes().ServeHTTP(w, r)
	})
	var process workflowapplication.ParticipantWorkflowProcessDetailDTO
	raw := managedIdentityRequest(t, routes, issuer.cookies["domainry_agent_access"].Value, "GET", "/workflow/processes/"+started.ProcessID, nil, "", 200).Body.Bytes()
	if err := json.Unmarshal(raw, &process); err != nil || len(process.Tasks) != 1 || process.Tasks[0].Status != "open" {
		t.Fatal("shared reading changed actual pending approval", process, err)
	}
	issuer.call("GET", "/agent/conversations/"+detail.ConversationID, nil, 404)
}
