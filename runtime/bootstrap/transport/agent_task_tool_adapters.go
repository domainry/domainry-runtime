package transport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	agentstate "github.com/domainry/domainry-runtime/runtime/application/agent"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
)

type agentTaskToolQueryAdapter struct {
	records agentTaskRecordService
}

type agentTaskRecordService interface {
	ListRecords(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error)
	GetRecord(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error)
	RelatedRecords(context.Context, string, string, string, recordservice.RecordRelatedRecordsRequest, principalmodel.Principal) (recordmodel.RecordPageResult, error)
}

func (a agentTaskToolQueryAdapter) QueryAgentRecords(ctx context.Context, objectKey string, raw map[string]any, scope agentapplication.AgentToolFieldScope, principal principalmodel.Principal) (any, error) {
	if a.records == nil {
		return nil, apperror.New(apperror.KindUnavailable, "agent.tool.query_unavailable", nil, nil)
	}
	query, relations, err := decodeAgentRecordQuery(raw, scope)
	if err != nil {
		return nil, err
	}
	query.SelectFields = append([]string(nil), scope.VisibleFields[objectKey]...)
	page, err := a.records.ListRecords(ctx, objectKey, query, principal)
	if err != nil {
		return nil, err
	}
	projectAgentRecordPage(&page, scope.VisibleFields[objectKey])
	if len(relations) == 0 {
		return page, nil
	}
	return a.expandRelations(ctx, objectKey, page, relations, scope, principal)
}

func (a agentTaskToolQueryAdapter) GetAgentRecord(ctx context.Context, objectKey, recordID string, scope agentapplication.AgentToolFieldScope, principal principalmodel.Principal) (any, error) {
	if a.records == nil {
		return nil, apperror.New(apperror.KindUnavailable, "agent.tool.query_unavailable", nil, nil)
	}
	record, err := a.records.GetRecord(ctx, objectKey, recordID, principal)
	if err != nil {
		return nil, err
	}
	projectAgentRecord(&record, scope.VisibleFields[objectKey])
	return record, nil
}

func (a agentTaskToolQueryAdapter) expandRelations(ctx context.Context, objectKey string, page recordmodel.RecordPageResult, relations []agentRelationRequest, scope agentapplication.AgentToolFieldScope, principal principalmodel.Principal) (any, error) {
	items := make([]map[string]any, 0, len(page.Items))
	for _, record := range page.Items {
		related := map[string]recordmodel.RecordPageResult{}
		for _, relation := range relations {
			fields, allowed := scope.VisibleFields[relation.ObjectKey]
			if !allowed {
				return nil, apperror.New(apperror.KindForbidden, "agent.tool.relation_denied", nil, nil)
			}
			result, err := a.records.RelatedRecords(ctx, objectKey, record.ID, relation.ObjectKey, recordservice.RecordRelatedRecordsRequest{Page: 1, PageSize: relation.Limit, FieldKey: relation.FieldKey}, principal)
			if err != nil {
				return nil, err
			}
			projectAgentRecordPage(&result, fields)
			related[relation.ObjectKey] = result
		}
		items = append(items, map[string]any{"record": record, "relations": related})
	}
	return map[string]any{"items": items, "page": page.Page, "page_size": page.PageSize, "total": page.Total, "has_next": page.HasNext}, nil
}

type agentRelationRequest struct {
	ObjectKey string
	FieldKey  string
	Limit     int
}

func decodeAgentRecordQuery(raw map[string]any, scope agentapplication.AgentToolFieldScope) (recordmodel.RecordListQuery, []agentRelationRequest, error) {
	query := recordmodel.RecordListQuery{Page: agentToolInt(raw["page"], 1), PageSize: agentToolInt(raw["page_size"], 20), Search: strings.TrimSpace(fmt.Sprint(raw["search"])), Filters: agentToolMapValue(raw["filters"])}
	if query.Page < 1 {
		return query, nil, apperror.New(apperror.KindBadRequest, "agent.tool.query_invalid", nil, nil)
	}
	if query.PageSize < 1 {
		return query, nil, apperror.New(apperror.KindBadRequest, "agent.tool.query_invalid", nil, nil)
	}
	if query.PageSize > 25 {
		return query, nil, apperror.New(apperror.KindBadRequest, "agent.tool.query_invalid", nil, nil)
	}
	if query.Search == "<nil>" {
		query.Search = ""
	}
	for _, item := range agentToolList(raw["sort"]) {
		entry := agentToolMapValue(item)
		field, direction := strings.TrimSpace(fmt.Sprint(entry["field"])), strings.ToLower(strings.TrimSpace(fmt.Sprint(entry["direction"])))
		if field == "" {
			return query, nil, apperror.New(apperror.KindBadRequest, "agent.tool.query_invalid", nil, nil)
		}
		if direction != "asc" && direction != "desc" {
			return query, nil, apperror.New(apperror.KindBadRequest, "agent.tool.query_invalid", nil, nil)
		}
		query.Sort = append(query.Sort, recordmodel.RecordSortRule{Field: field, Direction: direction})
	}
	relations := []agentRelationRequest{}
	for _, item := range agentToolList(raw["relations"]) {
		entry := agentToolMapValue(item)
		relation := agentRelationRequest{ObjectKey: strings.TrimSpace(fmt.Sprint(entry["object_key"])), FieldKey: strings.TrimSpace(fmt.Sprint(entry["field_key"])), Limit: agentToolInt(entry["limit"], 10)}
		if relation.ObjectKey == "" {
			return query, nil, apperror.New(apperror.KindBadRequest, "agent.tool.relation_invalid", nil, nil)
		}
		if relation.ObjectKey == "<nil>" {
			return query, nil, apperror.New(apperror.KindBadRequest, "agent.tool.relation_invalid", nil, nil)
		}
		if relation.Limit < 1 {
			return query, nil, apperror.New(apperror.KindBadRequest, "agent.tool.relation_invalid", nil, nil)
		}
		if relation.Limit > 10 {
			return query, nil, apperror.New(apperror.KindBadRequest, "agent.tool.relation_invalid", nil, nil)
		}
		if _, allowed := scope.VisibleFields[relation.ObjectKey]; !allowed {
			return query, nil, apperror.New(apperror.KindForbidden, "agent.tool.relation_denied", nil, nil)
		}
		relations = append(relations, relation)
		if len(relations) > 5 {
			return query, nil, apperror.New(apperror.KindBadRequest, "agent.tool.relation_limit_exceeded", nil, nil)
		}
	}
	return query, relations, nil
}

