package changeplans

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/idempotency"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	changeplanapplication "github.com/domainry/domainry-runtime/runtime/application/changeplan"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	changeplancontract "github.com/domainry/domainry-runtime/runtime/domain/changeplan/contract"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type changePlansRepositoryStub struct {
	draft         changeplanmodel.BusinessChangePlanDraft
	getWorkspace  string
	saveWorkspace string
	getErr        error
	saveErr       error
	saveOK        bool
	replayResult  json.RawMessage
	claimErr      error
}

func (r *changePlansRepositoryStub) GetDraft(_ context.Context, workspaceID, _ string) (changeplanmodel.BusinessChangePlanDraft, bool, error) {
	r.getWorkspace = workspaceID
	return r.draft, r.draft.PlanID != "", r.getErr
}

func (r *changePlansRepositoryStub) SaveDraft(_ context.Context, workspaceID string, draft changeplanmodel.BusinessChangePlanDraft, _ int) (changeplanmodel.BusinessChangePlanDraft, bool, error) {
	r.saveWorkspace = workspaceID
	if r.saveErr != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, false, r.saveErr
	}
	r.draft = draft
	return draft, r.saveOK, nil
}

func (r *changePlansRepositoryStub) TransitionDraft(_ context.Context, workspaceID, _ string, _ int, _, toStatus, by, _ string) (changeplanmodel.BusinessChangePlanDraft, bool, error) {
	r.draft.WorkspaceID = workspaceID
	r.draft.Status = toStatus
	r.draft.Revision++
	r.draft.UpdatedBy = by
	return r.draft, true, nil
}

func (*changePlansRepositoryStub) PublishDraft(context.Context, string, string, int, string, string) (changeplanmodel.BusinessChangePlanDraft, bool, error) {
	return changeplanmodel.BusinessChangePlanDraft{}, false, nil
}

func (r *changePlansRepositoryStub) TryBeginOperation(_ context.Context, workspaceID string, request changeplanmodel.ChangePlanOperationClaimRequest) (changeplanmodel.ChangePlanOperationClaimResult, error) {
	if r.claimErr != nil {
		return changeplanmodel.ChangePlanOperationClaimResult{}, r.claimErr
	}
	execution := request.Execution
	execution.ID = "execution-1"
	execution.WorkspaceID = workspaceID
	execution.RequestFingerprint = request.RequestFingerprint
	execution.LeaseOwner = request.LeaseOwner
	execution.FencingToken = 1
	execution.Result = append([]byte(nil), r.replayResult...)
	return changeplanmodel.ChangePlanOperationClaimResult{Decision: idempotency.DecisionReplay, Execution: execution}, nil
}

func (*changePlansRepositoryStub) CompleteOperation(context.Context, string, changeplanmodel.ChangePlanOperationCompletion) (changeplanmodel.ChangePlanOperationExecution, error) {
	return changeplanmodel.ChangePlanOperationExecution{}, nil
}

func (*changePlansRepositoryStub) FailOperation(context.Context, string, changeplanmodel.ChangePlanOperationFailure) (changeplanmodel.ChangePlanOperationExecution, error) {
	return changeplanmodel.ChangePlanOperationExecution{}, nil
}

type changePlansAuditStub struct{ events []auditmodel.AuditEvent }

type changePlansRuntimeStub struct{}

func (changePlansRuntimeStub) ListDefinitions(context.Context, principalmodel.SystemScope, string) ([]metadatamodel.MetadataDefinition, error) {
	return nil, nil
}
func (changePlansRuntimeStub) ApplyDefinitionMutations(context.Context, principalmodel.SystemScope, []metadatamodel.MetadataDefinitionMutation, []auditmodel.AuditEvent, *changeplanmodel.BusinessChangePlanPublication) ([]metadatamodel.MetadataDefinition, error) {
	return nil, nil
}

func (changePlansRuntimeStub) CanonicalizeMetadataCandidate(_ context.Context, mutations []metadatamodel.MetadataDefinitionMutation) ([]metadatamodel.MetadataDefinitionMutation, error) {
	return mutations, nil
}
func (changePlansRuntimeStub) ReloadMetadata(context.Context, principalmodel.Principal) (string, error) {
	return "schema", nil
}

type changePlansScenarioRuntimeStub struct{}

