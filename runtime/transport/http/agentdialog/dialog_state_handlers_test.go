package agentdialog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type agentDialogStateRepository struct {
	mu      sync.Mutex
	values  map[string]agentmodel.AgentStateRecord
	listErr error
	getErr  error
	putErr  error
}

func newAgentDialogStateRepository() *agentDialogStateRepository {
	return &agentDialogStateRepository{values: map[string]agentmodel.AgentStateRecord{}}
}

func (repository *agentDialogStateRepository) List(_ context.Context, workspaceID, kind, userID, roleKey string) ([]agentmodel.AgentStateRecord, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	result := []agentmodel.AgentStateRecord{}
	for _, value := range repository.values {
		if value.WorkspaceID == workspaceID && value.Kind == kind && value.UserID == userID && value.RoleKey == roleKey {
			result = append(result, value)
		}
	}
	return result, nil
}

func (repository *agentDialogStateRepository) Get(_ context.Context, workspaceID, kind, key string) (agentmodel.AgentStateRecord, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.getErr != nil {
		return agentmodel.AgentStateRecord{}, false, repository.getErr
	}
	value, found := repository.values[workspaceID+"\x00"+kind+"\x00"+key]
	return value, found, nil
}

func (repository *agentDialogStateRepository) Put(_ context.Context, workspaceID string, value agentmodel.AgentStateRecord) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.putErr != nil {
		return repository.putErr
	}
	repository.values[workspaceID+"\x00"+value.Kind+"\x00"+value.Key] = value
	return nil
}

func (repository *agentDialogStateRepository) PutBatch(ctx context.Context, workspaceID string, values []agentmodel.AgentStateRecord) error {
	for _, value := range values {
		if err := repository.Put(ctx, workspaceID, value); err != nil {
			return err
		}
	}
	return nil
}

func (repository *agentDialogStateRepository) CompareAndSwap(_ context.Context, workspaceID string, value agentmodel.AgentStateRecord, expectedUpdatedAt int64) (bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.putErr != nil {
		return false, repository.putErr
	}
	key := workspaceID + "\x00" + value.Kind + "\x00" + value.Key
	current, found := repository.values[key]
	if !found || current.UpdatedAt != expectedUpdatedAt {
		return false, nil
	}
	repository.values[key] = value
	return true, nil
}

type agentDialogStateHTTPResult struct {
	serviceErrors int
	errorCode     string
}

func agentDialogStatePrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "user-1"}}, accessfixture.Bundle{Key: "operator", Permissions: []string{"identity.audit.view"}})
}

func agentDialogStateHandler(repository *agentDialogStateRepository, principal principalmodel.Principal) (*AgentDialogHandler, *agentDialogStateHTTPResult) {
	state := agentapplication.NewAgentApplicationService(repository)
	result := &agentDialogStateHTTPResult{}
	return NewAgentDialogHandler(AgentDialogDependencies{
		Sessions: state, ProposalState: state,
		Principal: func(*http.Request) principalmodel.Principal { return principal },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteError: func(w http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			result.errorCode = code
			w.WriteHeader(status)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, _ error) {
			result.serviceErrors++
			w.WriteHeader(http.StatusUnprocessableEntity)
		},
		DecodeJSON: func(w http.ResponseWriter, request *http.Request, target any) bool {
			if err := json.NewDecoder(request.Body).Decode(target); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return false
			}
			return true
		},
	}), result
}

func TestAgentDialogSessionLimitBoundaries(t *testing.T) {
	for raw, want := range map[string]int{"": 20, "invalid": 20, "0": 20, "-1": 20, " 7 ": 7, "101": 100, "100": 100} {
		if got := agentDialogSessionLimit(raw); got != want {
			t.Errorf("limit(%q)=%d want=%d", raw, got, want)
		}
	}
}

