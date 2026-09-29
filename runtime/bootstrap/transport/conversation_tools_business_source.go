package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

type conversationBusinessEvidenceSource interface {
	BusinessSourceIdentity() string
	QueryBusinessRecords(context.Context, agentsdk.ConversationBusinessQuery, agentsdk.ConversationAuthority) (agentsdk.ConversationBusinessRecordPage, error)
	QueryRelatedBusinessRecords(context.Context, agentsdk.ConversationBusinessRelatedQuery, agentsdk.ConversationAuthority) (agentsdk.ConversationBusinessRelatedPage, error)
	GetBusinessRecord(context.Context, agentsdk.ConversationBusinessGet, agentsdk.ConversationAuthority) (agentsdk.ConversationBusinessRecord, error)
	SealBusinessEvidence(context.Context, agentsdk.ConversationBusinessEvidence, agentsdk.ConversationAuthority) (string, error)
	RevalidateBusiness(context.Context, agentsdk.ConversationBusinessEvidence, agentsdk.ConversationAuthority) error
}

type conversationBusinessActionEvidenceSource interface {
	conversationBusinessEvidenceSource
	ResolveBusinessAction(context.Context, agentsdk.ConversationBusinessActionIntent, agentsdk.ConversationAuthority) (agentsdk.ConversationBusinessAction, agentsdk.ConversationToolAuthorization, error)
	InvokeResolvedBusinessAction(context.Context, agentsdk.ConversationBusinessActionRequest) (agentsdk.ConversationBusinessActionResult, error)
	ReconcileResolvedBusinessAction(context.Context, agentsdk.ConversationBusinessActionRequest) (agentsdk.ConversationBusinessActionResult, error)
	RevalidateBusinessAction(context.Context, agentsdk.ConversationBusinessEvidence, agentsdk.ConversationAuthority) error
}

type conversationToolsBusinessSource struct {
	source conversationBusinessEvidenceSource
}

func (s conversationToolsBusinessSource) BusinessSourceIdentity() string {
	if s.source == nil {
		return ""
	}
	return s.source.BusinessSourceIdentity()
}