func (changePlansScenarioRuntimeStub) SimulateActionCandidate(context.Context, string, metadatamodel.MetadataDefinitionUpsertRequest, map[string]any, map[string]any, principalmodel.Principal) (changeplanapplication.AcceptanceScenarioRuntimeResult, error) {
	return changeplanapplication.AcceptanceScenarioRuntimeResult{Valid: true, SideEffectFree: true, Payload: []byte(`{"valid":true}`)}, nil
}

func (s *changePlansAuditStub) InsertAuditEvent(_ context.Context, _ string, event auditmodel.AuditEvent) error {
	s.events = append(s.events, event)
	return nil
}

type changePlansCapture struct {
	serviceErr error
	errorCode  string
}

type changePlansFixture struct {
	handler         *ChangePlansHandler
	repository      *changePlansRepositoryStub
	capture         *changePlansCapture
	principal       *principalmodel.Principal
	snapshotErr     error
	snapshotErrorAt int
	graphErr        error
	snapshotCalls   int
	graphCalls      int
	snapshotValue   changeplanmodel.Snapshot
}

func newChangePlansFixture() *changePlansFixture {
	repository := &changePlansRepositoryStub{saveOK: true}
	audit := &changePlansAuditStub{}
	runtime := changePlansRuntimeStub{}
	service := changeplanapplication.NewChangePlanApplicationService(repository, runtime, audit, runtime, changePlansScenarioRuntimeStub{})
	principal := accessfixture.AttachPointer(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin", "*"}})
	capture := &changePlansCapture{}
	fixture := &changePlansFixture{repository: repository, capture: capture, principal: principal}
	fixture.handler = NewChangePlansHandler(ChangePlansDependencies{
		Service:   service,
		Principal: func(*http.Request) principalmodel.Principal { return *principal },
		Snapshot: func(*http.Request) (changeplancontract.SnapshotSource, error) {
			fixture.snapshotCalls++
			if fixture.snapshotErr != nil && (fixture.snapshotErrorAt == 0 || fixture.snapshotCalls == fixture.snapshotErrorAt) {
				return nil, fixture.snapshotErr
			}
			if fixture.snapshotValue.SnapshotHash != "" {
				return fixture.snapshotValue, nil
			}
			return changeplanmodel.Snapshot{SnapshotHash: "snapshot-current"}, nil
		},
		Graph: func(*http.Request) (changeplancontract.ReferenceGraphSource, error) {
			fixture.graphCalls++
			if fixture.graphErr != nil {
				return nil, fixture.graphErr
			}
			return changeplanmodel.ReferenceGraph{Version: "v1", Hash: "graph-current"}, nil
		},
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteError: func(w http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			capture.errorCode = code
			w.WriteHeader(status)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			capture.serviceErr = err
			w.WriteHeader(http.StatusUnprocessableEntity)
		},
		DecodeJSON: func(w http.ResponseWriter, r *http.Request, value any) bool {
			if err := json.NewDecoder(r.Body).Decode(value); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return false
			}
			return true
		},
	})
	return fixture
}

func changePlansRequest(method, body, planID string) *http.Request {
	request := httptest.NewRequest(method, "/tenant-admin/change-plans/plan", strings.NewReader(body))
	request.SetPathValue("planID", planID)
	return request
}