func TestAgentDialogSessionHandlersLifecycleAndFilters(t *testing.T) {
	repository := newAgentDialogStateRepository()
	handler, _ := agentDialogStateHandler(repository, agentDialogStatePrincipal())

	bad := httptest.NewRecorder()
	handler.agentDialogUpsertSession(bad, httptest.NewRequest(http.MethodPut, "/agent-dialog/sessions", strings.NewReader("{")))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status=%d", bad.Code)
	}

	upsert := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/agent-dialog/sessions", strings.NewReader(`{"external_session_id":"session-1","title":"Customer review","context":{"surface":"customers","object_key":"customer","record_id":"customer-1"}}`))
	handler.agentDialogUpsertSession(upsert, request)
	if upsert.Code != http.StatusOK {
		t.Fatalf("upsert status=%d body=%s", upsert.Code, upsert.Body.String())
	}

	list := httptest.NewRecorder()
	handler.agentDialogListSessions(list, httptest.NewRequest(http.MethodGet, "/agent-dialog/sessions?q=review&surface=customers&object_key=customer&record_id=customer-1&limit=1", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "session-1") {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}

	archiveRequest := httptest.NewRequest(http.MethodPost, "/agent-dialog/sessions/session-1/archive", nil)
	archiveRequest.SetPathValue("externalSessionID", "session-1")
	archive := httptest.NewRecorder()
	handler.agentDialogArchiveSession(archive, archiveRequest)
	if archive.Code != http.StatusOK || !strings.Contains(archive.Body.String(), `"archived":true`) {
		t.Fatalf("archive status=%d body=%s", archive.Code, archive.Body.String())
	}

	active := httptest.NewRecorder()
	handler.agentDialogListSessions(active, httptest.NewRequest(http.MethodGet, "/agent-dialog/sessions", nil))
	if active.Code != http.StatusOK || strings.Contains(active.Body.String(), "session-1") {
		t.Fatalf("active list status=%d body=%s", active.Code, active.Body.String())
	}
	archived := httptest.NewRecorder()
	handler.agentDialogListSessions(archived, httptest.NewRequest(http.MethodGet, "/agent-dialog/sessions?archived=true", nil))
	if archived.Code != http.StatusOK || !strings.Contains(archived.Body.String(), "session-1") {
		t.Fatalf("archived list status=%d body=%s", archived.Code, archived.Body.String())
	}

	restoreRequest := httptest.NewRequest(http.MethodPost, "/agent-dialog/sessions/session-1/restore", nil)
	restoreRequest.SetPathValue("externalSessionID", "session-1")
	restore := httptest.NewRecorder()
	handler.agentDialogRestoreSession(restore, restoreRequest)
	if restore.Code != http.StatusOK || strings.Contains(restore.Body.String(), `"archived":true`) {
		t.Fatalf("restore status=%d body=%s", restore.Code, restore.Body.String())
	}
}

func TestAgentDialogSessionHandlersPropagateRepositoryAndScopeErrors(t *testing.T) {
	want := errors.New("state unavailable")
	repository := newAgentDialogStateRepository()
	repository.listErr = want
	handler, result := agentDialogStateHandler(repository, agentDialogStatePrincipal())
	response := httptest.NewRecorder()
	handler.agentDialogListSessions(response, httptest.NewRequest(http.MethodGet, "/agent-dialog/sessions", nil))
	if response.Code != http.StatusInternalServerError || result.errorCode != "agent_dialog.state_repository_failed" {
		t.Fatalf("list failure status=%d code=%q", response.Code, result.errorCode)
	}

	repository.listErr = nil
	repository.putErr = want
	response = httptest.NewRecorder()
	handler.agentDialogUpsertSession(response, httptest.NewRequest(http.MethodPut, "/agent-dialog/sessions", strings.NewReader(`{"external_session_id":"session-1"}`)))
	if response.Code != http.StatusUnprocessableEntity || result.serviceErrors != 1 {
		t.Fatalf("upsert failure status=%d serviceErrors=%d", response.Code, result.serviceErrors)
	}

	unknown, unknownResult := agentDialogStateHandler(newAgentDialogStateRepository(), principalmodel.Principal{})
	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/agent-dialog/sessions/missing/archive", nil)
	request.SetPathValue("externalSessionID", "missing")
	unknown.agentDialogArchiveSession(response, request)
	if response.Code != http.StatusUnprocessableEntity || unknownResult.serviceErrors != 1 {
		t.Fatalf("scope failure status=%d serviceErrors=%d", response.Code, unknownResult.serviceErrors)
	}
}

