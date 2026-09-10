package agenthost

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	invocationcontract "github.com/domainry/domainry-runtime/runtime/domain/manifest/contract/invocation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

type businessWorkflowPortProbe struct {
	definition      definitionmodel.WorkflowSchema
	process         workflowmodel.WorkflowProcessInstance
	execution       workflowmodel.WorkflowExecution
	starts          int
	denied, unknown bool
}

func (f *businessWorkflowPortProbe) AgentWorkflowDefinition(_ context.Context, key string, p principalmodel.Principal) (definitionmodel.WorkflowSchema, error) {
	if f.denied || key != f.definition.Key || len(invocationcontract.ValidateWorkflowPermission(f.definition, p)) != 0 {
		return definitionmodel.WorkflowSchema{}, fmt.Errorf("denied")
	}
	return f.definition, nil
}
func (f *businessWorkflowPortProbe) RunAgentWorkflowWithKey(_ context.Context, key string, data map[string]any, call string, p principalmodel.Principal) (workflowmodel.WorkflowRunResult, error) {
	f.starts++
	f.execution = workflowmodel.WorkflowExecution{ID: "execution-1", ProcessID: f.process.ID, WorkflowKey: key, ActorID: p.UserID, WorkspaceID: p.WorkspaceID, IdempotencyKey: call, Payload: data}
	if f.unknown {
		return workflowmodel.WorkflowRunResult{}, fmt.Errorf("transport unavailable")
	}
	return workflowmodel.WorkflowRunResult{Execution: f.execution}, nil
}
func (f *businessWorkflowPortProbe) InspectAgentWorkflowInvocation(_ context.Context, key string, _ map[string]any, call string, p principalmodel.Principal) (workflowmodel.WorkflowExecution, bool, error) {
	if f.denied || key != f.execution.WorkflowKey || call != f.execution.IdempotencyKey || p.UserID != f.execution.ActorID {
		return workflowmodel.WorkflowExecution{}, false, fmt.Errorf("denied")
	}
	return f.execution, true, nil
}
func (f *businessWorkflowPortProbe) WorkflowProcess(_ context.Context, id string, p principalmodel.Principal) (workflowapplication.WorkflowProcessDetail, error) {
	if f.denied || id != f.process.ID || p.UserID != f.process.InitiatorID {
		return workflowapplication.WorkflowProcessDetail{}, fmt.Errorf("denied")
	}
	return workflowapplication.WorkflowProcessDetail{Process: f.process}, nil
}