func TestChangePlansDraftHandlersGetSaveIdentityAndRepositoryErrors(t *testing.T) {
	fixture := newChangePlansFixture()
	fixture.repository.draft = changeplanmodel.BusinessChangePlanDraft{PlanID: "plan-1", Revision: 2, Status: "draft"}
	get := httptest.NewRecorder()
	fixture.handler.getDraft(get, changePlansRequest(http.MethodGet, "", " plan-1 "))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"revision":2`) || fixture.repository.getWorkspace != "workspace-a" {
		t.Fatalf("get status=%d body=%s", get.Code, get.Body.String())
	}

	fixture.repository.draft = changeplanmodel.BusinessChangePlanDraft{}
	fixture.capture.serviceErr = nil
	missing := httptest.NewRecorder()
	fixture.handler.getDraft(missing, changePlansRequest(http.MethodGet, "", "missing"))
	if missing.Code != http.StatusUnprocessableEntity || fixture.capture.serviceErr == nil {
		t.Fatalf("missing status=%d error=%v", missing.Code, fixture.capture.serviceErr)
	}

	badJSON := httptest.NewRecorder()
	fixture.handler.saveDraft(badJSON, changePlansRequest(http.MethodPut, "{", "plan-1"))
	if badJSON.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON status=%d", badJSON.Code)
	}
	mismatch := httptest.NewRecorder()
	fixture.handler.saveDraft(mismatch, changePlansRequest(http.MethodPut, `{"plan":{"plan_id":"other"}}`, "plan-1"))
	if mismatch.Code != http.StatusBadRequest || fixture.capture.errorCode != "backend.change_plan.draft_identity_mismatch" {
		t.Fatalf("mismatch status=%d code=%q", mismatch.Code, fixture.capture.errorCode)
	}

	fixture.repository.draft = changeplanmodel.BusinessChangePlanDraft{}
	saved := httptest.NewRecorder()
	fixture.handler.saveDraft(saved, changePlansRequest(http.MethodPut, `{"expected_revision":0,"plan":{"business_reason":"test"}}`, " plan-1 "))
	if saved.Code != http.StatusOK || fixture.repository.draft.PlanID != "plan-1" || fixture.repository.draft.Revision != 1 || fixture.repository.draft.WorkspaceID != "workspace-a" || fixture.repository.saveWorkspace != "workspace-a" {
		t.Fatalf("save status=%d draft=%#v body=%s", saved.Code, fixture.repository.draft, saved.Body.String())
	}

	fixture.repository.getErr = errors.New("draft store failed")
	fixture.capture.serviceErr = nil
	failure := httptest.NewRecorder()
	fixture.handler.saveDraft(failure, changePlansRequest(http.MethodPut, `{"plan":{"plan_id":"plan-2"}}`, "plan-2"))
	if failure.Code != http.StatusUnprocessableEntity || fixture.capture.serviceErr == nil {
		t.Fatalf("save failure status=%d error=%v", failure.Code, fixture.capture.serviceErr)
	}
}

func TestChangePlansRollbackPolicyHandlerEnforcesAdministrator(t *testing.T) {
	fixture := newChangePlansFixture()
	success := httptest.NewRecorder()
	fixture.handler.rollbackPolicy(success, httptest.NewRequest(http.MethodGet, "/domain-maintenance/rollback-policy", nil))
	if success.Code != http.StatusOK || !strings.Contains(success.Body.String(), `"version":"domain-rollback-policy-v1"`) || !strings.Contains(success.Body.String(), `"manual_compensation"`) {
		t.Fatalf("success status=%d body=%s", success.Code, success.Body.String())
	}

	accessfixture.Set(fixture.principal, accessfixture.Bundle{})
	fixture.capture.serviceErr = nil
	forbidden := httptest.NewRecorder()
	fixture.handler.rollbackPolicy(forbidden, httptest.NewRequest(http.MethodGet, "/domain-maintenance/rollback-policy", nil))
	if forbidden.Code != http.StatusUnprocessableEntity || fixture.capture.serviceErr == nil {
		t.Fatalf("forbidden status=%d error=%v", forbidden.Code, fixture.capture.serviceErr)
	}
}

func TestChangePlansValidateCoversDecodeSourcesAuthorizationAndSuccess(t *testing.T) {
	fixture := newChangePlansFixture()
	badJSON := httptest.NewRecorder()
	fixture.handler.validate(badJSON, changePlansRequest(http.MethodPost, "{", ""))
	if badJSON.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON status=%d", badJSON.Code)
	}

	fixture.snapshotErr = errors.New("snapshot failed")
	snapshotFailure := httptest.NewRecorder()
	fixture.handler.validate(snapshotFailure, changePlansRequest(http.MethodPost, `{}`, ""))
	if snapshotFailure.Code != http.StatusUnprocessableEntity || !errors.Is(fixture.capture.serviceErr, fixture.snapshotErr) {
		t.Fatalf("snapshot failure status=%d error=%v", snapshotFailure.Code, fixture.capture.serviceErr)
	}
	fixture.snapshotErr = nil
	fixture.graphErr = errors.New("graph failed")
	graphFailure := httptest.NewRecorder()
	fixture.handler.validate(graphFailure, changePlansRequest(http.MethodPost, `{}`, ""))
	if graphFailure.Code != http.StatusUnprocessableEntity || !errors.Is(fixture.capture.serviceErr, fixture.graphErr) {
		t.Fatalf("graph failure status=%d error=%v", graphFailure.Code, fixture.capture.serviceErr)
	}

	fixture.graphErr = nil
	accessfixture.Set(fixture.principal, accessfixture.Bundle{})
	serviceFailure := httptest.NewRecorder()
	fixture.handler.validate(serviceFailure, changePlansRequest(http.MethodPost, `{}`, ""))
	if serviceFailure.Code != http.StatusUnprocessableEntity || fixture.capture.serviceErr == nil {
		t.Fatalf("service failure status=%d error=%v", serviceFailure.Code, fixture.capture.serviceErr)
	}

	accessfixture.Set(fixture.principal, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
	fixture.capture.serviceErr = nil
	success := httptest.NewRecorder()
	fixture.handler.validate(success, changePlansRequest(http.MethodPost, `{}`, ""))
	if success.Code != http.StatusOK || fixture.capture.serviceErr != nil || !strings.Contains(success.Body.String(), `"valid":false`) {
		t.Fatalf("success status=%d error=%v body=%s", success.Code, fixture.capture.serviceErr, success.Body.String())
	}
}

func TestChangePlansApplyCoversDecodeIdempotencySourcesAndReplay(t *testing.T) {
	fixture := newChangePlansFixture()
	badJSON := httptest.NewRecorder()
	fixture.handler.apply(badJSON, changePlansRequest(http.MethodPost, "{", ""))
	if badJSON.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON status=%d", badJSON.Code)
	}
	missingKey := httptest.NewRecorder()
	fixture.handler.apply(missingKey, changePlansRequest(http.MethodPost, `{}`, ""))
	if missingKey.Code != http.StatusBadRequest || fixture.capture.errorCode != idempotency.ErrorCodeMissingKey {
		t.Fatalf("missing key status=%d code=%q", missingKey.Code, fixture.capture.errorCode)
	}

	applyRequest := func(body string) *http.Request {
		request := changePlansRequest(http.MethodPost, body, "")
		request.Header.Set("Idempotency-Key", "apply-1")
		return request
	}
	fixture.snapshotErr = errors.New("snapshot failed")
	snapshotFailure := httptest.NewRecorder()
	fixture.handler.apply(snapshotFailure, applyRequest(`{}`))
	if snapshotFailure.Code != http.StatusUnprocessableEntity || !errors.Is(fixture.capture.serviceErr, fixture.snapshotErr) {
		t.Fatalf("snapshot failure status=%d error=%v", snapshotFailure.Code, fixture.capture.serviceErr)
	}
	fixture.snapshotErr = nil
	fixture.graphErr = errors.New("graph failed")
	graphFailure := httptest.NewRecorder()
	fixture.handler.apply(graphFailure, applyRequest(`{}`))
	if graphFailure.Code != http.StatusUnprocessableEntity || !errors.Is(fixture.capture.serviceErr, fixture.graphErr) {
		t.Fatalf("graph failure status=%d error=%v", graphFailure.Code, fixture.capture.serviceErr)
	}

	fixture.graphErr = nil
	fixture.capture.serviceErr = nil
	serviceFailure := httptest.NewRecorder()
	fixture.handler.apply(serviceFailure, applyRequest(`{}`))
	if serviceFailure.Code != http.StatusUnprocessableEntity || fixture.capture.serviceErr == nil {
		t.Fatalf("service failure status=%d error=%v", serviceFailure.Code, fixture.capture.serviceErr)
	}

	fixture.repository.replayResult = json.RawMessage(`{"result":{"plan_id":"plan-1","status":"applied","applied_definitions":[],"schema_hash":"hash","validation":{"valid":true,"apply_allowed":true}}}`)
	fixture.repository.draft = changeplanmodel.BusinessChangePlanDraft{WorkspaceID: "workspace-a", PlanID: "plan-1", Revision: 3, Status: "approved", UpdatedBy: "approver", Payload: json.RawMessage(`{"plan_id":"plan-1"}`)}
	fixture.snapshotCalls = 0
	replay := httptest.NewRecorder()
	fixture.handler.apply(replay, applyRequest(`{"plan_id":"plan-1","expected_revision":3,"confirmation":"plan-1"}`))
	if replay.Code != http.StatusOK || replay.Header().Get("Idempotency-Replayed") != "true" || fixture.snapshotCalls != 2 || !strings.Contains(replay.Body.String(), `"status":"applied"`) || !strings.Contains(replay.Body.String(), `"current_snapshot"`) {
		t.Fatalf("replay status=%d header=%q snapshots=%d body=%s", replay.Code, replay.Header().Get("Idempotency-Replayed"), fixture.snapshotCalls, replay.Body.String())
	}

	fixture.snapshotCalls = 0
	fixture.snapshotErr = errors.New("current snapshot failed")
	fixture.snapshotErrorAt = 2
	fixture.capture.serviceErr = nil
	currentFailure := httptest.NewRecorder()
	fixture.handler.apply(currentFailure, applyRequest(`{"plan_id":"plan-1","expected_revision":3,"confirmation":"plan-1"}`))
	if currentFailure.Code != http.StatusUnprocessableEntity || currentFailure.Header().Get("Idempotency-Replayed") != "true" || !errors.Is(fixture.capture.serviceErr, fixture.snapshotErr) || fixture.snapshotCalls != 2 {
		t.Fatalf("current failure status=%d header=%q error=%v snapshots=%d", currentFailure.Code, currentFailure.Header().Get("Idempotency-Replayed"), fixture.capture.serviceErr, fixture.snapshotCalls)
	}
	fixture.snapshotErr = nil
	fixture.snapshotErrorAt = 0

	fixture.repository.replayResult = json.RawMessage(`{"error_kind":"bad_request","error_code":"backend.change_plan.apply_not_allowed"}`)
	fixture.capture.serviceErr = nil
	failedReplay := httptest.NewRecorder()
	fixture.handler.apply(failedReplay, applyRequest(`{"plan_id":"plan-1","expected_revision":3,"confirmation":"plan-1"}`))
	if failedReplay.Code != http.StatusUnprocessableEntity || failedReplay.Header().Get("Idempotency-Replayed") != "true" || fixture.capture.serviceErr == nil {
		t.Fatalf("failed replay status=%d header=%q error=%v", failedReplay.Code, failedReplay.Header().Get("Idempotency-Replayed"), fixture.capture.serviceErr)
	}
}

func TestChangePlansExportPackageRequiresPinnedRevisionAndReturnsAttachment(t *testing.T) {
	fixture := newChangePlansFixture()
	plan := changeplanmodel.BusinessSystemChangePlan{PlanID: "plan-1", RuntimeVersion: "runtime-v1", AuthoringContractVersion: "contract-v1", AuthoringContractHash: "hash", Items: []changeplanmodel.BusinessSystemChangeItem{{Operation: "create", ResourceType: "object", ResourceKey: "order", After: json.RawMessage(`{"key":"order"}`)}}}
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	fixture.repository.draft = changeplanmodel.BusinessChangePlanDraft{WorkspaceID: "workspace-a", PlanID: plan.PlanID, Revision: 3, Status: "approved", Payload: payload}

	missing := httptest.NewRecorder()
	request := changePlansRequest(http.MethodGet, "", plan.PlanID)
	fixture.handler.exportPackage(missing, request)
	if missing.Code != http.StatusBadRequest || fixture.capture.errorCode != "backend.change_plan.draft_revision_required" {
		t.Fatalf("missing revision status=%d code=%q", missing.Code, fixture.capture.errorCode)
	}
	zero := httptest.NewRecorder()
	request = changePlansRequest(http.MethodGet, "", plan.PlanID)
	request.URL.RawQuery = "revision=0"
	fixture.handler.exportPackage(zero, request)
	if zero.Code != http.StatusBadRequest || fixture.capture.errorCode != "backend.change_plan.draft_revision_required" {
		t.Fatalf("zero revision status=%d code=%q", zero.Code, fixture.capture.errorCode)
	}

	success := httptest.NewRecorder()
	request = changePlansRequest(http.MethodGet, "", plan.PlanID)
	request.URL.RawQuery = "revision=3"
	fixture.handler.exportPackage(success, request)
	if success.Code != http.StatusOK || !strings.Contains(success.Header().Get("Content-Disposition"), "plan-1.system-package.json") || !strings.Contains(success.Body.String(), `"package_version":"domain-system-package-v1"`) {
		t.Fatalf("export status=%d disposition=%q body=%s", success.Code, success.Header().Get("Content-Disposition"), success.Body.String())
	}
}

func TestChangePlansImportPackageCreatesSnapshotBoundDraft(t *testing.T) {
	fixture := newChangePlansFixture()
	plan := changeplanmodel.BusinessSystemChangePlan{PlanID: "source", Items: []changeplanmodel.BusinessSystemChangeItem{{Operation: "create", ResourceType: "object", ResourceKey: "order", After: json.RawMessage(`{"key":"order"}`)}}}
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	fixture.repository.draft = changeplanmodel.BusinessChangePlanDraft{WorkspaceID: "source", PlanID: plan.PlanID, Revision: 2, Status: "published", Payload: payload}
	exported := httptest.NewRecorder()
	exportRequest := changePlansRequest(http.MethodGet, "", plan.PlanID)
	exportRequest.URL.RawQuery = "revision=2"
	fixture.handler.exportPackage(exported, exportRequest)
	if exported.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", exported.Code, exported.Body.String())
	}
	var systemPackage changeplanmodel.BusinessSystemPackage
	if err := json.Unmarshal(exported.Body.Bytes(), &systemPackage); err != nil {
		t.Fatal(err)
	}
	fixture.repository.draft = changeplanmodel.BusinessChangePlanDraft{}
	requestBody, _ := json.Marshal(map[string]any{"expected_revision": 0, "business_reason": "Replay current package", "package": systemPackage})
	response := httptest.NewRecorder()
	fixture.handler.importPackage(response, changePlansRequest(http.MethodPut, string(requestBody), "replay"))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"plan_id":"replay"`) || !strings.Contains(response.Body.String(), `"status":"draft"`) {
		t.Fatalf("import status=%d body=%s error=%v", response.Code, response.Body.String(), fixture.capture.serviceErr)
	}

	badJSON := httptest.NewRecorder()
	fixture.handler.importPackage(badJSON, changePlansRequest(http.MethodPut, "{", "broken"))
	if badJSON.Code != http.StatusBadRequest {
		t.Fatalf("bad json status=%d", badJSON.Code)
	}
}