type agentTaskToolActionAdapter struct {
	actions agentTaskActionService
}

type agentTaskActionService interface {
	Invoke(context.Context, actionmodel.ActionSource, actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error)
	Definitions() []definitionmodel.ActionSchema
}

func (a agentTaskToolActionAdapter) InvokeAgentAction(ctx context.Context, request agentapplication.AgentToolActionInvocation) (agentapplication.AgentToolActionInvocationResult, error) {
	if a.actions == nil {
		return agentapplication.AgentToolActionInvocationResult{}, apperror.New(apperror.KindUnavailable, "agent.tool.action_unavailable", nil, nil)
	}
	result, err := a.actions.Invoke(ctx, actionmodel.ActionSourceAgent, actionmodel.ActionInvocation{ActionKey: request.ActionKey, ObjectKey: request.ObjectKey, RecordID: request.RecordID, Input: request.Input, Principal: request.Principal, Actor: request.Principal, RequestID: request.RequestID, IdempotencyKey: request.IdempotencyKey})
	return agentapplication.AgentToolActionInvocationResult{Record: result.Record, Object: result.Object}, err
}

type agentTaskToolRiskAdapter struct {
	actions agentTaskActionService
}

func (a agentTaskToolRiskAdapter) RequiresAgentProposal(_ context.Context, actionKey string, _ principalmodel.Principal) (bool, string, error) {
	if a.actions == nil {
		return false, "", apperror.New(apperror.KindUnavailable, "agent.tool.risk_policy_unavailable", nil, nil)
	}
	for _, action := range a.actions.Definitions() {
		if action.Key != strings.TrimSpace(actionKey) {
			continue
		}
		risk := actionmodel.ActionRiskLevel(action)
		return risk == "high" || risk == "critical", risk, nil
	}
	return false, "", apperror.New(apperror.KindNotFound, "agent.tool.action_unknown", nil, nil)
}

type agentTaskToolProposalAdapter struct {
	state agentTaskProposalState
}

type agentTaskProposalState interface {
	StoreProposal(context.Context, agentstate.AgentProposal) (agentstate.AgentProposal, error)
}

func (a agentTaskToolProposalAdapter) CreateAgentActionProposal(ctx context.Context, request agentapplication.AgentToolProposalRequest) (agentapplication.AgentToolProposalResult, error) {
	if a.state == nil {
		return agentapplication.AgentToolProposalResult{}, apperror.New(apperror.KindUnavailable, "agent.tool.proposal_unavailable", nil, nil)
	}
	hash := sha256.Sum256([]byte(strings.Join([]string{request.Principal.WorkspaceID, request.ProcessID, request.TaskRunID, request.InteractiveRunID, request.IdempotencyKey, request.ActionKey, request.ObjectKey, request.RecordID}, "\x00")))
	proposal := agentstate.AgentProposal{
		ProposalID: "agent-task-" + hex.EncodeToString(hash[:12]), Status: "draft",
		Title: "Agent action proposal: " + request.ActionKey, Source: "agent_task", Reference: request.TaskRunID,
		Actor: request.Identity.Execution.UserID, WorkspaceID: request.Principal.WorkspaceID, UserID: request.Principal.UserID, Role: request.Principal.RoleKey,
		Proposed: map[string]any{"action_binding": map[string]any{"action_key": request.ActionKey, "object_key": request.ObjectKey, "record_id": request.RecordID, "data": request.Input, "guarded_write": true}},
		Metadata: map[string]any{"process_id": request.ProcessID, "node_instance_id": request.NodeInstanceID, "task_run_id": request.TaskRunID, "attempt": request.Attempt, "interactive_run_id": request.InteractiveRunID, "session_id": request.SessionID, "entrypoint_key": request.EntrypointKey, "surface": request.Surface, "route_key": request.RouteKey, "context_revision": request.ContextRevision, "idempotency_key": request.IdempotencyKey, "execution_mode": request.Identity.Mode, "service_principal_key": request.Identity.ServicePrincipalKey},
	}
	stored, err := a.state.StoreProposal(ctx, proposal)
	return agentapplication.AgentToolProposalResult{ProposalID: stored.ProposalID, Value: stored}, err
}

func projectAgentRecordPage(page *recordmodel.RecordPageResult, fields []string) {
	for index := range page.Items {
		projectAgentRecord(&page.Items[index], fields)
	}
}

func projectAgentRecord(record *recordmodel.Record, fields []string) {
	allowed := map[string]struct{}{}
	for _, field := range fields {
		allowed[field] = struct{}{}
	}
	for key := range record.Data {
		if _, ok := allowed[key]; !ok {
			delete(record.Data, key)
		}
	}
}

func agentToolInt(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return fallback
	}
}

func agentToolMapValue(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{}
}

func agentToolList(value any) []any {
	if typed, ok := value.([]any); ok {
		return typed
	}
	return nil
}