func TestAgentDialogProposalStateHandlers(t *testing.T) {
	repository := newAgentDialogStateRepository()
	principal := agentDialogStatePrincipal()
	handler, result := agentDialogStateHandler(repository, principal)
	stored, err := handler.agentDialogStoreProposal(t.Context(), agentDialogProposalRecord{ProposalID: "proposal-1", Status: "draft", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey, Title: "Review customer"})
	if err != nil || stored.ProposalID != "proposal-1" {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}

	list := httptest.NewRecorder()
	handler.agentDialogListProposals(list, httptest.NewRequest(http.MethodGet, "/agent-dialog/proposals?status=draft", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "proposal-1") {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	getRequest := httptest.NewRequest(http.MethodGet, "/agent-dialog/proposals/proposal-1", nil)
	getRequest.SetPathValue("proposalID", "proposal-1")
	get := httptest.NewRecorder()
	handler.agentDialogGetProposal(get, getRequest)
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "Review customer") {
		t.Fatalf("get status=%d body=%s", get.Code, get.Body.String())
	}

	missingRequest := httptest.NewRequest(http.MethodGet, "/agent-dialog/proposals/missing", nil)
	missingRequest.SetPathValue("proposalID", "missing")
	missing := httptest.NewRecorder()
	handler.agentDialogGetProposal(missing, missingRequest)
	if missing.Code != http.StatusUnprocessableEntity || result.serviceErrors != 1 {
		t.Fatalf("missing status=%d serviceErrors=%d", missing.Code, result.serviceErrors)
	}

	repository.listErr = errors.New("list failed")
	failure := httptest.NewRecorder()
	handler.agentDialogListProposals(failure, httptest.NewRequest(http.MethodGet, "/agent-dialog/proposals", nil))
	if failure.Code != http.StatusInternalServerError || result.errorCode != "agent_dialog.state_repository_failed" {
		t.Fatalf("list failure status=%d code=%q", failure.Code, result.errorCode)
	}

	repository.listErr = nil
	repository.putErr = errors.New("put failed")
	if _, err := handler.agentDialogStoreProposal(t.Context(), agentDialogProposalRecord{ProposalID: "proposal-2", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey}); err == nil {
		t.Fatal("store repository failure was ignored")
	}
}

func TestAgentDialogApplicationPortWiringIgnoresNilAndAcceptsOwners(t *testing.T) {
	repository := newAgentDialogStateRepository()
	first := agentapplication.NewAgentApplicationService(repository)
	second := agentapplication.NewAgentApplicationService(repository)
	decisions := agentapplication.NewAgentProposalApplicationService(first, agentapplication.AgentProposalDependencies{})
	records := &recordapplication.RecordApplicationService{}
	handler := NewAgentDialogHandler(AgentDialogDependencies{Sessions: first, ProposalState: first, ReportGovernance: first, ProposalDecisions: decisions, AnalysisRecords: records})

	handler.UseStateApplications(nil, nil, nil)
	handler.UseProposalDecisions(nil)
	handler.UseAnalysisRecords(nil)
	if handler.sessions != first || handler.proposalState != first || handler.reportGovernance != first || handler.proposalDecisions != decisions || handler.analysisRecords != records {
		t.Fatal("nil wiring replaced an existing owner")
	}

	secondDecisions := agentapplication.NewAgentProposalApplicationService(second, agentapplication.AgentProposalDependencies{})
	secondRecords := &recordapplication.RecordApplicationService{}
	handler.UseStateApplications(second, second, second)
	handler.UseProposalDecisions(secondDecisions)
	handler.UseAnalysisRecords(secondRecords)
	if handler.sessions != second || handler.proposalState != second || handler.reportGovernance != second || handler.proposalDecisions != secondDecisions || handler.analysisRecords != secondRecords {
		t.Fatal("non-nil owner wiring was not applied")
	}
}

func agentDialogDecisionHandler(repository *agentDialogStateRepository) (*AgentDialogHandler, *agentDialogStateHTTPResult, *int) {
	principal := agentDialogStatePrincipal()
	handler, result := agentDialogStateHandler(repository, principal)
	state := handler.proposalState
	handler.proposalDecisions = agentapplication.NewAgentProposalApplicationService(state, agentapplication.AgentProposalDependencies{
		GuardedWrites: func(context.Context, principalmodel.Principal) []agentapplication.AgentGuardedWriteContract {
			return []agentapplication.AgentGuardedWriteContract{{ObjectKey: "customer", Operation: "create", ActionKey: "customer.create", Endpoint: "/objects/customer/actions/create"}}
		},
		ResolvePrincipalRole: func(_ context.Context, userID, roleKey string) (principalmodel.Principal, error) {
			refreshed := principal
			if userID != principal.UserID || roleKey != principal.RoleKey {
				return principalmodel.Principal{}, errors.New("identity mismatch")
			}
			refreshed.AuthorizationRevision = "auth-rev-2"
			return refreshed, nil
		},
		InvokeAction: func(context.Context, agentapplication.AgentActionInvocation) (agentapplication.AgentActionInvocationResult, error) {
			return agentapplication.AgentActionInvocationResult{Object: map[string]any{"id": "customer-1"}}, nil
		},
	})
	audits := 0
	handler.securityAudit = func(*http.Request, string, string, map[string]any) { audits++ }
	handler.securityAuditForPrincipal = func(*http.Request, principalmodel.Principal, string, string, map[string]any) { audits++ }
	return handler, result, &audits
}

func TestAgentDialogCreateAndApproveProposalLifecycle(t *testing.T) {
	repository := newAgentDialogStateRepository()
	handler, _, audits := agentDialogDecisionHandler(repository)

	bad := httptest.NewRecorder()
	handler.agentDialogCreateProposal(bad, httptest.NewRequest(http.MethodPost, "/agent-dialog/proposals", strings.NewReader("{")))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status=%d", bad.Code)
	}

	body := `{"title":" Create customer ","summary":" Review first ","source":" analysis ","reference":" query-1 ","metadata":{"agent_mode":"domain-flow","run_mode":"suggested_write","risk_level":"medium"},"proposed":{"tool_binding":{"tool_name":"createRecord","object_key":"customer","data":{"name":"Alice"}}}}`
	createdResponse := httptest.NewRecorder()
	handler.agentDialogCreateProposal(createdResponse, httptest.NewRequest(http.MethodPost, "/agent-dialog/proposals", strings.NewReader(body)))
	if createdResponse.Code != http.StatusCreated || *audits != 1 {
		t.Fatalf("create status=%d audits=%d body=%s", createdResponse.Code, *audits, createdResponse.Body.String())
	}
	var created agentapplication.AgentProposal
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	binding, ok := created.Proposed["action_binding"].(map[string]any)
	if !ok || binding["action_key"] != "customer.create" || binding["guarded_write"] != true || created.Metadata["guarded_write_rewrite"] != true || created.Actor != "user-1" {
		t.Fatalf("created proposal=%#v", created)
	}

	approveBody := `{"reason":" looks good ","metadata":{"run_mode":"suggested_write","risk_level":"high"}}`
	approveRequest := httptest.NewRequest(http.MethodPost, "/agent-dialog/proposals/"+created.ProposalID+"/approve", strings.NewReader(approveBody))
	approveRequest.SetPathValue("proposalID", created.ProposalID)
	approvedResponse := httptest.NewRecorder()
	handler.agentDialogApproveProposal(approvedResponse, approveRequest)
	if approvedResponse.Code != http.StatusOK || *audits != 2 {
		t.Fatalf("approve status=%d audits=%d body=%s", approvedResponse.Code, *audits, approvedResponse.Body.String())
	}
	var approved agentapplication.AgentProposal
	if err := json.Unmarshal(approvedResponse.Body.Bytes(), &approved); err != nil {
		t.Fatal(err)
	}
	if approved.Status != "approved" || approved.DecisionReason != "looks good" || approved.Execution["status"] != "applied" || approved.Execution["kind"] != "object_action" {
		t.Fatalf("approved proposal=%#v", approved)
	}
}