func TestChangePlansSimulateScenariosUsesExactDraftRevision(t *testing.T) {
	fixture := newChangePlansFixture()
	plan := changeplanmodel.BusinessSystemChangePlan{
		PlanID: "plan-1", DraftRevision: 2, ReleaseOrder: []string{"action"},
		Items:               []changeplanmodel.BusinessSystemChangeItem{{ItemID: "action", Operation: "create", ResourceType: "action", ResourceKey: "order.approve", After: json.RawMessage(`{"key":"order.approve"}`)}},
		AcceptanceScenarios: []changeplanmodel.BusinessAcceptanceScenario{{Key: "approve", Kind: changeplanmodel.BusinessAcceptanceScenarioKindActionDefinition, ResourceKey: "order.approve", Expected: changeplanmodel.BusinessAcceptanceScenarioExpectation{Valid: true}}},
	}
	payload, _ := json.Marshal(plan)
	fixture.repository.draft = changeplanmodel.BusinessChangePlanDraft{PlanID: plan.PlanID, Revision: 2, Status: "draft", Payload: payload}

	badJSON := httptest.NewRecorder()
	fixture.handler.simulateScenarios(badJSON, changePlansRequest(http.MethodPost, "{", plan.PlanID))
	if badJSON.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON status=%d", badJSON.Code)
	}

	stale := httptest.NewRecorder()
	fixture.handler.simulateScenarios(stale, changePlansRequest(http.MethodPost, `{"expected_revision":1}`, plan.PlanID))
	if stale.Code != http.StatusUnprocessableEntity || fixture.capture.serviceErr == nil {
		t.Fatalf("stale status=%d error=%v", stale.Code, fixture.capture.serviceErr)
	}

	success := httptest.NewRecorder()
	fixture.handler.simulateScenarios(success, changePlansRequest(http.MethodPost, `{"expected_revision":2}`, plan.PlanID))
	if success.Code != http.StatusOK || !strings.Contains(success.Body.String(), `"side_effect_free":true`) || !strings.Contains(success.Body.String(), `"passed":true`) {
		t.Fatalf("success status=%d body=%s", success.Code, success.Body.String())
	}
}

