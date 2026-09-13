package agenthost

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type ConversationBusinessWorkflows interface {
	AgentWorkflowDefinition(context.Context, string, principalmodel.Principal) (definitionmodel.WorkflowSchema, error)
	RunAgentWorkflowWithKey(context.Context, string, map[string]any, string, principalmodel.Principal) (workflowmodel.WorkflowRunResult, error)
	InspectAgentWorkflowInvocation(context.Context, string, map[string]any, string, principalmodel.Principal) (workflowmodel.WorkflowExecution, bool, error)
	WorkflowProcess(context.Context, string, principalmodel.Principal) (workflowapplication.WorkflowProcessDetail, error)
}

func WithConversationBusinessWorkflows(workflows ConversationBusinessWorkflows) ConversationBusinessHostOption {
	return func(h *ConversationBusinessHost) error {
		if workflows == nil {
			return fmt.Errorf("conversation workflows are required")
		}
		h.workflows = workflows
		return nil
	}
}

func (h *ConversationBusinessHost) businessWorkflowVersion(workflow definitionmodel.WorkflowSchema) string {
	return conversationBusinessDigest([]any{"runtime-conversation-workflow-v1", h.source, workflow})
}

func (h *ConversationBusinessHost) prepareBusinessWorkflow(ctx context.Context, start agentsdk.ConversationWorkflowStart, a agentsdk.ConversationAuthority) (principalmodel.Principal, map[string]any, error) {
	return h.prepareBusinessWorkflowAccess(ctx, start, a, false)
}

func (h *ConversationBusinessHost) prepareBusinessWorkflowAccess(ctx context.Context, start agentsdk.ConversationWorkflowStart, a agentsdk.ConversationAuthority, resultRead bool) (principalmodel.Principal, map[string]any, error) {
	p, err := h.principal(ctx, a)
	if err != nil {
		return p, nil, err
	}
	if h.workflows == nil || len(h.evidenceKey) == 0 || start.WorkflowKey == "" || len(start.WorkflowKey) > 128 || len(start.Version) > 256 {
		return p, nil, conversationBusinessError("forbidden")
	}
	var workflow definitionmodel.WorkflowSchema
	if resultRead {
		reader, ok := h.workflows.(conversationBusinessWorkflowReceiptReader)
		if !ok {
			return p, nil, &agentsdk.Error{Class: "unavailable", Code: agentsdk.BusinessResultReadUnsupportedCode}
		}
		workflow, err = reader.AgentWorkflowReceiptDefinition(ctx, start.WorkflowKey, p)
	} else {
		workflow, err = h.workflows.AgentWorkflowDefinition(ctx, start.WorkflowKey, p)
	}
	if err != nil {
		return p, nil, conversationBusinessReadError(err)
	}
	if start.Version != h.businessWorkflowVersion(workflow) {
		return p, nil, conversationBusinessError("conflict")
	}
	var data map[string]any
	decoder := json.NewDecoder(bytes.NewReader(start.Data))
	decoder.UseNumber()
	if decoder.Decode(&data) != nil || data == nil || len(data) > 100 {
		return p, nil, conversationBusinessError("bad_request")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return p, nil, conversationBusinessError("bad_request")
	}
	data, err = workflowapplication.NormalizeAgentWorkflowInput(workflow, data)
	if err != nil {
		return p, nil, conversationBusinessError("bad_request")
	}
	return p, data, nil
}

func (h *ConversationBusinessHost) AuthorizeWorkflowStart(ctx context.Context, start agentsdk.ConversationWorkflowStart, a agentsdk.ConversationAuthority) (agentsdk.ConversationToolAuthorization, error) {
	if start.WorkflowKey == "" {
		_, err := h.principal(ctx, a)
		return agentsdk.ConversationToolAuthorization{Granted: err == nil && h.workflows != nil && len(h.evidenceKey) > 0}, err
	}
	_, _, err := h.prepareBusinessWorkflow(ctx, start, a)
	return agentsdk.ConversationToolAuthorization{Granted: err == nil}, err
}

func workflowStartReceipt(start agentsdk.ConversationWorkflowStart, key string, execution workflowmodel.WorkflowExecution) agentsdk.ConversationWorkflowReceipt {
	return agentsdk.ConversationWorkflowReceipt{Status: "accepted", InvocationID: key, WorkflowKey: start.WorkflowKey, ExecutionID: execution.ID, ProcessID: execution.ProcessID}
}