func TestAgentDialogRejectProposalAndPolicyDenials(t *testing.T) {
	repository := newAgentDialogStateRepository()
	handler, result, audits := agentDialogDecisionHandler(repository)
	principal := agentDialogStatePrincipal()
	stored, err := handler.agentDialogStoreProposal(t.Context(), agentDialogProposalRecord{ProposalID: "proposal-reject", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey})
	if err != nil {
		t.Fatal(err)
	}

	missingID := httptest.NewRecorder()
	handler.agentDialogRejectProposal(missingID, httptest.NewRequest(http.MethodPost, "/agent-dialog/proposals//reject", nil))
	if missingID.Code != http.StatusBadRequest || result.errorCode != "agent_dialog.proposal_id_required" {
		t.Fatalf("missing ID status=%d code=%q", missingID.Code, result.errorCode)
	}
	nilBodyRequest := httptest.NewRequest(http.MethodPost, "/agent-dialog/proposals/"+stored.ProposalID+"/reject", nil)
	nilBodyRequest.Body = nil
	nilBodyRequest.SetPathValue("proposalID", stored.ProposalID)
	nilBodyResponse := httptest.NewRecorder()
	handler.agentDialogRejectProposal(nilBodyResponse, nilBodyRequest)
	if nilBodyResponse.Code != http.StatusForbidden {
		t.Fatalf("nil body decision status=%d", nilBodyResponse.Code)
	}

	invalidRequest := httptest.NewRequest(http.MethodPost, "/agent-dialog/proposals/"+stored.ProposalID+"/reject", strings.NewReader("{"))
	invalidRequest.SetPathValue("proposalID", stored.ProposalID)
	invalid := httptest.NewRecorder()
	handler.agentDialogRejectProposal(invalid, invalidRequest)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid decision JSON status=%d", invalid.Code)
	}

	deniedRequest := httptest.NewRequest(http.MethodPost, "/agent-dialog/proposals/"+stored.ProposalID+"/approve", strings.NewReader(`{"metadata":{"run_mode":"suggested_write","risk_level":"critical"}}`))
	deniedRequest.SetPathValue("proposalID", stored.ProposalID)
	denied := httptest.NewRecorder()
	handler.agentDialogApproveProposal(denied, deniedRequest)
	if denied.Code != http.StatusForbidden || result.errorCode != "agent_dialog.policy_risk_denied" || *audits != 2 {
		t.Fatalf("policy denial status=%d code=%q audits=%d", denied.Code, result.errorCode, *audits)
	}

	rejectRequest := httptest.NewRequest(http.MethodPost, "/agent-dialog/proposals/"+stored.ProposalID+"/reject", strings.NewReader(`{"reason":"not now","metadata":{"run_mode":"suggested_write"}}`))
	rejectRequest.SetPathValue("proposalID", stored.ProposalID)
	rejectedResponse := httptest.NewRecorder()
	handler.agentDialogRejectProposal(rejectedResponse, rejectRequest)
	if rejectedResponse.Code != http.StatusOK || *audits != 3 || !strings.Contains(rejectedResponse.Body.String(), `"status":"rejected"`) {
		t.Fatalf("reject status=%d audits=%d body=%s", rejectedResponse.Code, *audits, rejectedResponse.Body.String())
	}
}