func conversationToolsBusinessScope(source string, authority toolsdk.Authority) string {
	raw, _ := json.Marshal([]string{source, authority.RuntimeID, authority.WorkspaceID, authority.UserID})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func decodeConversationBusinessInput(raw json.RawMessage, target any) error {
	if len(raw) == 0 || len(raw) > 64*1024 || !json.Valid(raw) {
		return fmt.Errorf("conversation business input is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("conversation business input is invalid")
	}
	return nil
}

func (s conversationToolsBusinessSource) ReadConversationBusiness(ctx context.Context, request toolsdk.ConversationBusinessRead, authority toolsdk.Authority) (toolsdk.ConversationBusinessEvidence, error) {
	identity := strings.TrimSpace(s.BusinessSourceIdentity())
	if identity == "" || !authority.Known || strings.TrimSpace(authority.RuntimeID) == "" || strings.TrimSpace(authority.WorkspaceID) == "" || strings.TrimSpace(authority.UserID) == "" || !json.Valid(request.Input) {
		return toolsdk.ConversationBusinessEvidence{}, fmt.Errorf("conversation business source is unavailable")
	}
	var value any
	var err error
	switch strings.TrimSpace(request.Operation) {
	case "query_records":
		var query agentsdk.ConversationBusinessQuery
		if decodeConversationBusinessInput(request.Input, &query) != nil {
			return toolsdk.ConversationBusinessEvidence{}, fmt.Errorf("conversation business query is invalid")
		}
		value, err = s.source.QueryBusinessRecords(ctx, query, authority)
	case "get_record":
		var query agentsdk.ConversationBusinessGet
		if decodeConversationBusinessInput(request.Input, &query) != nil {
			return toolsdk.ConversationBusinessEvidence{}, fmt.Errorf("conversation business get is invalid")
		}
		value, err = s.source.GetBusinessRecord(ctx, query, authority)
	case "query_related_records":
		var query agentsdk.ConversationBusinessRelatedQuery
		if decodeConversationBusinessInput(request.Input, &query) != nil {
			return toolsdk.ConversationBusinessEvidence{}, fmt.Errorf("conversation business related query is invalid")
		}
		value, err = s.source.QueryRelatedBusinessRecords(ctx, query, authority)
	default:
		return toolsdk.ConversationBusinessEvidence{}, fmt.Errorf("unsupported conversation business operation %q", request.Operation)
	}
	if err != nil {
		return toolsdk.ConversationBusinessEvidence{}, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return toolsdk.ConversationBusinessEvidence{}, err
	}
	evidence := agentsdk.ConversationBusinessEvidence{
		Version: 1, Source: identity, ScopeSHA256: conversationToolsBusinessScope(identity, authority),
		Operation: strings.TrimSpace(request.Operation), Input: append(json.RawMessage(nil), request.Input...), Data: data,
	}
	evidence.HostProof, err = s.source.SealBusinessEvidence(ctx, evidence, authority)
	if err != nil {
		return toolsdk.ConversationBusinessEvidence{}, err
	}
	return toolsdk.ConversationBusinessEvidence{
		Version: evidence.Version, Source: evidence.Source, ScopeSHA256: evidence.ScopeSHA256,
		Operation: evidence.Operation, Input: evidence.Input, Data: evidence.Data, HostProof: evidence.HostProof,
	}, nil
}

func (s conversationToolsBusinessSource) RevalidateConversationBusiness(ctx context.Context, evidence toolsdk.ConversationBusinessEvidence, authority toolsdk.Authority) error {
	if s.source == nil {
		return fmt.Errorf("conversation business source is unavailable")
	}
	return s.source.RevalidateBusiness(ctx, agentsdk.ConversationBusinessEvidence{
		Version: evidence.Version, Source: evidence.Source, ScopeSHA256: evidence.ScopeSHA256,
		Operation: evidence.Operation, Input: evidence.Input, Data: evidence.Data, HostProof: evidence.HostProof,
	}, authority)
}

func conversationToolsAction(action toolsdk.ConversationBusinessAction) agentsdk.ConversationBusinessActionIntent {
	return agentsdk.ConversationBusinessActionIntent{
		ObjectKey: action.ObjectKey, ActionKey: action.ActionKey, RecordID: action.RecordID,
		Data: append(json.RawMessage(nil), action.Data...),
	}
}

func (s conversationToolsBusinessSource) AuthorizeConversationBusinessAction(ctx context.Context, action toolsdk.ConversationBusinessAction, authority toolsdk.Authority) (toolsdk.Authorization, error) {
	source, ok := s.source.(conversationBusinessActionEvidenceSource)
	if !ok || source == nil {
		return toolsdk.Authorization{}, fmt.Errorf("conversation business source is unavailable")
	}
	_, auth, err := source.ResolveBusinessAction(ctx, conversationToolsAction(action), authority)
	return auth, err
}

func conversationToolsActionRequest(ctx context.Context, source conversationBusinessActionEvidenceSource, request toolsdk.ConversationBusinessActionRequest) (agentsdk.ConversationBusinessActionRequest, error) {
	resolved, auth, err := source.ResolveBusinessAction(ctx, conversationToolsAction(request.Action), request.Authority)
	if err != nil {
		return agentsdk.ConversationBusinessActionRequest{}, err
	}
	if !auth.Granted || auth.ConfirmationRequired {
		return agentsdk.ConversationBusinessActionRequest{}, fmt.Errorf("conversation business action is not authorized")
	}
	return agentsdk.ConversationBusinessActionRequest{
		Authority: request.Authority, Action: resolved, ToolActionKey: request.ToolActionKey, ToolVersion: request.ToolVersion,
		ConversationID: request.ConversationID, RunID: request.RunID, CorrelationID: request.CorrelationID,
		Step: request.Step, CallID: request.CallID, IdempotencyKey: request.IdempotencyKey,
		Confirmation: request.Confirmation, Arguments: request.Arguments,
	}, nil
}

func conversationToolsActionResult(identity string, authority toolsdk.Authority, action agentsdk.ConversationBusinessAction, result agentsdk.ConversationBusinessActionResult) (toolsdk.ConversationBusinessActionResult, error) {
	out := toolsdk.ConversationBusinessActionResult{
		Status: result.Status, ErrorCode: result.ErrorCode, InvocationID: result.InvocationID,
		ObjectKey: result.ObjectKey, ActionKey: result.ActionKey, RecordID: result.RecordID,
		ReferenceCount: result.ReferenceCount, ReferencesTruncated: result.ReferencesTruncated,
	}
	copyRefs := func(items []agentsdk.ConversationBusinessRecordReference) []toolsdk.ConversationBusinessRecordReference {
		refs := make([]toolsdk.ConversationBusinessRecordReference, len(items))
		for index, item := range items {
			refs[index] = toolsdk.ConversationBusinessRecordReference{ObjectKey: item.ObjectKey, RecordID: item.RecordID}
		}
		return refs
	}
	out.CreatedRecords = copyRefs(result.CreatedRecords)
	out.UpdatedRecords = copyRefs(result.UpdatedRecords)
	out.DeletedRecords = copyRefs(result.DeletedRecords)
	out.RestoredRecords = copyRefs(result.RestoredRecords)
	if result.Status != "completed" {
		return out, nil
	}
	input, err := json.Marshal(action)
	if err != nil {
		return toolsdk.ConversationBusinessActionResult{}, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return toolsdk.ConversationBusinessActionResult{}, err
	}
	out.Evidence = &toolsdk.ConversationBusinessEvidence{
		Version: 1, Source: identity, ScopeSHA256: conversationToolsBusinessScope(identity, authority),
		Operation: "invoke_action", Input: input, Data: data,
	}
	return out, nil
}

func (s conversationToolsBusinessSource) runConversationBusinessAction(ctx context.Context, request toolsdk.ConversationBusinessActionRequest, reconcile bool) (toolsdk.ConversationBusinessActionResult, error) {
	identity := strings.TrimSpace(s.BusinessSourceIdentity())
	source, ok := s.source.(conversationBusinessActionEvidenceSource)
	if !ok || source == nil || identity == "" {
		return toolsdk.ConversationBusinessActionResult{}, fmt.Errorf("conversation business source is unavailable")
	}
	resolved, err := conversationToolsActionRequest(ctx, source, request)
	if err != nil {
		return toolsdk.ConversationBusinessActionResult{}, err
	}
	var result agentsdk.ConversationBusinessActionResult
	if reconcile {
		result, err = source.ReconcileResolvedBusinessAction(ctx, resolved)
	} else {
		result, err = source.InvokeResolvedBusinessAction(ctx, resolved)
	}
	if err != nil {
		return toolsdk.ConversationBusinessActionResult{}, err
	}
	return conversationToolsActionResult(identity, request.Authority, resolved.Action, result)
}

func (s conversationToolsBusinessSource) InvokeConversationBusinessAction(ctx context.Context, request toolsdk.ConversationBusinessActionRequest) (toolsdk.ConversationBusinessActionResult, error) {
	return s.runConversationBusinessAction(ctx, request, false)
}

func (s conversationToolsBusinessSource) ReconcileConversationBusinessAction(ctx context.Context, request toolsdk.ConversationBusinessActionRequest) (toolsdk.ConversationBusinessActionResult, error) {
	return s.runConversationBusinessAction(ctx, request, true)
}

func (s conversationToolsBusinessSource) RevalidateConversationBusinessAction(ctx context.Context, evidence toolsdk.ConversationBusinessEvidence, authority toolsdk.Authority) error {
	source, ok := s.source.(conversationBusinessActionEvidenceSource)
	if !ok || source == nil {
		return fmt.Errorf("conversation business source is unavailable")
	}
	return source.RevalidateBusinessAction(ctx, agentsdk.ConversationBusinessEvidence{
		Version: evidence.Version, Source: evidence.Source, ScopeSHA256: evidence.ScopeSHA256,
		Operation: evidence.Operation, Input: evidence.Input, Data: evidence.Data, HostProof: evidence.HostProof,
	}, authority)
}

var _ toolsdk.ConversationBusinessSource = conversationToolsBusinessSource{}
var _ toolsdk.ConversationBusinessActionSource = conversationToolsBusinessSource{}