func TestChangePlansCloneCurrentCoversDecodeSourcesServiceAndSuccess(t *testing.T) {
	fixture := newChangePlansFixture()
	request := func(body string) *http.Request { return changePlansRequest(http.MethodPost, body, "clone") }

	badJSON := httptest.NewRecorder()
	fixture.handler.cloneCurrent(badJSON, request("{"))
	if badJSON.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON status=%d", badJSON.Code)
	}
	fixture.snapshotErr = errors.New("snapshot unavailable")
	response := httptest.NewRecorder()
	fixture.handler.cloneCurrent(response, request(`{}`))
	if response.Code != http.StatusUnprocessableEntity || !errors.Is(fixture.capture.serviceErr, fixture.snapshotErr) {
		t.Fatalf("snapshot status=%d err=%v", response.Code, fixture.capture.serviceErr)
	}
	fixture.snapshotErr = nil
	fixture.graphErr = errors.New("graph unavailable")
	response = httptest.NewRecorder()
	fixture.handler.cloneCurrent(response, request(`{}`))
	if response.Code != http.StatusUnprocessableEntity || !errors.Is(fixture.capture.serviceErr, fixture.graphErr) {
		t.Fatalf("graph status=%d err=%v", response.Code, fixture.capture.serviceErr)
	}
	fixture.graphErr = nil
	fixture.principal.Known = false
	response = httptest.NewRecorder()
	fixture.handler.cloneCurrent(response, request(`{}`))
	if response.Code != http.StatusUnprocessableEntity || fixture.capture.serviceErr == nil {
		t.Fatalf("service status=%d err=%v", response.Code, fixture.capture.serviceErr)
	}
	fixture.principal.Known = true
	fixture.capture.serviceErr = nil
	response = httptest.NewRecorder()
	fixture.handler.cloneCurrent(response, request(`{"business_reason":"Clone current"}`))
	if response.Code != http.StatusOK || fixture.capture.serviceErr != nil || fixture.repository.draft.PlanID != "clone" {
		t.Fatalf("success status=%d draft=%#v err=%v body=%s", response.Code, fixture.repository.draft, fixture.capture.serviceErr, response.Body.String())
	}
}