func TestAgentDialogProposalCommandsPropagateStateFailures(t *testing.T) {
	repository := newAgentDialogStateRepository()
	handler, result, _ := agentDialogDecisionHandler(repository)
	defaultDenied := httptest.NewRecorder()
	handler.agentDialogCreateProposal(defaultDenied, httptest.NewRequest(http.MethodPost, "/agent-dialog/proposals", strings.NewReader(`{}`)))
	if defaultDenied.Code != http.StatusForbidden || result.errorCode != "agent_dialog.policy_read_only" {
		t.Fatalf("default create policy status=%d code=%q", defaultDenied.Code, result.errorCode)
	}
	emptyDecision := httptest.NewRequest(http.MethodPost, "/agent-dialog/proposals/missing/reject", nil)
	emptyDecision.SetPathValue("proposalID", "missing")
	emptyDenied := httptest.NewRecorder()
	handler.agentDialogRejectProposal(emptyDenied, emptyDecision)
	if emptyDenied.Code != http.StatusForbidden || result.errorCode != "agent_dialog.policy_read_only" {
		t.Fatalf("default decision policy status=%d code=%q", emptyDenied.Code, result.errorCode)
	}

	repository.putErr = errors.New("put failed")
	create := httptest.NewRecorder()
	handler.agentDialogCreateProposal(create, httptest.NewRequest(http.MethodPost, "/agent-dialog/proposals", strings.NewReader(`{"metadata":{"run_mode":"suggested_write"}}`)))
	if create.Code != http.StatusInternalServerError || result.errorCode != "agent_dialog.state_repository_failed" {
		t.Fatalf("create failure status=%d code=%q", create.Code, result.errorCode)
	}

	repository.putErr = nil
	request := httptest.NewRequest(http.MethodPost, "/agent-dialog/proposals/missing/approve", strings.NewReader(`{"metadata":{"run_mode":"suggested_write"}}`))
	request.SetPathValue("proposalID", "missing")
	decision := httptest.NewRecorder()
	handler.agentDialogApproveProposal(decision, request)
	if decision.Code != http.StatusUnprocessableEntity || result.serviceErrors != 1 {
		t.Fatalf("decision failure status=%d serviceErrors=%d", decision.Code, result.serviceErrors)
	}
}

func TestAgentDialogCreateProposalLeavesUnguardedBindingUnmarked(t *testing.T) {
	repository := newAgentDialogStateRepository()
	handler, _, _ := agentDialogDecisionHandler(repository)
	response := httptest.NewRecorder()
	handler.agentDialogCreateProposal(response, httptest.NewRequest(http.MethodPost, "/agent-dialog/proposals", strings.NewReader(`{"metadata":{"run_mode":"suggested_write"},"proposed":{"action_binding":{"action_key":"customer.read","guarded_write":false}}}`)))
	if response.Code != http.StatusCreated || strings.Contains(response.Body.String(), "guarded_write_rewrite") {
		t.Fatalf("unguarded proposal status=%d body=%s", response.Code, response.Body.String())
	}
}