func TestConversationWorkflowHostConfirmsReconcilesAndRevalidatesChangingState(t *testing.T) {
	host, resolver, reads, a := newConversationBusinessFixture(t)
	permissions := []string{"customer.read", "workflow.review.run"}
	resolver.principal = accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: a.UserID, WorkspaceID: a.WorkspaceID}}, accessfixture.Bundle{Permissions: permissions, DataPolicies: accessfixture.DataPoliciesForPermissions(permissions, identitysdk.DataScopeOwner)})
	definition := definitionmodel.WorkflowSchema{Key: "review", Name: "业务审核", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}, InputFields: []definitionmodel.WorkflowInputField{{Key: "reason", Type: "text", Required: true}}, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "approval", Type: "approval", Name: "经理审批"}}}}
	port := &businessWorkflowPortProbe{definition: definition, unknown: true, process: workflowmodel.WorkflowProcessInstance{ID: "process-1", WorkspaceID: a.WorkspaceID, WorkflowKey: "review", WorkflowName: "业务审核", InitiatorID: a.UserID, Status: "waiting", CurrentNodeIDs: []string{"approval"}, DefinitionSnapshot: definition, Variables: map[string]any{"private": "DO-NOT-EXPOSE"}, Result: map[string]any{"private": "DO-NOT-EXPOSE"}, UpdatedAt: "2026-09-10T08:00:00Z"}}
	if err := WithConversationBusinessWorkflows(port)(host); err != nil {
		t.Fatal(err)
	}
	if err := WithConversationBusinessEvidenceKey([]byte(strings.Repeat("k", 32)))(host); err != nil {
		t.Fatal(err)
	}
	host.schema = appschemaapplication.NewApplicationSchemaQueryApplicationService(businessSchemaSnapshot{appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{reads.object}, Workflows: []definitionmodel.WorkflowSchema{definition}}}, nil)
	page, err := host.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{Kind: "workflows", WorkflowKey: "review"}, a)
	if err != nil || len(page.Workflows) != 1 || page.Workflows[0].ExecutionVersion == "" {
		t.Fatal(page, err)
	}
	start := agentsdk.ConversationWorkflowStart{WorkflowKey: "review", Version: page.Workflows[0].ExecutionVersion, Data: json.RawMessage(`{"reason":"正式审核"}`)}
	raw, _ := json.Marshal(start)
	request := agentsdk.ConversationWorkflowStartRequest{Authority: a, Start: start, Arguments: string(raw), ConversationID: "conversation", RunID: "run", CallID: "call", IdempotencyKey: "logical-key", Confirmation: &agentsdk.ConversationConfirmation{ID: "confirmation", UserID: a.UserID, ActionKey: agentsdk.ConversationToolActionPrefix + "workflow_start", ToolVersion: "1", ApprovedAt: time.Now().UTC(), ArgumentsHash: conversationBusinessDigest(string(raw))}}
	for _, mutate := range []func(*agentsdk.ConversationWorkflowStartRequest){
		func(q *agentsdk.ConversationWorkflowStartRequest) { q.Confirmation = nil },
		func(q *agentsdk.ConversationWorkflowStartRequest) { q.Start.Version = "stale" },
		func(q *agentsdk.ConversationWorkflowStartRequest) { q.Arguments += " " },
		func(q *agentsdk.ConversationWorkflowStartRequest) {
			q.Start.Data = json.RawMessage(`{"reason":"审核","run_as":"admin"}`)
		},
	} {
		bad := request
		mutate(&bad)
		if _, err := host.StartBusinessWorkflow(t.Context(), bad); err == nil || port.starts != 0 {
			t.Fatal("invalid start reached host", err)
		}
	}
	receipt, err := host.StartBusinessWorkflow(t.Context(), request)
	if err != nil || receipt.Status != "uncertain" || port.starts != 1 {
		t.Fatal(receipt, err)
	}
	receipt, err = host.ReconcileBusinessWorkflow(t.Context(), request)
	if err != nil || receipt.Status != "accepted" || receipt.ProcessID != "process-1" || port.starts != 1 {
		t.Fatal(receipt, err)
	}
	receiptData, _ := json.Marshal(receipt)
	evidence := agentsdk.ConversationBusinessEvidence{Version: 1, Source: host.source, ScopeSHA256: conversationBusinessDigest([]string{host.source, a.RuntimeID, a.WorkspaceID, a.UserID}), Operation: "workflow_start", Input: raw, Data: receiptData}
	if err := host.RevalidateBusinessWorkflow(t.Context(), evidence, a); err != nil {
		t.Fatal(err)
	}
	q := agentsdk.ConversationWorkflowGet{WorkflowKey: "review", ProcessID: "process-1"}
	state, err := host.GetBusinessWorkflow(t.Context(), q, a)
	if err != nil || state.Status != "waiting" || state.Terminal || len(state.CurrentSteps) != 1 || state.CurrentSteps[0] != "经理审批" {
		t.Fatal(state, err)
	}
	stateData, _ := json.Marshal(state)
	queryData, _ := json.Marshal(q)
	if strings.Contains(string(stateData), "DO-NOT-EXPOSE") {
		t.Fatal("private variables escaped")
	}
	evidence.Operation, evidence.Input, evidence.Data = "workflow_get", queryData, stateData
	evidence.HostProof, err = host.SealBusinessEvidence(t.Context(), evidence, a)
	if err != nil || evidence.HostProof == "" {
		t.Fatal(err)
	}
	port.process.Status, port.process.CurrentNodeIDs = "success", nil
	port.process.Variables["approval_decision"] = "approved"
	port.process.UpdatedAt, port.process.CompletedAt = "2026-09-10T09:00:00Z", "2026-09-10T09:00:00Z"
	state, err = host.GetBusinessWorkflow(t.Context(), q, a)
	if err != nil || !state.Terminal || state.BusinessOutcome != "approved" {
		t.Fatal(state, err)
	}
	if err := host.RevalidateBusinessWorkflow(t.Context(), evidence, a); err != nil {
		t.Fatal("history should preserve signed waiting state", err)
	}
	bad := evidence
	bad.Data = json.RawMessage(strings.Replace(string(evidence.Data), "waiting", "success", 1))
	if err := host.RevalidateBusinessWorkflow(t.Context(), bad, a); err == nil {
		t.Fatal("tampered history accepted")
	}
	// The public schema snapshot can lag the executable registry. Every part
	// of the catalog projection and object filter must use the current target.
	port.definition.Name = "更新后的审核"
	port.definition.TriggerContract = &definitionmodel.WorkflowTriggerContract{Type: "manual", ObjectKey: "customer"}
	page, err = host.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{Kind: "workflows", WorkflowKey: "review", ObjectKey: "customer"}, a)
	if err != nil || len(page.Workflows) != 1 || page.Workflows[0].Label != port.definition.Name || len(page.Workflows[0].ObjectKeys) != 1 || page.Workflows[0].ObjectKeys[0] != "customer" || page.Workflows[0].ExecutionVersion == start.Version {
		t.Fatal("catalog mixed stale schema and current execution contract", page, err)
	}
	port.denied = true
	if _, err := host.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{Kind: "workflows", WorkflowKey: "review"}, a); err == nil {
		t.Fatal("unavailable execution target fell back to stale schema")
	}
	page, err = host.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{Kind: "workflows"}, a)
	if err != nil || len(page.Workflows) != 0 {
		t.Fatal("unavailable execution target remained in discovery", page, err)
	}
	if err := host.RevalidateBusinessWorkflow(t.Context(), evidence, a); err == nil {
		t.Fatal("revoked process history remained visible")
	}
	if port.starts != 1 {
		t.Fatal("reading progress repeated start")
	}
}
