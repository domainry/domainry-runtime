package agenthost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionpolicy "github.com/domainry/domainry-runtime/runtime/domain/action/policy"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	invocationcontract "github.com/domainry/domainry-runtime/runtime/domain/manifest/contract/invocation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ConversationBusinessActions interface {
	Definitions() []definitionmodel.ActionSchema
	Invoke(context.Context, actionmodel.ActionSource, actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error)
	InspectInvocation(context.Context, actionmodel.ActionInvocation) (actionapplication.ActionInvocationInspection, error)
}

type conversationBusinessActionRecords interface {
	RecordScopeAllowsAction(context.Context, string, string, string, principalmodel.Principal) (bool, error)
}

func WithConversationBusinessActions(actions ConversationBusinessActions) ConversationBusinessHostOption {
	return func(h *ConversationBusinessHost) error {
		if actions == nil {
			return fmt.Errorf("conversation business actions are required")
		}
		if _, ok := h.records.(conversationBusinessActionRecords); !ok {
			return fmt.Errorf("conversation business action record scope is required")
		}
		h.actions = actions
		return nil
	}
}

func (h *ConversationBusinessHost) businessActionDefinition(key string) (definitionmodel.ActionSchema, bool) {
	if h.actions != nil {
		for _, action := range h.actions.Definitions() {
			if action.Key == key {
				return action, true
			}
		}
	}
	return definitionmodel.ActionSchema{}, false
}

func (h *ConversationBusinessHost) businessActionVersion(action definitionmodel.ActionSchema) string {
	return conversationBusinessDigest([]any{"runtime-conversation-action-v1", h.source, action})
}

// Publish the concurrency input required by the Runtime even when it is not a
// business field. Other legacy invocation extras are not model-owned input.
func businessActionPayloadFields(action definitionmodel.ActionSchema) []definitionmodel.ActionPayloadField {
	fields := append([]definitionmodel.ActionPayloadField{}, action.PayloadFields...)
	if !action.OptimisticConcurrency {
		return fields
	}
	field := definitionmodel.ActionPayloadField{Key: "expected_updated_at", Type: "text", Required: true, Description: "Use the version returned by get_record or query_records for this target."}
	if action.ConcurrencyField != "" && action.ConcurrencyField != "updated_at" {
		field.Key, field.Type, field.Description = "expected_version", "integer", "Use the current value of the published concurrency_field on the target record."
	}
	for _, existing := range fields {
		if existing.Key == field.Key {
			return fields
		}
	}
	return append(fields, field)
}

func businessActionError(class, code string) error { return &agentsdk.Error{Class: class, Code: code} }

func businessActionHostError(err error) error {
	if err == nil {
		return nil
	}
	code := apperror.CodeOf(err)
	switch code {
	case "backend.record.version_conflict":
		return businessActionError("conflict", "business_record_version_conflict")
	case idempotency.ErrorCodeKeyReused:
		return businessActionError("conflict", "business_action_receipt_conflict")
	}
	if strings.Contains(code, "assurance") {
		return businessActionError("bad_request", "business_action_assurance_required")
	}
	switch apperror.KindOf(err) {
	case apperror.KindForbidden:
		return conversationBusinessError("forbidden")
	case apperror.KindNotFound:
		return conversationBusinessError("not_found")
	case apperror.KindBadRequest, apperror.KindConflict:
		return businessActionError("bad_request", "business_action_invalid")
	default:
		return conversationBusinessError("unavailable")
	}
}

func (h *ConversationBusinessHost) prepareBusinessAction(ctx context.Context, q agentsdk.ConversationBusinessAction, a agentsdk.ConversationAuthority) (definitionmodel.ActionSchema, actionmodel.ActionInvocation, error) {
	return h.prepareBusinessActionAccess(ctx, q, a, false)
}