func (h *ConversationBusinessHost) startBusinessWorkflow(ctx context.Context, request agentsdk.ConversationWorkflowStartRequest, reconcile bool) (agentsdk.ConversationWorkflowReceipt, error) {
	unknown := agentsdk.ConversationWorkflowReceipt{Status: "uncertain"}
	p, data, err := h.prepareBusinessWorkflow(ctx, request.Start, request.Authority)
	if err != nil {
		return agentsdk.ConversationWorkflowReceipt{}, err
	}
	c := request.Confirmation
	if c == nil || c.ID == "" || c.UserID != request.Authority.UserID || c.ActionKey != agentsdk.ConversationToolActionPrefix+"workflow_start" || c.ToolVersion != "1" || c.ApprovedAt.IsZero() || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 256 || request.ConversationID == "" || request.RunID == "" || request.CallID == "" {
		return agentsdk.ConversationWorkflowReceipt{}, conversationBusinessError("forbidden")
	}
	var confirmed agentsdk.ConversationWorkflowStart
	if json.Unmarshal([]byte(request.Arguments), &confirmed) != nil || conversationBusinessDigest(confirmed) != conversationBusinessDigest(request.Start) || c.ArgumentsHash != conversationBusinessDigest(request.Arguments) {
		return agentsdk.ConversationWorkflowReceipt{}, conversationBusinessError("forbidden")
	}
	p.RequestID = "conversation:" + request.RunID + ":" + request.CallID
	p.CorrelationID = request.RunID
	if reconcile {
		execution, found, err := h.workflows.InspectAgentWorkflowInvocation(ctx, request.Start.WorkflowKey, data, request.IdempotencyKey, p)
		if err != nil {
			return unknown, nil
		}
		if found {
			return workflowStartReceipt(request.Start, request.IdempotencyKey, execution), nil
		}
	}
	result, err := h.workflows.RunAgentWorkflowWithKey(ctx, request.Start.WorkflowKey, data, request.IdempotencyKey, p)
	if err != nil {
		// A failure here can follow a durable process start. Only a receipt
		// inspection can decide whether the logical operation was accepted.
		return unknown, nil
	}
	return workflowStartReceipt(request.Start, request.IdempotencyKey, result.Execution), nil
}

func (h *ConversationBusinessHost) StartBusinessWorkflow(ctx context.Context, request agentsdk.ConversationWorkflowStartRequest) (agentsdk.ConversationWorkflowReceipt, error) {
	return h.startBusinessWorkflow(ctx, request, false)
}

func (h *ConversationBusinessHost) ReconcileBusinessWorkflow(ctx context.Context, request agentsdk.ConversationWorkflowStartRequest) (agentsdk.ConversationWorkflowReceipt, error) {
	return h.startBusinessWorkflow(ctx, request, true)
}

func (h *ConversationBusinessHost) GetBusinessWorkflow(ctx context.Context, q agentsdk.ConversationWorkflowGet, a agentsdk.ConversationAuthority) (agentsdk.ConversationWorkflowState, error) {
	var state agentsdk.ConversationWorkflowState
	p, err := h.principal(ctx, a)
	if err != nil {
		return state, err
	}
	if h.workflows == nil || len(h.evidenceKey) == 0 || q.WorkflowKey == "" || len(q.WorkflowKey) > 128 || q.ProcessID == "" || len(q.ProcessID) > 256 {
		return state, conversationBusinessError("forbidden")
	}
	detail, err := h.workflows.WorkflowProcess(ctx, q.ProcessID, p)
	if err != nil {
		return state, conversationBusinessReadError(err)
	}
	process := detail.Process
	if process.ID != q.ProcessID || process.WorkflowKey != q.WorkflowKey || process.WorkspaceID != a.WorkspaceID {
		return state, conversationBusinessError("forbidden")
	}
	state = agentsdk.ConversationWorkflowState{WorkflowKey: process.WorkflowKey, ProcessID: process.ID, Name: process.WorkflowName, Status: process.Status, CurrentSteps: []string{}, CurrentStepCount: len(process.CurrentNodeIDs), UpdatedAt: process.UpdatedAt, CompletedAt: process.CompletedAt}
	switch process.Status {
	case "success", "completed", "approved", "rejected", "cancelled", "failed", "configuration_error", "terminated":
		state.Terminal = true
	}
	// The ordinary participant projection intentionally exposes the business
	// outcome. Execution variables and raw node outputs stay inside Workflow.
	state.BusinessOutcome = process.BusinessOutcome
	if state.BusinessOutcome == "" {
		if outcome, ok := process.Variables["approval_decision"].(string); ok {
			state.BusinessOutcome = outcome
		}
	}
	names := map[string]string{}
	if process.DefinitionSnapshot.Graph != nil {
		for _, node := range process.DefinitionSnapshot.Graph.Nodes {
			names[node.ID] = node.Name
		}
	}
	for _, id := range process.CurrentNodeIDs {
		if len(state.CurrentSteps) == 100 {
			state.StepsTruncated = true
			break
		}
		name := names[id]
		if name == "" {
			name = id
		}
		state.CurrentSteps = append(state.CurrentSteps, name)
	}
	if process.ObjectKey != "" || process.RecordID != "" {
		if process.ObjectKey == "" || process.RecordID == "" {
			return agentsdk.ConversationWorkflowState{}, conversationBusinessError("forbidden")
		}
		if _, err := h.GetBusinessRecord(ctx, agentsdk.ConversationBusinessGet{ObjectKey: process.ObjectKey, RecordID: process.RecordID}, a); err != nil {
			return agentsdk.ConversationWorkflowState{}, err
		}
		state.Record = &agentsdk.ConversationBusinessRecordReference{ObjectKey: process.ObjectKey, RecordID: process.RecordID}
	}
	return state, nil
}

