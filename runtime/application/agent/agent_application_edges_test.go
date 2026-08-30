package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

var errAgentRepository = errors.New("agent repository failed")

type agentRepositoryStub struct {
	listValues []agentmodel.AgentStateRecord
	listErr    error
	getValues  map[string]agentmodel.AgentStateRecord
	getFound   map[string]bool
	getErr     error
	putErr     error
	batchErr   error
	puts       []agentmodel.AgentStateRecord
	batches    [][]agentmodel.AgentStateRecord
}

func (r *agentRepositoryStub) List(context.Context, string, string, string, string) ([]agentmodel.AgentStateRecord, error) {
	return append([]agentmodel.AgentStateRecord(nil), r.listValues...), r.listErr
}

func (r *agentRepositoryStub) Get(_ context.Context, _ string, kind, _ string) (agentmodel.AgentStateRecord, bool, error) {
	if r.getErr != nil {
		return agentmodel.AgentStateRecord{}, false, r.getErr
	}
	return r.getValues[kind], r.getFound[kind], nil
}

func (r *agentRepositoryStub) Put(_ context.Context, _ string, value agentmodel.AgentStateRecord) error {
	r.puts = append(r.puts, value)
	return r.putErr
}

func (r *agentRepositoryStub) PutBatch(_ context.Context, _ string, values []agentmodel.AgentStateRecord) error {
	r.batches = append(r.batches, append([]agentmodel.AgentStateRecord(nil), values...))
	return r.batchErr
}

func (r *agentRepositoryStub) CompareAndSwap(_ context.Context, _ string, value agentmodel.AgentStateRecord, _ int64) (bool, error) {
	r.puts = append(r.puts, value)
	return r.putErr == nil, r.putErr
}

func TestAgentSessionListFiltersAndIncludesArchived(t *testing.T) {
	t.Parallel()

	principal := agentTestPrincipal()
	records := []AgentSession{
		{ExternalSessionID: "active-new", AgentSessionID: "agent-2", Title: "Customer Follow Up", LastSummary: "Priority", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey, Surface: "customer", ObjectKey: "customer", RecordID: "customer-1", UpdatedAt: 3},
		{ExternalSessionID: "active-old", Title: "Other", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey, Surface: "customer", ObjectKey: "customer", RecordID: "customer-1", UpdatedAt: 1},
		{ExternalSessionID: "archived", Title: "Archived", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey, Archived: true, UpdatedAt: 2},
		{ExternalSessionID: "other-user", WorkspaceID: principal.WorkspaceID, UserID: "other", Role: principal.RoleKey},
	}
	states := []agentmodel.AgentStateRecord{{Payload: json.RawMessage(`{`)}}
	for _, record := range records {
		payload, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		states = append(states, agentmodel.AgentStateRecord{Payload: payload})
	}
	repository := &agentRepositoryStub{listValues: states}
	service := NewAgentApplicationService(repository)
	all, err := service.ListSessions(t.Context(), AgentSessionQuery{IncludeArchived: true, Limit: 1000}, principal)
	if err != nil || len(all) != 3 || all[0].ExternalSessionID != "active-new" || all[1].ExternalSessionID != "archived" {
		t.Fatalf("all sessions=%#v err=%v", all, err)
	}
	active, err := service.ListSessions(t.Context(), AgentSessionQuery{Search: "follow", Surface: "customer", ObjectKey: "customer", RecordID: "customer-1", Limit: -1}, principal)
	if err != nil || len(active) != 1 || active[0].ExternalSessionID != "active-new" {
		t.Fatalf("filtered sessions=%#v err=%v", active, err)
	}
	limited, err := service.ListSessions(t.Context(), AgentSessionQuery{Limit: 1}, principal)
	if err != nil || len(limited) != 1 || limited[0].ExternalSessionID != "active-new" {
		t.Fatalf("limited sessions=%#v err=%v", limited, err)
	}
	for _, query := range []AgentSessionQuery{{Surface: "missing"}, {ObjectKey: "missing"}, {RecordID: "missing"}, {Search: "missing"}} {
		if got, err := service.ListSessions(t.Context(), query, principal); err != nil || len(got) != 0 {
			t.Errorf("query=%#v sessions=%#v err=%v", query, got, err)
		}
	}
	repository.listErr = errAgentRepository
	if _, err := service.ListSessions(t.Context(), AgentSessionQuery{}, principal); !errors.Is(err, errAgentRepository) {
		t.Fatalf("list repository error=%v", err)
	}
}