func (h *ConversationBusinessHost) prepareBusinessActionAccess(ctx context.Context, q agentsdk.ConversationBusinessAction, a agentsdk.ConversationAuthority, resultRead bool) (definitionmodel.ActionSchema, actionmodel.ActionInvocation, error) {
	var in actionmodel.ActionInvocation
	p, err := h.principal(ctx, a)
	if err != nil {
		return definitionmodel.ActionSchema{}, in, err
	}
	action, ok := h.businessActionDefinition(q.ActionKey)
	if !ok || len(h.evidenceKey) == 0 || !resultRead && len(invocationcontract.ValidateActionPermission(action, p)) != 0 {
		return action, in, conversationBusinessError("forbidden")
	}
	if q.Version != h.businessActionVersion(action) {
		return action, in, businessActionError("conflict", "business_action_changed")
	}
	if action.ObjectKey != q.ObjectKey || action.PayloadFields == nil || q.RecordID == "" && !actionpolicy.ActionIsObjectKind(action.Kind) || q.RecordID != "" && !actionpolicy.ActionIsRecordKind(action.Kind) || len(q.RecordID) > 256 {
		return action, in, businessActionError("bad_request", "business_action_invalid")
	}
	if q.RecordID != "" && !resultRead {
		allowed, err := h.records.(conversationBusinessActionRecords).RecordScopeAllowsAction(ctx, action.ObjectKey, q.RecordID, actionpolicy.ActionName(action), p)
		if err != nil {
			return action, in, businessActionHostError(err)
		}
		if !allowed {
			return action, in, conversationBusinessError("forbidden")
		}
	}
	var data map[string]any
	decoder := json.NewDecoder(bytes.NewReader(q.Data))
	decoder.UseNumber()
	if decoder.Decode(&data) != nil || data == nil || decoder.Decode(new(any)) != io.EOF || len(data) > 100 {
		return action, in, businessActionError("bad_request", "business_action_invalid")
	}
	declared := map[string]bool{}
	inputContract := action
	inputContract.PayloadFields = businessActionPayloadFields(action)
	for _, field := range inputContract.PayloadFields {
		declared[field.Key] = true
	}
	for key := range data {
		if !declared[key] {
			return action, in, businessActionError("bad_request", "business_action_invalid")
		}
	}
	payload, err := actionapplication.ActionNormalizePayload(inputContract, data)
	if err != nil {
		return action, in, businessActionHostError(err)
	}
	in = actionmodel.ActionInvocation{ActionKey: action.Key, ObjectKey: action.ObjectKey, RecordID: q.RecordID, Input: payload, Principal: p, Source: actionmodel.ActionSourceAgent, PreventExecutionReclaim: true}
	return action, in, nil
}

func (h *ConversationBusinessHost) AuthorizeBusinessAction(ctx context.Context, q agentsdk.ConversationBusinessAction, a agentsdk.ConversationAuthority) (agentsdk.ConversationToolAuthorization, error) {
	if h.actions == nil || len(h.evidenceKey) == 0 {
		return agentsdk.ConversationToolAuthorization{}, nil
	}
	if q.ActionKey == "" && q.ObjectKey == "" && q.Version == "" && q.RecordID == "" && len(q.Data) == 0 {
		_, err := h.principal(ctx, a)
		return agentsdk.ConversationToolAuthorization{Granted: err == nil}, err
	}
	_, in, err := h.prepareBusinessAction(ctx, q, a)
	if err != nil {
		return agentsdk.ConversationToolAuthorization{}, err
	}
	return agentsdk.ConversationToolAuthorization{Granted: true, Revision: in.Principal.AuthorizationRevision}, nil
}

func businessActionAcknowledgement(in actionmodel.ActionInvocation, result actionmodel.ActionInvocationResult) agentsdk.ConversationBusinessActionResult {
	if result.InvocationID != in.IdempotencyKey || (result.Record == nil) == (result.Object == nil) {
		return agentsdk.ConversationBusinessActionResult{Status: "uncertain"}
	}
	out := agentsdk.ConversationBusinessActionResult{Status: "completed", InvocationID: in.IdempotencyKey, ObjectKey: in.ObjectKey, ActionKey: in.ActionKey, RecordID: in.RecordID}
	remaining := 100
	refs := func(items []actionmodel.ActionObjectRecordRef) []agentsdk.ConversationBusinessRecordReference {
		out.ReferenceCount += len(items)
		var selected []agentsdk.ConversationBusinessRecordReference
		for _, ref := range items {
			if remaining == 0 {
				out.ReferencesTruncated = true
				break
			}
			remaining--
			selected = append(selected, agentsdk.ConversationBusinessRecordReference{ObjectKey: ref.ObjectKey, RecordID: ref.RecordID})
		}
		return selected
	}
	if result.Record != nil {
		out.CreatedRecords, out.UpdatedRecords, out.DeletedRecords, out.RestoredRecords = refs(result.Record.CreatedRecords), refs(result.Record.UpdatedRecords), refs(result.Record.DeletedRecords), refs(result.Record.RestoredRecords)
	}
	if result.Object != nil {
		out.CreatedRecords, out.UpdatedRecords, out.DeletedRecords, out.RestoredRecords = refs(result.Object.CreatedRecords), refs(result.Object.UpdatedRecords), refs(result.Object.DeletedRecords), refs(result.Object.RestoredRecords)
	}
	return out
}

