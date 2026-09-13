package agenthost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	agent "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/idempotency"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type conversationBusinessActionReceiptReader interface {
	ReadInvocationReceipt(context.Context, actionmodel.ActionInvocation) (actionapplication.ActionInvocationReceipt, error)
}

type conversationBusinessWorkflowReceiptReader interface {
	AgentWorkflowReceiptDefinition(context.Context, string, principalmodel.Principal) (definitionmodel.WorkflowSchema, error)
	ReadAgentWorkflowReceipt(context.Context, string, map[string]any, string, principalmodel.Principal) (workflowapplication.AgentWorkflowReceipt, bool, error)
}

func decodeBusinessReceipt(raw json.RawMessage, out any) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	return d.Decode(out) == nil && d.Decode(new(any)) == io.EOF
}

func (h *ConversationBusinessHost) ownedWriteEvidence(e agent.ConversationBusinessEvidence, a agent.ConversationAuthority) bool {
	// Runtime write acknowledgements are verified against their durable ledger,
	// rather than carrying the snapshot signatures used by read operations.
	return e.Version == 1 && e.HostProof == "" && e.Source == h.source && e.ScopeSHA256 == conversationBusinessDigest([]string{h.source, a.RuntimeID, a.WorkspaceID, a.UserID})
}

func (h *ConversationBusinessHost) unchangedReceiptReadAuthority(ctx context.Context, a agent.ConversationAuthority, policy string) error {
	p, err := h.principal(ctx, a)
	if err != nil || h.businessPolicyDigest(ctx, p) != policy {
		return conversationBusinessError("forbidden")
	}
	return nil
}

func (h *ConversationBusinessHost) readBusinessActionReceipt(ctx context.Context, e agent.ConversationBusinessEvidence, a agent.ConversationAuthority) error {
	reader, ok := h.actions.(conversationBusinessActionReceiptReader)
	if !ok {
		return &agent.Error{Class: "unavailable", Code: agent.BusinessResultReadUnsupportedCode}
	}
	var q agent.ConversationBusinessAction
	var saved agent.ConversationBusinessActionResult
	if !h.ownedWriteEvidence(e, a) || !decodeBusinessReceipt(e.Input, &q) || !decodeBusinessReceipt(e.Data, &saved) || saved.Status != "completed" || saved.InvocationID == "" || len(saved.InvocationID) > 256 {
		return conversationBusinessError("forbidden")
	}
	_, in, err := h.prepareBusinessActionAccess(ctx, q, a, true)
	if err != nil {
		return err
	}
	policy := h.businessPolicyDigest(ctx, in.Principal)
	in.IdempotencyKey = saved.InvocationID
	stored, err := reader.ReadInvocationReceipt(ctx, in)
	if err != nil || !stored.Found || stored.Status != string(idempotency.StatusSucceeded) {
		return conversationBusinessError("forbidden")
	}
	// Adapt only the owner's neutral acknowledgement. Raw handler values never
	// enter this path; reuse the exact original reference/count projection.
	result := actionmodel.ActionInvocationResult{InvocationID: stored.InvocationID}
	if in.RecordID != "" {
		result.Record = &actionmodel.ActionResult{CreatedRecords: stored.CreatedRecords, UpdatedRecords: stored.UpdatedRecords, DeletedRecords: stored.DeletedRecords, RestoredRecords: stored.RestoredRecords}
	} else {
		result.Object = &actionmodel.ActionObjectResult{CreatedRecords: stored.CreatedRecords, UpdatedRecords: stored.UpdatedRecords, DeletedRecords: stored.DeletedRecords, RestoredRecords: stored.RestoredRecords}
	}
	current, err := json.Marshal(businessActionAcknowledgement(in, result))
	if err != nil || !bytes.Equal(current, e.Data) {
		return conversationBusinessError("forbidden")
	}
	return h.unchangedReceiptReadAuthority(ctx, a, policy)
}

func (h *ConversationBusinessHost) readBusinessWorkflowStartReceipt(ctx context.Context, e agent.ConversationBusinessEvidence, a agent.ConversationAuthority) error {
	reader, ok := h.workflows.(conversationBusinessWorkflowReceiptReader)
	if !ok {
		return &agent.Error{Class: "unavailable", Code: agent.BusinessResultReadUnsupportedCode}
	}
	var start agent.ConversationWorkflowStart
	var saved agent.ConversationWorkflowReceipt
	if !h.ownedWriteEvidence(e, a) || !decodeBusinessReceipt(e.Input, &start) || !decodeBusinessReceipt(e.Data, &saved) || saved.Status != "accepted" || saved.InvocationID == "" || len(saved.InvocationID) > 256 {
		return conversationBusinessError("forbidden")
	}
	p, data, err := h.prepareBusinessWorkflowAccess(ctx, start, a, true)
	if err != nil {
		return err
	}
	policy := h.businessPolicyDigest(ctx, p)
	stored, found, err := reader.ReadAgentWorkflowReceipt(ctx, start.WorkflowKey, data, saved.InvocationID, p)
	if err != nil || !found || stored.WorkflowKey != start.WorkflowKey || stored.ExecutionID == "" || stored.ProcessID == "" {
		return conversationBusinessError("forbidden")
	}
	expected := agent.ConversationWorkflowReceipt{Status: "accepted", InvocationID: saved.InvocationID, WorkflowKey: stored.WorkflowKey, ExecutionID: stored.ExecutionID, ProcessID: stored.ProcessID}
	current, err := json.Marshal(expected)
	if err != nil || !bytes.Equal(current, e.Data) {
		return conversationBusinessError("forbidden")
	}
	// This also applies current linked-record read policy in the Record owner.
	if _, err := h.GetBusinessWorkflow(ctx, agent.ConversationWorkflowGet{WorkflowKey: start.WorkflowKey, ProcessID: saved.ProcessID}, a); err != nil {
		return err
	}
	return h.unchangedReceiptReadAuthority(ctx, a, policy)
}

var _ conversationBusinessActionReceiptReader = (*actionapplication.ActionApplicationService)(nil)
var _ conversationBusinessWorkflowReceiptReader = (*workflowapplication.WorkflowApplicationService)(nil)