func TestAgentSessionUpsertAndArchiveEdges(t *testing.T) {
	t.Parallel()

	principal := agentTestPrincipal()
	previous := AgentSession{ExternalSessionID: "session-1", AgentSessionID: "agent-existing", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey, CreatedAt: 7}
	previousPayload, _ := json.Marshal(previous)
	repository := &agentRepositoryStub{getValues: map[string]agentmodel.AgentStateRecord{"session": {Payload: previousPayload}}, getFound: map[string]bool{"session": true}}
	service := NewAgentApplicationService(repository)
	updated, err := service.UpsertSession(t.Context(), AgentSessionUpsertRequest{ExternalSessionID: " session-1 ", LastSummary: " summary ", Context: map[string]any{"surface": " customer ", "object_key": " customer ", "record_id": nil}}, principal)
	if err != nil || updated.CreatedAt != 7 || updated.AgentSessionID != "agent-existing" || updated.Surface != "customer" || updated.RecordID != "" {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	if len(repository.puts) != 1 {
		t.Fatalf("puts=%d", len(repository.puts))
	}
	explicit, err := service.UpsertSession(t.Context(), AgentSessionUpsertRequest{ExternalSessionID: "session-1", AgentSessionID: "agent-replacement"}, principal)
	if err != nil || explicit.AgentSessionID != "agent-replacement" || explicit.CreatedAt != 7 {
		t.Fatalf("explicit agent session=%#v err=%v", explicit, err)
	}

	repository.getFound["session"] = false
	created, err := service.UpsertSession(t.Context(), AgentSessionUpsertRequest{}, principal)
	if err != nil || !strings.HasPrefix(created.ExternalSessionID, "session:user-a:") || created.Title != "New conversation" {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	repository.getErr = errAgentRepository
	if _, err := service.UpsertSession(t.Context(), AgentSessionUpsertRequest{ExternalSessionID: "failed"}, principal); !errors.Is(err, errAgentRepository) {
		t.Fatalf("upsert get error=%v", err)
	}
	repository.getErr = nil
	repository.putErr = errAgentRepository
	if _, err := service.UpsertSession(t.Context(), AgentSessionUpsertRequest{ExternalSessionID: "failed"}, principal); !errors.Is(err, errAgentRepository) {
		t.Fatalf("upsert put error=%v", err)
	}
	repository.putErr = nil
	if _, err := service.UpsertSession(t.Context(), AgentSessionUpsertRequest{ExternalSessionID: "invalid", Context: map[string]any{"bad": make(chan int)}}, principal); err == nil {
		t.Fatal("unencodable context must fail")
	}

	if _, err := service.SetSessionArchived(t.Context(), " ", true, principal); apperror.CodeOf(err) != "agent_dialog.session_id_required" {
		t.Fatalf("blank archive id error=%v", err)
	}
	repository.getFound["session"] = false
	if _, err := service.SetSessionArchived(t.Context(), "missing", true, principal); apperror.CodeOf(err) != "agent_dialog.session_not_found" {
		t.Fatalf("missing archive error=%v", err)
	}
	repository.getFound["session"] = true
	repository.getValues["session"] = agentmodel.AgentStateRecord{Payload: json.RawMessage(`{`)}
	if _, err := service.SetSessionArchived(t.Context(), "invalid", true, principal); apperror.CodeOf(err) != "agent_dialog.session_not_found" {
		t.Fatalf("invalid archive error=%v", err)
	}
	hidden := previous
	hidden.UserID = "other"
	hiddenPayload, _ := json.Marshal(hidden)
	repository.getValues["session"] = agentmodel.AgentStateRecord{Payload: hiddenPayload}
	if _, err := service.SetSessionArchived(t.Context(), "hidden", true, principal); apperror.CodeOf(err) != "agent_dialog.session_not_found" {
		t.Fatalf("hidden archive error=%v", err)
	}
	repository.getErr = errAgentRepository
	if _, err := service.SetSessionArchived(t.Context(), "failed", true, principal); !errors.Is(err, errAgentRepository) {
		t.Fatalf("archive get error=%v", err)
	}
	repository.getErr = nil
	repository.getValues["session"] = agentmodel.AgentStateRecord{Payload: previousPayload}
	repository.putErr = errAgentRepository
	if _, err := service.SetSessionArchived(t.Context(), "session-1", true, principal); !errors.Is(err, errAgentRepository) {
		t.Fatalf("archive put error=%v", err)
	}
}

func TestAgentSessionHelpers(t *testing.T) {
	t.Parallel()

	if agentStatePart(" ") != "anonymous" || agentStatePart(" a:b ") != "a_b" {
		t.Fatal("state part normalization mismatch")
	}
	if got := agentSessionTitle(" title ", "ignored"); got != "title" {
		t.Fatalf("explicit title=%q", got)
	}
	if got := agentSessionTitle("", " summary "); got != "summary" {
		t.Fatalf("summary title=%q", got)
	}
	long := strings.Repeat("界", 501)
	if got := agentSessionTitle(long, ""); len([]rune(got)) != 120 {
		t.Fatalf("title rune length=%d", len([]rune(got)))
	}
	if got := agentSessionSummary(long); len([]rune(got)) != 500 {
		t.Fatalf("summary rune length=%d", len([]rune(got)))
	}
	if truncateAgentText("short", 10) != "short" || len(cloneAgentContext(nil)) != 0 || agentContextString(map[string]any{}, "missing") != "" {
		t.Fatal("session helper fallback mismatch")
	}
}

func TestAgentProposalPersistenceEdges(t *testing.T) {
	t.Parallel()

	principal := agentTestPrincipal()
	visible := AgentProposal{ProposalID: "visible", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey, Status: "draft", UpdatedAt: 2}
	hidden := visible
	hidden.UserID, hidden.UpdatedAt = "other", 3
	otherStatus := visible
	otherStatus.ProposalID, otherStatus.Status, otherStatus.UpdatedAt = "approved", "approved", 1
	states := []agentmodel.AgentStateRecord{{Payload: json.RawMessage(`{`)}}
	for _, proposal := range []AgentProposal{visible, hidden, otherStatus} {
		payload, _ := json.Marshal(proposal)
		states = append(states, agentmodel.AgentStateRecord{Payload: payload})
	}
	repository := &agentRepositoryStub{listValues: states, getValues: map[string]agentmodel.AgentStateRecord{}, getFound: map[string]bool{}}
	service := NewAgentApplicationService(repository)
	listed, err := service.ListProposals(t.Context(), " draft ", principal)
	if err != nil || len(listed) != 1 || listed[0].ProposalID != "visible" {
		t.Fatalf("listed=%#v err=%v", listed, err)
	}
	all, err := service.ListProposals(t.Context(), "", principal)
	if err != nil || len(all) != 2 || all[0].ProposalID != "visible" || all[1].ProposalID != "approved" {
		t.Fatalf("all proposals=%#v err=%v", all, err)
	}
	repository.listErr = errAgentRepository
	if _, err := service.ListProposals(t.Context(), "", principal); !errors.Is(err, errAgentRepository) {
		t.Fatalf("proposal list error=%v", err)
	}
	repository.listErr = nil
	repository.getErr = errAgentRepository
	if _, err := service.GetProposal(t.Context(), "visible", principal); !errors.Is(err, errAgentRepository) {
		t.Fatalf("proposal get error=%v", err)
	}
	repository.getErr = nil
	repository.getFound["proposal"] = true
	repository.getValues["proposal"] = agentmodel.AgentStateRecord{Payload: json.RawMessage(`{`)}
	if _, err := service.GetProposal(t.Context(), "invalid", principal); apperror.CodeOf(err) != "agent_dialog.proposal_not_found" {
		t.Fatalf("invalid proposal error=%v", err)
	}
	hiddenPayload, _ := json.Marshal(hidden)
	repository.getValues["proposal"] = agentmodel.AgentStateRecord{Payload: hiddenPayload}
	if _, err := service.GetProposal(t.Context(), hidden.ProposalID, principal); apperror.CodeOf(err) != "agent_dialog.proposal_not_found" {
		t.Fatalf("hidden proposal error=%v", err)
	}
	if _, err := service.StoreProposal(t.Context(), AgentProposal{ProposalID: "invalid", WorkspaceID: principal.WorkspaceID, Proposed: map[string]any{"bad": make(chan int)}}); err == nil {
		t.Fatal("unencodable proposal must fail")
	}
	repository.putErr = errAgentRepository
	if _, err := service.StoreProposal(t.Context(), AgentProposal{ProposalID: "failed", WorkspaceID: principal.WorkspaceID}); !errors.Is(err, errAgentRepository) {
		t.Fatalf("proposal put error=%v", err)
	}
}

func TestAgentReportPersistenceEdges(t *testing.T) {
	t.Parallel()

	principal := agentTestPrincipal()
	repository := &agentRepositoryStub{getValues: map[string]agentmodel.AgentStateRecord{}, getFound: map[string]bool{}}
	service := NewAgentApplicationService(repository)
	repository.batchErr = errAgentRepository
	if _, err := service.RecordReportGovernance(t.Context(), AgentReportGovernanceRequest{QueryRef: "query"}, principal); !errors.Is(err, errAgentRepository) {
		t.Fatalf("record governance error=%v", err)
	}
	repository.batchErr = nil
	if _, err := service.PrepareReportHandoff(t.Context(), "missing", principal); apperror.CodeOf(err) != "agent_dialog.report_download_handoff_not_found" {
		t.Fatalf("missing query handoff error=%v", err)
	}

	query := AgentReportQueryRun{QueryRef: "query", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey}
	audit := AgentReportExportAudit{QueryRef: "query", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey}
	task := AgentReportDownloadTask{QueryRef: "query", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey}
	setReportState := func(kind string, value any) {
		t.Helper()
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		repository.getValues[kind] = agentmodel.AgentStateRecord{Payload: payload}
		repository.getFound[kind] = true
	}
	setReportState("report_query_run", query)
	if _, err := service.PrepareReportHandoff(t.Context(), "query", principal); apperror.CodeOf(err) != "agent_dialog.report_download_handoff_not_found" {
		t.Fatalf("missing audit handoff error=%v", err)
	}
	setReportState("report_export_audit", audit)
	if _, err := service.PrepareReportHandoff(t.Context(), "query", principal); apperror.CodeOf(err) != "agent_dialog.report_download_handoff_not_found" {
		t.Fatalf("missing task handoff error=%v", err)
	}
	setReportState("report_download_task", task)
	repository.batchErr = errAgentRepository
	if _, err := service.PrepareReportHandoff(t.Context(), "query", principal); !errors.Is(err, errAgentRepository) {
		t.Fatalf("handoff batch error=%v", err)
	}
	repository.batchErr = nil
	if _, err := service.PrepareReportHandoff(t.Context(), "query", principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("handoff scope error=%v", err)
	}
	if _, err := service.GetReportQueryRun(t.Context(), "query", principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("report query scope error=%v", err)
	}
	repository.getFound["report_query_run"] = false
	if _, err := service.GetReportQueryRun(t.Context(), "query", principal); apperror.CodeOf(err) != "agent_dialog.report_query_run_not_found" {
		t.Fatalf("missing report state error=%v", err)
	}
	repository.getFound["report_query_run"] = true
	repository.getErr = errAgentRepository
	if _, err := service.GetReportQueryRun(t.Context(), "query", principal); !errors.Is(err, errAgentRepository) {
		t.Fatalf("load report get error=%v", err)
	}
	repository.getErr = nil
	repository.getValues["report_query_run"] = agentmodel.AgentStateRecord{Payload: json.RawMessage(`{`)}
	if _, err := service.GetReportQueryRun(t.Context(), "query", principal); apperror.CodeOf(err) != "agent_dialog.report_query_run_not_found" {
		t.Fatalf("invalid report state error=%v", err)
	}
}

func TestAgentWorkspaceScopeEdges(t *testing.T) {
	t.Parallel()

	unknown := principalmodel.Principal{}
	knownWithoutWorkspace := principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}
	service := NewAgentApplicationService(&agentRepositoryStub{})
	for _, principal := range []principalmodel.Principal{unknown, knownWithoutWorkspace} {
		if _, err := agentQueryWorkspace(principal); apperror.CodeOf(err) != "backend.workspace_scope_required" {
			t.Errorf("query principal=%#v err=%v", principal, err)
		}
		if _, err := agentCommandWorkspace(principal); apperror.CodeOf(err) != "backend.workspace_scope_required" {
			t.Errorf("command principal=%#v err=%v", principal, err)
		}
		if _, err := service.ListProposals(t.Context(), "", principal); apperror.CodeOf(err) != "backend.workspace_scope_required" {
			t.Errorf("list proposals principal=%#v err=%v", principal, err)
		}
		if _, err := service.SetSessionArchived(t.Context(), "session", true, principal); apperror.CodeOf(err) != "backend.workspace_scope_required" {
			t.Errorf("archive session principal=%#v err=%v", principal, err)
		}
	}
	principal := agentTestPrincipal()
	if workspace, err := agentQueryWorkspace(principal); err != nil || workspace != principal.WorkspaceID {
		t.Fatalf("query workspace=%q err=%v", workspace, err)
	}
	if workspace, err := agentCommandWorkspace(principal); err != nil || workspace != principal.WorkspaceID {
		t.Fatalf("command workspace=%q err=%v", workspace, err)
	}
}

func agentTestPrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}, accessfixture.Bundle{Key: "admin"})
}

func TestAgentStateStubIsolation(t *testing.T) {
	t.Parallel()
	repository := &agentRepositoryStub{listValues: []agentmodel.AgentStateRecord{{Key: "a"}}}
	values, _ := repository.List(t.Context(), "", "", "", "")
	values[0].Key = "changed"
	if reflect.DeepEqual(values, repository.listValues) {
		t.Fatal("stub list must return an isolated slice")
	}
}