func (h *ConversationBusinessHost) runBusinessAction(ctx context.Context, request agentsdk.ConversationBusinessActionRequest, reconcile bool) (agentsdk.ConversationBusinessActionResult, error) {
	unknown := agentsdk.ConversationBusinessActionResult{Status: "uncertain"}
	_, in, err := h.prepareBusinessAction(ctx, request.Action, request.Authority)
	if err != nil {
		return agentsdk.ConversationBusinessActionResult{}, err
	}
	confirmation := request.Confirmation
	if confirmation == nil || confirmation.ID == "" || confirmation.UserID != request.Authority.UserID || confirmation.ActionKey != agentsdk.ConversationToolActionPrefix+"invoke_action" || confirmation.ToolVersion != "1" || confirmation.ApprovedAt.IsZero() || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 256 || request.ConversationID == "" || request.RunID == "" || request.CallID == "" {
		return agentsdk.ConversationBusinessActionResult{}, conversationBusinessError("forbidden")
	}
	var confirmed agentsdk.ConversationBusinessAction
	if json.Unmarshal([]byte(request.Arguments), &confirmed) != nil || conversationBusinessDigest(confirmed) != conversationBusinessDigest(request.Action) || confirmation.ArgumentsHash != conversationBusinessDigest(request.Arguments) {
		return agentsdk.ConversationBusinessActionResult{}, conversationBusinessError("forbidden")
	}
	in.IdempotencyKey = request.IdempotencyKey
	in.RequestID = "conversation:" + request.RunID + ":" + request.CallID
	in.Principal.RequestID = in.RequestID
	in.Principal.CorrelationID = request.RunID
	if reconcile {
		stored, err := h.actions.InspectInvocation(ctx, in)
		if err != nil {
			return unknown, nil
		}
		if stored.Found {
			if stored.Status == string(idempotency.StatusSucceeded) {
				return businessActionAcknowledgement(in, stored.Result), nil
			}
			if stored.Status == string(idempotency.StatusFailedTerminal) {
				return agentsdk.ConversationBusinessActionResult{Status: "failed", ErrorCode: "business_action_invalid"}, nil
			}
			return unknown, nil
		}
	}
	// Invoke's create-or-replay claim cannot reclaim any existing unresolved
	// receipt, even if one appeared after the read above or its lease expired.
	result, err := h.actions.Invoke(ctx, actionmodel.ActionSourceAgent, in)
	if err != nil {
		switch apperror.KindOf(err) {
		case apperror.KindBadRequest, apperror.KindForbidden, apperror.KindNotFound:
			mapped := businessActionHostError(err)
			if e, ok := mapped.(*agentsdk.Error); ok {
				return agentsdk.ConversationBusinessActionResult{Status: "failed", ErrorCode: e.Code}, nil
			}
		case apperror.KindConflict:
			if apperror.CodeOf(err) != idempotency.ErrorCodeInProgress {
				return agentsdk.ConversationBusinessActionResult{Status: "failed", ErrorCode: businessActionHostError(err).(*agentsdk.Error).Code}, nil
			}
		}
		return unknown, nil
	}
	return businessActionAcknowledgement(in, result), nil
}

func (h *ConversationBusinessHost) InvokeBusinessAction(ctx context.Context, in agentsdk.ConversationBusinessActionRequest) (agentsdk.ConversationBusinessActionResult, error) {
	return h.runBusinessAction(ctx, in, false)
}
func (h *ConversationBusinessHost) ReconcileBusinessAction(ctx context.Context, in agentsdk.ConversationBusinessActionRequest) (agentsdk.ConversationBusinessActionResult, error) {
	return h.runBusinessAction(ctx, in, true)
}

func (h *ConversationBusinessHost) RevalidateBusinessAction(ctx context.Context, e agentsdk.ConversationBusinessEvidence, a agentsdk.ConversationAuthority) error {
	if e.Version != 1 || e.Operation != "invoke_action" || e.Source != h.source || e.ScopeSHA256 != conversationBusinessDigest([]string{h.source, a.RuntimeID, a.WorkspaceID, a.UserID}) {
		return conversationBusinessError("forbidden")
	}
	var q agentsdk.ConversationBusinessAction
	var saved agentsdk.ConversationBusinessActionResult
	if json.Unmarshal(e.Input, &q) != nil || json.Unmarshal(e.Data, &saved) != nil || saved.Status != "completed" || saved.InvocationID == "" {
		return conversationBusinessError("forbidden")
	}
	_, in, err := h.prepareBusinessAction(ctx, q, a)
	if err != nil {
		return err
	}
	in.IdempotencyKey = saved.InvocationID
	stored, err := h.actions.InspectInvocation(ctx, in)
	if err != nil || !stored.Found || stored.Status != string(idempotency.StatusSucceeded) {
		return conversationBusinessError("forbidden")
	}
	current, err := json.Marshal(businessActionAcknowledgement(in, stored.Result))
	if err != nil || !bytes.Equal(current, e.Data) {
		return conversationBusinessError("forbidden")
	}
	return nil
}

var _ agentsdk.ConversationBusinessActionSource = (*ConversationBusinessHost)(nil)