func TestChangePlansReviewTransitionsCoverDecodeSourcesBranchesAndErrors(t *testing.T) {
	fixture := newChangePlansFixture()
	call := func(approve bool, body string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := changePlansRequest(http.MethodPost, body, "missing")
		fixture.handler.transitionReview(response, request, approve)
		return response
	}
	if response := call(false, "{"); response.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON status=%d", response.Code)
	}
	fixture.snapshotErr = errors.New("snapshot unavailable")
	if response := call(false, `{}`); response.Code != http.StatusUnprocessableEntity || !errors.Is(fixture.capture.serviceErr, fixture.snapshotErr) {
		t.Fatalf("snapshot status=%d err=%v", response.Code, fixture.capture.serviceErr)
	}
	fixture.snapshotErr = nil
	fixture.graphErr = errors.New("graph unavailable")
	if response := call(true, `{}`); response.Code != http.StatusUnprocessableEntity || !errors.Is(fixture.capture.serviceErr, fixture.graphErr) {
		t.Fatalf("graph status=%d err=%v", response.Code, fixture.capture.serviceErr)
	}
	fixture.graphErr = nil
	for _, approve := range []bool{false, true} {
		fixture.capture.serviceErr = nil
		if response := call(approve, `{"expected_revision":1}`); response.Code != http.StatusUnprocessableEntity || fixture.capture.serviceErr == nil {
			t.Fatalf("approve=%v status=%d err=%v", approve, response.Code, fixture.capture.serviceErr)
		}
	}

	fixture = newChangePlansFixture()
	contract := capabilityapplication.RuntimeAuthoringCapabilities()
	capabilityKeys := []string{}
	for _, domain := range contract.Domains {
		for _, capability := range domain.Capabilities {
			capabilityKeys = append(capabilityKeys, capability.Key)
		}
	}
	fixture.snapshotValue = changeplanmodel.Snapshot{
		SnapshotHash: "snapshot-current", RuntimeVersion: capabilitycontract.RuntimeCapabilityContractVersion,
		AuthoringContractVersion: contract.ContractVersion, AuthoringContractHash: contract.ContractHash, CapabilityKeys: capabilityKeys,
	}
	plan := changeplanmodel.BusinessSystemChangePlan{
		PlanVersion: changeplanmodel.BusinessSystemChangePlanVersion, PlanID: "reviewable", BusinessReason: "Add asset category",
		SnapshotHash: fixture.snapshotValue.SnapshotHash, ReferenceGraphHash: "graph-current", RuntimeVersion: fixture.snapshotValue.RuntimeVersion,
		AuthoringContractVersion: fixture.snapshotValue.AuthoringContractVersion, AuthoringContractHash: fixture.snapshotValue.AuthoringContractHash,
		ReleaseOrder: []string{"add-category"}, RollbackOrder: []string{"add-category"}, Items: []changeplanmodel.BusinessSystemChangeItem{{
			ItemID: "add-category", Operation: "create", ChangeKind: "additive", RiskLevel: "low", ResourceType: "field", ResourceKey: "asset.category", ResourceOwner: "builder", CapabilityKey: "schema.field", After: json.RawMessage(`{"key":"category","type":"text"}`), ValidationMethods: []string{"metadata.validate"},
		}},
	}
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	fixture.repository.draft = changeplanmodel.BusinessChangePlanDraft{WorkspaceID: "workspace-a", PlanID: plan.PlanID, Revision: 1, Status: "draft", Payload: payload, UpdatedBy: "author"}
	fixture.principal.UserID = "author"
	review := httptest.NewRecorder()
	fixture.handler.review(review, changePlansRequest(http.MethodPost, `{"expected_revision":1}`, "reviewable"))
	if review.Code != http.StatusOK || !strings.Contains(review.Body.String(), `"status":"in_review"`) {
		t.Fatalf("review status=%d error=%v body=%s", review.Code, fixture.capture.serviceErr, review.Body.String())
	}
	fixture.principal.UserID = "independent-approver"
	approve := httptest.NewRecorder()
	fixture.handler.approve(approve, changePlansRequest(http.MethodPost, `{"expected_revision":2}`, "reviewable"))
	if approve.Code != http.StatusOK || !strings.Contains(approve.Body.String(), `"status":"approved"`) {
		t.Fatalf("approve status=%d error=%v body=%s", approve.Code, fixture.capture.serviceErr, approve.Body.String())
	}
}