func (h *ConversationBusinessHost) RevalidateBusinessWorkflow(ctx context.Context, e agentsdk.ConversationBusinessEvidence, a agentsdk.ConversationAuthority) error {
	return h.revalidateBusinessWorkflow(ctx, e, a, false)
}

func (h *ConversationBusinessHost) revalidateBusinessWorkflow(ctx context.Context, e agentsdk.ConversationBusinessEvidence, a agentsdk.ConversationAuthority, resultRead bool) error {
	if e.Version != 1 || e.Source != h.source || e.ScopeSHA256 != conversationBusinessDigest([]string{h.source, a.RuntimeID, a.WorkspaceID, a.UserID}) {
		return conversationBusinessError("forbidden")
	}
	if e.Operation == "workflow_start" {
		var start agentsdk.ConversationWorkflowStart
		var saved agentsdk.ConversationWorkflowReceipt
		if json.Unmarshal(e.Input, &start) != nil || json.Unmarshal(e.Data, &saved) != nil || saved.Status != "accepted" {
			return conversationBusinessError("forbidden")
		}
		p, data, err := h.prepareBusinessWorkflow(ctx, start, a)
		if err != nil {
			return err
		}
		execution, found, err := h.workflows.InspectAgentWorkflowInvocation(ctx, start.WorkflowKey, data, saved.InvocationID, p)
		if err != nil || !found || conversationBusinessDigest(saved) != conversationBusinessDigest(workflowStartReceipt(start, saved.InvocationID, execution)) {
			return conversationBusinessError("forbidden")
		}
		_, err = h.GetBusinessWorkflow(ctx, agentsdk.ConversationWorkflowGet{WorkflowKey: start.WorkflowKey, ProcessID: saved.ProcessID}, a)
		return err
	}
	if e.Operation != "workflow_get" {
		return conversationBusinessError("forbidden")
	}
	var q agentsdk.ConversationWorkflowGet
	if json.Unmarshal(e.Input, &q) != nil {
		return conversationBusinessError("forbidden")
	}
	current, err := h.GetBusinessWorkflow(ctx, q, a)
	if err != nil {
		return err
	}
	if e.HostProof == "" {
		if conversationBusinessDigest(current) != conversationBusinessDigest(json.RawMessage(e.Data)) {
			return conversationBusinessError("forbidden")
		}
		return nil
	}
	parts := strings.Split(e.HostProof, ":")
	if len(h.evidenceKey) < sha256.Size || len(parts) != 3 || parts[0] != "workflow1" || len(parts[1]) != 64 || len(parts[2]) != 64 {
		return conversationBusinessError("forbidden")
	}
	mac, err := hex.DecodeString(parts[2])
	if err != nil || !hmac.Equal(mac, h.businessEvidenceMAC(e, "workflow:"+parts[1])) {
		return conversationBusinessError("forbidden")
	}
	var saved agentsdk.ConversationWorkflowState
	if json.Unmarshal(e.Data, &saved) != nil || saved.WorkflowKey != current.WorkflowKey || saved.ProcessID != current.ProcessID || conversationBusinessDigest(saved.Record) != conversationBusinessDigest(current.Record) {
		return conversationBusinessError("forbidden")
	}
	p, err := h.principal(ctx, a)
	if err != nil {
		return err
	}
	if parts[1] != h.businessPolicyDigest(ctx, p) {
		// A policy change (including removal of the producing tool grant)
		// may still permit the exact currently visible process projection.
		// Verify the signature first; never ignore a forged proof or replace
		// historical state with current state under a changed policy.
		if resultRead && conversationBusinessDigest(current) == conversationBusinessDigest(json.RawMessage(e.Data)) {
			return nil
		}
		return conversationBusinessError("forbidden")
	}
	// Current membership and record scope were rechecked above. An ordinary
	// state transition does not rewrite or invalidate the signed old status.
	return nil
}

func (h *ConversationBusinessHost) sealWorkflowEvidence(ctx context.Context, e agentsdk.ConversationBusinessEvidence, a agentsdk.ConversationAuthority) (string, error) {
	if len(h.evidenceKey) < sha256.Size || e.HostProof != "" {
		return "", conversationBusinessError("forbidden")
	}
	p, err := h.principal(ctx, a)
	if err != nil {
		return "", err
	}
	policy := h.businessPolicyDigest(ctx, p)
	if err := h.RevalidateBusinessWorkflow(ctx, e, a); err != nil {
		return "", err
	}
	p, err = h.principal(ctx, a)
	if err != nil || policy != h.businessPolicyDigest(ctx, p) {
		return "", conversationBusinessError("forbidden")
	}
	return "workflow1:" + policy + ":" + hex.EncodeToString(h.businessEvidenceMAC(e, "workflow:"+policy)), nil
}

var _ agentsdk.ConversationBusinessWorkflowSource = (*ConversationBusinessHost)(nil)
