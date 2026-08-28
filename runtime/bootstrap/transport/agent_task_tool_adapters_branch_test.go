package transport

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	agentstate "github.com/domainry/domainry-runtime/runtime/application/agent"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type agentTaskRecordStub struct {
	page       recordmodel.RecordPageResult
	record     recordmodel.Record
	err        error
	relatedErr error
}

func (s agentTaskRecordStub) ListRecords(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	return s.page, s.err
}
func (s agentTaskRecordStub) GetRecord(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error) {
	return s.record, s.err
}
func (s agentTaskRecordStub) RelatedRecords(context.Context, string, string, string, recordservice.RecordRelatedRecordsRequest, principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	return s.page, s.relatedErr
}

type agentTaskActionStub struct {
	definitions []definitionmodel.ActionSchema
	result      actionmodel.ActionInvocationResult
	err         error
}

func (s agentTaskActionStub) Invoke(context.Context, actionmodel.ActionSource, actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
	return s.result, s.err
}
func (s agentTaskActionStub) Definitions() []definitionmodel.ActionSchema { return s.definitions }

type agentTaskProposalStub struct {
	result agentstate.AgentProposal
	err    error
}

func (s agentTaskProposalStub) StoreProposal(context.Context, agentstate.AgentProposal) (agentstate.AgentProposal, error) {
	return s.result, s.err
}