func TestChangePlansImportAndExportMapSourceAndServiceFailures(t *testing.T) {
	fixture := newChangePlansFixture()
	fixture.repository.draft = changeplanmodel.BusinessChangePlanDraft{PlanID: "plan", Revision: 2, Status: "draft", Payload: json.RawMessage(`{}`)}
	exportRequest := changePlansRequest(http.MethodGet, "", "plan")
	exportRequest.URL.RawQuery = "revision=2"
	exportResponse := httptest.NewRecorder()
	fixture.handler.exportPackage(exportResponse, exportRequest)
	if exportResponse.Code != http.StatusUnprocessableEntity || fixture.capture.serviceErr == nil {
		t.Fatalf("export status=%d err=%v", exportResponse.Code, fixture.capture.serviceErr)
	}

	request := func() *http.Request { return changePlansRequest(http.MethodPut, `{}`, "import") }
	fixture.snapshotErr = errors.New("snapshot unavailable")
	response := httptest.NewRecorder()
	fixture.handler.importPackage(response, request())
	if response.Code != http.StatusUnprocessableEntity || !errors.Is(fixture.capture.serviceErr, fixture.snapshotErr) {
		t.Fatalf("snapshot status=%d err=%v", response.Code, fixture.capture.serviceErr)
	}
	fixture.snapshotErr = nil
	fixture.graphErr = errors.New("graph unavailable")
	response = httptest.NewRecorder()
	fixture.handler.importPackage(response, request())
	if response.Code != http.StatusUnprocessableEntity || !errors.Is(fixture.capture.serviceErr, fixture.graphErr) {
		t.Fatalf("graph status=%d err=%v", response.Code, fixture.capture.serviceErr)
	}
	fixture.graphErr = nil
	fixture.capture.serviceErr = nil
	response = httptest.NewRecorder()
	fixture.handler.importPackage(response, request())
	if response.Code != http.StatusUnprocessableEntity || fixture.capture.serviceErr == nil {
		t.Fatalf("service status=%d err=%v", response.Code, fixture.capture.serviceErr)
	}
}