func TestAgentTaskQueryAdapterRemainingBranches(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	scope := agentapplication.AgentToolFieldScope{VisibleFields: map[string][]string{"customer": {"name"}, "order": {"number"}}}
	wantErr := errors.New("records failure")
	if _, err := (agentTaskToolQueryAdapter{}).QueryAgentRecords(t.Context(), "customer", nil, scope, principal); apperror.CodeOf(err) != "agent.tool.query_unavailable" {
		t.Fatalf("err=%v", err)
	}
	adapter := agentTaskToolQueryAdapter{records: agentTaskRecordStub{err: wantErr}}
	if _, err := adapter.QueryAgentRecords(t.Context(), "customer", map[string]any{"page": 0}, scope, principal); apperror.CodeOf(err) != "agent.tool.query_invalid" {
		t.Fatalf("err=%v", err)
	}
	if _, err := adapter.QueryAgentRecords(t.Context(), "customer", nil, scope, principal); !errors.Is(err, wantErr) {
		t.Fatalf("err=%v", err)
	}
	page := recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "one", Data: map[string]any{"name": "Ada", "secret": "x"}}}}
	adapter = agentTaskToolQueryAdapter{records: agentTaskRecordStub{page: page}}
	if _, err := adapter.QueryAgentRecords(t.Context(), "customer", nil, scope, principal); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.QueryAgentRecords(t.Context(), "customer", map[string]any{"relations": []any{map[string]any{"object_key": "order", "limit": 1}}}, scope, principal); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.expandRelations(t.Context(), "customer", page, []agentRelationRequest{{ObjectKey: "secret"}}, scope, principal); apperror.CodeOf(err) != "agent.tool.relation_denied" {
		t.Fatalf("err=%v", err)
	}
	adapter.records = agentTaskRecordStub{page: page, relatedErr: wantErr}
	if _, err := adapter.expandRelations(t.Context(), "customer", page, []agentRelationRequest{{ObjectKey: "order"}}, scope, principal); !errors.Is(err, wantErr) {
		t.Fatalf("err=%v", err)
	}
	adapter.records = agentTaskRecordStub{page: page}
	if result, err := adapter.expandRelations(t.Context(), "customer", page, []agentRelationRequest{{ObjectKey: "order"}}, scope, principal); err != nil || result == nil {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if _, err := (agentTaskToolQueryAdapter{}).GetAgentRecord(t.Context(), "customer", "one", scope, principal); apperror.CodeOf(err) != "agent.tool.query_unavailable" {
		t.Fatalf("err=%v", err)
	}
	if _, err := (agentTaskToolQueryAdapter{records: agentTaskRecordStub{err: wantErr}}).GetAgentRecord(t.Context(), "customer", "one", scope, principal); !errors.Is(err, wantErr) {
		t.Fatalf("err=%v", err)
	}
	if result, err := (agentTaskToolQueryAdapter{records: agentTaskRecordStub{record: page.Items[0]}}).GetAgentRecord(t.Context(), "customer", "one", scope, principal); err != nil || result == nil {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestAgentTaskActionRiskProposalAndPrimitiveBranches(t *testing.T) {
	wantErr := errors.New("adapter failure")
	request := agentapplication.AgentToolActionInvocation{}
	if _, err := (agentTaskToolActionAdapter{}).InvokeAgentAction(t.Context(), request); apperror.CodeOf(err) != "agent.tool.action_unavailable" {
		t.Fatalf("err=%v", err)
	}
	if _, err := (agentTaskToolActionAdapter{actions: agentTaskActionStub{err: wantErr}}).InvokeAgentAction(t.Context(), request); !errors.Is(err, wantErr) {
		t.Fatalf("err=%v", err)
	}
	if _, err := (agentTaskToolActionAdapter{actions: agentTaskActionStub{}}).InvokeAgentAction(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (agentTaskToolRiskAdapter{}).RequiresAgentProposal(t.Context(), "action", principalmodel.Principal{}); apperror.CodeOf(err) != "agent.tool.risk_policy_unavailable" {
		t.Fatalf("err=%v", err)
	}
	risk := agentTaskToolRiskAdapter{actions: agentTaskActionStub{definitions: []definitionmodel.ActionSchema{{Key: "other"}, {Key: "action", RiskLevel: "critical"}}}}
	if required, level, err := risk.RequiresAgentProposal(t.Context(), " action ", principalmodel.Principal{}); err != nil || !required || level != "critical" {
		t.Fatalf("required=%v level=%s err=%v", required, level, err)
	}
	if _, _, err := risk.RequiresAgentProposal(t.Context(), "missing", principalmodel.Principal{}); apperror.CodeOf(err) != "agent.tool.action_unknown" {
		t.Fatalf("err=%v", err)
	}
	if _, err := (agentTaskToolProposalAdapter{}).CreateAgentActionProposal(t.Context(), agentapplication.AgentToolProposalRequest{}); apperror.CodeOf(err) != "agent.tool.proposal_unavailable" {
		t.Fatalf("err=%v", err)
	}
	if _, err := (agentTaskToolProposalAdapter{state: agentTaskProposalStub{err: wantErr}}).CreateAgentActionProposal(t.Context(), agentapplication.AgentToolProposalRequest{}); !errors.Is(err, wantErr) {
		t.Fatalf("err=%v", err)
	}
	if result, err := (agentTaskToolProposalAdapter{state: agentTaskProposalStub{result: agentstate.AgentProposal{ProposalID: "proposal"}}}).CreateAgentActionProposal(t.Context(), agentapplication.AgentToolProposalRequest{}); err != nil || result.ProposalID != "proposal" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	record := recordmodel.Record{Data: map[string]any{"name": "Ada"}}
	projectAgentRecord(&record, []string{"name"})
	if agentToolInt(3, 1) != 3 || len(agentToolMapValue(map[string]any{"x": 1})) != 1 || len(agentToolList([]any{1})) != 1 {
		t.Fatal("primitive decoder branch")
	}
	if _, _, err := decodeAgentRecordQuery(map[string]any{"search": nil, "sort": []any{map[string]any{"field": "name", "direction": "sideways"}}}, agentapplication.AgentToolFieldScope{}); apperror.CodeOf(err) != "agent.tool.query_invalid" {
		t.Fatalf("err=%v", err)
	}
	invalidQueries := []map[string]any{
		{"page_size": 0},
		{"sort": []any{map[string]any{"field": "", "direction": "asc"}}},
		{"sort": []any{map[string]any{"field": "name", "direction": "desc"}}},
		{"relations": []any{map[string]any{"object_key": "", "limit": 1}}},
		{"relations": []any{map[string]any{"object_key": nil, "limit": 1}}},
		{"relations": []any{map[string]any{"object_key": "order", "limit": 0}}},
	}
	for index, raw := range invalidQueries {
		_, _, _ = decodeAgentRecordQuery(raw, agentapplication.AgentToolFieldScope{VisibleFields: map[string][]string{"order": {"number"}}})
		_ = index
	}
	_, _, _ = decodeAgentRecordQuery(map[string]any{"sort": []any{map[string]any{"field": "name", "direction": "asc"}}}, agentapplication.AgentToolFieldScope{})
}
