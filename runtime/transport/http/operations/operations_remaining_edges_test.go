package operations

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

type operationsServiceEdgeStub struct {
	OperationsService
	err          error
	executeErr   error
	searchFilter *operationsmodel.OperationsReceiptFilter
}

func (s operationsServiceEdgeStub) Definitions() []operationsmodel.OperationsDefinition { return nil }
func (s operationsServiceEdgeStub) Submit(context.Context, operationsapplication.OperationsSubmitRequest, string, principalmodel.Principal) (operationsmodel.OperationsReceipt, operationsmodel.OperationsSubmissionDecision, error) {
	return operationsmodel.OperationsReceipt{}, "", s.err
}
func (s operationsServiceEdgeStub) Receipt(context.Context, string, principalmodel.Principal) (operationsmodel.OperationsReceipt, error) {
	return operationsmodel.OperationsReceipt{}, s.err
}
func (s operationsServiceEdgeStub) Receipts(context.Context, operationsmodel.OperationsStatus, int, principalmodel.Principal) ([]operationsmodel.OperationsReceipt, error) {
	return nil, s.err
}
func (s operationsServiceEdgeStub) SearchReceipts(_ context.Context, filter operationsmodel.OperationsReceiptFilter, _ principalmodel.Principal) (operationsmodel.OperationsReceiptPage, error) {
	if s.searchFilter != nil {
		*s.searchFilter = filter
	}
	return operationsmodel.OperationsReceiptPage{Items: []operationsmodel.OperationsReceipt{}}, s.err
}
func (s operationsServiceEdgeStub) LegacyReceipts(context.Context, principalmodel.Principal, string, int) ([]idempotency.ReceiptSummary, error) {
	return nil, s.err
}
func (s operationsServiceEdgeStub) RetryLegacyReceipt(context.Context, principalmodel.Principal, string, string) error {
	return s.err
}
func (s operationsServiceEdgeStub) ResetLegacyReceipt(context.Context, principalmodel.Principal, string, string) error {
	return s.err
}
func (s operationsServiceEdgeStub) ExecuteOwnerOperation(ctx context.Context, _ operationsapplication.OperationsOwnerExecutionRequest, _ principalmodel.Principal, execute func(context.Context) (any, error)) (operationsapplication.OperationsOwnerExecutionResult, error) {
	if s.executeErr != nil {
		return operationsapplication.OperationsOwnerExecutionResult{}, s.executeErr
	}
	value, err := execute(ctx)
	return operationsapplication.OperationsOwnerExecutionResult{Value: value}, err
}
func (s operationsServiceEdgeStub) EnableBreakGlass(context.Context, operationsapplication.OperationsBreakGlassEnableCommand, string, principalmodel.Principal) (operationsapplication.OperationsBreakGlassResult, error) {
	return operationsapplication.OperationsBreakGlassResult{}, s.err
}
func (s operationsServiceEdgeStub) DisableBreakGlass(context.Context, string, operationsapplication.OperationsBreakGlassDisableCommand, string, principalmodel.Principal) (operationsapplication.OperationsBreakGlassResult, error) {
	return operationsapplication.OperationsBreakGlassResult{}, s.err
}
func (s operationsServiceEdgeStub) ListBreakGlass(context.Context, int, principalmodel.Principal) ([]operationsmodel.OperationsBreakGlassGrant, error) {
	return nil, s.err
}
func (s operationsServiceEdgeStub) CaptureDiagnostics(context.Context, operationsapplication.OperationsDiagnosticsCommand, string, principalmodel.Principal) (operationsapplication.OperationsDiagnosticsResult, error) {
	return operationsapplication.OperationsDiagnosticsResult{}, s.err
}
func (s operationsServiceEdgeStub) DryRunBulkDeadLetters(context.Context, operationsapplication.OperationsBulkDryRunRequest, string, principalmodel.Principal) (operationsapplication.OperationsBulkPlan, error) {
	return operationsapplication.OperationsBulkPlan{}, s.err
}
func (s operationsServiceEdgeStub) ApplyBulkDeadLetters(context.Context, operationsapplication.OperationsBulkApplyRequest, string, principalmodel.Principal) (operationsapplication.OperationsBulkApplyResult, error) {
	return operationsapplication.OperationsBulkApplyResult{}, s.err
}
func (s operationsServiceEdgeStub) InspectDeadLetter(context.Context, string, string, principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error) {
	return operationsapplication.OperationsDeadLetterItem{}, s.err
}
func (s operationsServiceEdgeStub) ActOnDeadLetter(context.Context, string, string, string, operationsapplication.OperationsDeadLetterActionRequest, string, principalmodel.Principal) (operationsapplication.OperationsDeadLetterActionResult, error) {
	return operationsapplication.OperationsDeadLetterActionResult{}, s.err
}

type operationsControlEdgeStub struct {
	OperationsControlService
	err error
}

func (s operationsControlEdgeStub) Set(context.Context, operationsapplication.OperationsControlRequest, string, principalmodel.Principal) (operationsapplication.OperationsControlResult, error) {
	return operationsapplication.OperationsControlResult{}, s.err
}
func (s operationsControlEdgeStub) List(context.Context, operationsmodel.OperationsControlKind, int, principalmodel.Principal) ([]operationsmodel.OperationsControl, error) {
	return nil, s.err
}

type operationsLeaseEdgeStub struct {
	OperationsLeaseService
	err error
}

func (s operationsLeaseEdgeStub) ForceRelease(context.Context, operationsapplication.OperationsLeaseReleaseCommand, string, principalmodel.Principal) (operationsapplication.OperationsLeaseReleaseReceipt, error) {
	return operationsapplication.OperationsLeaseReleaseReceipt{}, s.err
}

type databaseRetirementEdgeStub struct {
	DatabaseRetirementService
	err error
}

func (s databaseRetirementEdgeStub) Discover(context.Context, operationsmodel.DatabaseObjectIdentity, string, principalmodel.Principal) (operationsmodel.DatabaseRetirement, error) {
	return operationsmodel.DatabaseRetirement{}, s.err
}
func (s databaseRetirementEdgeStub) List(context.Context, operationsmodel.DatabaseRetirementState, int, principalmodel.Principal) ([]operationsmodel.DatabaseRetirement, error) {
	return nil, s.err
}
func (s databaseRetirementEdgeStub) OperationalStatus(context.Context, string, principalmodel.Principal) (operationsmodel.DatabaseRetirementOperationalStatus, error) {
	return operationsmodel.DatabaseRetirementOperationalStatus{}, s.err
}
func (s databaseRetirementEdgeStub) Preview(context.Context, string, principalmodel.Principal) (operationsmodel.DatabaseDropPlan, error) {
	return operationsmodel.DatabaseDropPlan{RetirementID: "retirement"}, s.err
}
func (s databaseRetirementEdgeStub) Advance(context.Context, string, operationsmodel.DatabaseRetirementState, operationsmodel.DatabaseRetirementEvidence, principalmodel.Principal) (operationsmodel.DatabaseRetirement, error) {
	return operationsmodel.DatabaseRetirement{}, s.err
}
func (s databaseRetirementEdgeStub) Execute(context.Context, string, principalmodel.Principal) (operationscontract.DatabaseRetirementExecutionResult, error) {
	return operationscontract.DatabaseRetirementExecutionResult{}, s.err
}

func operationsEdgeHandler(service OperationsService) (*OperationsHandler, *int) {
	errorsWritten := 0
	handler := NewOperationsHandler(OperationsDependencies{
		Service: service,
		Principal: func(*http.Request) principalmodel.Principal {
			return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace"}}
		},
		DecodeJSON: func(_ http.ResponseWriter, _ *http.Request, _ any) bool { return true },
		WriteJSON:  func(w http.ResponseWriter, status int, _ any) { w.WriteHeader(status) },
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, _ error) {
			errorsWritten++
			w.WriteHeader(http.StatusTeapot)
		},
	})
	return handler, &errorsWritten
}

func operationEdgeRequest(method, path string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader("{}"))
	request.Header.Set("Idempotency-Key", "key")
	request.SetPathValue("owner", "record")
	request.SetPathValue("receiptID", "receipt")
	request.SetPathValue("operationID", "operation")
	request.SetPathValue("grantID", "grant")
	request.SetPathValue("deadLetterID", "dead")
	request.SetPathValue("action", "retry")
	request.SetPathValue("controlKind", "maintenance")
	request.SetPathValue("resourceID", "resource")
	request.SetPathValue("retirementID", "retirement")
	return request
}

func TestOperationsCommandControlAndOwnerErrorEdges(t *testing.T) {
	failure := errors.New("service failed")
	handler, errorsWritten := operationsEdgeHandler(operationsServiceEdgeStub{err: failure})
	for _, call := range []func(http.ResponseWriter, *http.Request){handler.receipts, handler.getOperation, handler.listOperations, handler.disableBreakGlass, handler.applyBulkDeadLetters, handler.inspectDeadLetter} {
		response := httptest.NewRecorder()
		call(response, operationEdgeRequest(http.MethodPost, "/operations"))
		if response.Code != http.StatusTeapot {
			t.Fatalf("error status=%d", response.Code)
		}
	}
	if *errorsWritten != 6 {
		t.Fatalf("errors written=%d", *errorsWritten)
	}

	missingKey, _ := operationsEdgeHandler(operationsServiceEdgeStub{})
	request := operationEdgeRequest(http.MethodPost, "/operations")
	request.Header.Del("Idempotency-Key")
	response := httptest.NewRecorder()
	missingKey.submitOperation(response, request)
	if response.Code != http.StatusTeapot {
		t.Fatalf("missing-key status=%d", response.Code)
	}
	missingKey.decodeJSON = nil
	response = httptest.NewRecorder()
	missingKey.submitOperation(response, operationEdgeRequest(http.MethodPost, "/operations"))
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("nil-decode status=%d body=%s", response.Code, response.Body.String())
	}
	missingKey.decodeJSON = func(http.ResponseWriter, *http.Request, any) bool { return false }
	response = httptest.NewRecorder()
	missingKey.submitOperation(response, operationEdgeRequest(http.MethodPost, "/operations"))
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("false-decode status=%d body=%s", response.Code, response.Body.String())
	}

	controlHandler, _ := operationsEdgeHandler(operationsServiceEdgeStub{})
	response = httptest.NewRecorder()
	controlHandler.setControl(response, operationEdgeRequest(http.MethodPost, "/operations"))
	if response.Code != http.StatusTeapot {
		t.Fatalf("nil-control status=%d", response.Code)
	}
	response = httptest.NewRecorder()
	controlHandler.listControls(response, operationEdgeRequest(http.MethodGet, "/operations"))
	if response.Code != http.StatusTeapot {
		t.Fatalf("nil-control-list status=%d", response.Code)
	}
	controlHandler.controls = operationsControlEdgeStub{err: failure}
	request = operationEdgeRequest(http.MethodPost, "/operations")
	request.Header.Del("Idempotency-Key")
	response = httptest.NewRecorder()
	controlHandler.setControl(response, request)
	if response.Code != http.StatusTeapot {
		t.Fatalf("control missing-key status=%d", response.Code)
	}
	controlHandler.decodeJSON = nil
	response = httptest.NewRecorder()
	controlHandler.setControl(response, operationEdgeRequest(http.MethodPost, "/operations"))
	if response.Code != http.StatusOK {
		t.Fatalf("control nil-decode status=%d", response.Code)
	}
	controlHandler.decodeJSON = func(http.ResponseWriter, *http.Request, any) bool { return true }
	response = httptest.NewRecorder()
	controlHandler.setControl(response, operationEdgeRequest(http.MethodPost, "/operations"))
	if response.Code != http.StatusTeapot {
		t.Fatalf("control-error status=%d", response.Code)
	}
	response = httptest.NewRecorder()
	controlHandler.listControls(response, operationEdgeRequest(http.MethodGet, "/operations"))
	if response.Code != http.StatusTeapot {
		t.Fatalf("control-list-error status=%d", response.Code)
	}

	ownerHandler, _ := operationsEdgeHandler(operationsServiceEdgeStub{})
	audits := 0
	ownerHandler.securityAudit = func(*http.Request, principalmodel.Principal, string, string, map[string]any) { audits++ }
	response = httptest.NewRecorder()
	ownerHandler.retry(response, operationEdgeRequest(http.MethodPost, "/operations"))
	if response.Code != http.StatusOK || audits != 1 {
		t.Fatalf("owner success status=%d audits=%d", response.Code, audits)
	}
	ownerHandler.service = operationsServiceEdgeStub{executeErr: failure}
	response = httptest.NewRecorder()
	ownerHandler.reset(response, operationEdgeRequest(http.MethodPost, "/operations"))
	if response.Code != http.StatusTeapot {
		t.Fatalf("owner execute error status=%d", response.Code)
	}
	ownerHandler.service = operationsServiceEdgeStub{err: failure}
	response = httptest.NewRecorder()
	ownerHandler.retry(response, operationEdgeRequest(http.MethodPost, "/operations"))
	if response.Code != http.StatusTeapot {
		t.Fatalf("owner callback error status=%d", response.Code)
	}

	response = httptest.NewRecorder()
	handler.actOnDeadLetter(response, operationEdgeRequest(http.MethodPost, "/operations"))
	if response.Code != http.StatusTeapot {
		t.Fatalf("dead-letter action error status=%d", response.Code)
	}
}

func TestListOperationsPassesAllLedgerFiltersToApplication(t *testing.T) {
	var captured operationsmodel.OperationsReceiptFilter
	handler, _ := operationsEdgeHandler(operationsServiceEdgeStub{searchFilter: &captured})
	response := httptest.NewRecorder()
	handler.listOperations(response, operationEdgeRequest(http.MethodGet, "/operations?status=failed&failure_class=manual_intervention&kind=workflow.process.retry&resource_type=workflow_process&resource_id=process-1&requested_by=operator-1&correlation=req-1&created_from=2026-07-26T00%3A00%3A00Z&created_to=2026-07-26T02%3A00%3A00Z&search=INC-42&limit=17"))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	if captured.Status != operationsmodel.OperationsStatusFailed ||
		captured.FailureClass != operationsmodel.OperationsFailureManualIntervention ||
		captured.Kind != "workflow.process.retry" ||
		captured.ResourceType != "workflow_process" ||
		captured.ResourceID != "process-1" ||
		captured.RequestedBy != "operator-1" ||
		captured.Correlation != "req-1" ||
		captured.CreatedFrom != "2026-07-26T00:00:00Z" ||
		captured.CreatedTo != "2026-07-26T02:00:00Z" ||
		captured.Search != "INC-42" ||
		captured.Limit != 17 {
		t.Fatalf("captured=%#v", captured)
	}
}

func TestDatabaseRetirementAndLeaseRemainingEdges(t *testing.T) {
	failure := errors.New("service failed")
	handler, _ := operationsEdgeHandler(operationsServiceEdgeStub{})
	handler.databaseRetirement = databaseRetirementEdgeStub{err: failure}
	for _, call := range []func(http.ResponseWriter, *http.Request){handler.discoverDatabaseRetirement, handler.listDatabaseRetirements, handler.getDatabaseRetirement, handler.previewDatabaseRetirement} {
		response := httptest.NewRecorder()
		call(response, operationEdgeRequest(http.MethodPost, "/operations"))
		if response.Code != http.StatusTeapot {
			t.Fatalf("database error status=%d", response.Code)
		}
	}
	handler.databaseRetirement = databaseRetirementEdgeStub{}
	handler.securityAudit = nil
	response := httptest.NewRecorder()
	handler.previewDatabaseRetirement(response, operationEdgeRequest(http.MethodPost, "/operations"))
	if response.Code != http.StatusOK {
		t.Fatalf("database preview success status=%d", response.Code)
	}
	for _, call := range []func(http.ResponseWriter, *http.Request){handler.advanceDatabaseRetirement, handler.executeDatabaseRetirement} {
		response := httptest.NewRecorder()
		call(response, operationEdgeRequest(http.MethodPost, "/operations"))
		if response.Code != http.StatusOK {
			t.Fatalf("database success status=%d", response.Code)
		}
	}
	handler.decodeJSON = nil
	response = httptest.NewRecorder()
	handler.discoverDatabaseRetirement(response, operationEdgeRequest(http.MethodPost, "/operations"))
	if response.Code != http.StatusOK {
		t.Fatalf("database nil decode status=%d", response.Code)
	}
	response = httptest.NewRecorder()
	handler.advanceDatabaseRetirement(response, operationEdgeRequest(http.MethodPost, "/operations"))
	if response.Code != http.StatusOK {
		t.Fatalf("database advance nil decode status=%d", response.Code)
	}
	handler.decodeJSON = func(http.ResponseWriter, *http.Request, any) bool { return false }
	response = httptest.NewRecorder()
	handler.discoverDatabaseRetirement(response, operationEdgeRequest(http.MethodPost, "/operations"))
	if response.Code != http.StatusOK {
		t.Fatalf("database false decode status=%d", response.Code)
	}
	response = httptest.NewRecorder()
	handler.advanceDatabaseRetirement(response, operationEdgeRequest(http.MethodPost, "/operations"))
	if response.Code != http.StatusOK {
		t.Fatalf("database advance false decode status=%d", response.Code)
	}
	leaseHandler, _ := operationsEdgeHandler(operationsServiceEdgeStub{})
	leaseHandler.leases = operationsLeaseEdgeStub{err: failure}
	response = httptest.NewRecorder()
	leaseHandler.forceReleaseLease(response, operationEdgeRequest(http.MethodPost, "/operations"))
	if response.Code != http.StatusTeapot {
		t.Fatalf("lease error status=%d", response.Code)
	}
	leaseHandler.leases = operationsLeaseEdgeStub{}
	response = httptest.NewRecorder()
	leaseHandler.forceReleaseLease(response, operationEdgeRequest(http.MethodPost, "/operations"))
	if response.Code != http.StatusOK {
		t.Fatalf("lease success status=%d", response.Code)
	}
	leaseHandler.decodeJSON = nil
	response = httptest.NewRecorder()
	leaseHandler.forceReleaseLease(response, operationEdgeRequest(http.MethodPost, "/operations"))
	if response.Code != http.StatusOK {
		t.Fatalf("lease nil-decode status=%d", response.Code)
	}
}

func TestLifecycleRemainingDecodeDefaultAndServiceErrorEdges(t *testing.T) {
	mux, _ := newLifecycleGovernanceHTTPMux(t)
	for _, test := range []struct{ path, body string }{
		{path: "/operations/lifecycle/subjects", body: "{"},
		{path: "/operations/lifecycle/subjects/request/verify", body: "{"},
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("malformed %s status=%d body=%s", test.path, response.Code, response.Body.String())
		}
	}

	denied := []struct{ method, path, body string }{
		{http.MethodPost, "/operations/lifecycle/cleanup/jobs", `{}`},
		{http.MethodPost, "/operations/lifecycle/cleanup/jobs/missing/run", `{}`},
		{http.MethodPost, "/operations/lifecycle/policies", `{"status":"draft","published_at":"2026-01-01T00:00:00Z"}`},
		{http.MethodPost, "/operations/lifecycle/legal-holds", `{}`},
		{http.MethodPost, "/operations/lifecycle/legal-holds/missing/end", `{"ended_at":"2026-01-01T00:00:00Z"}`},
		{http.MethodPost, "/operations/lifecycle/subjects", `{}`},
		{http.MethodPost, "/operations/lifecycle/deletions/replay", `{}`},
		{http.MethodGet, "/operations/lifecycle/subjects/missing/download", ""},
		{http.MethodPost, "/operations/lifecycle/external-erasures/missing/reconcile", `{}`},
	}
	for _, test := range denied {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		request.Header.Set("X-Deny", "true")
		request.Header.Set("Idempotency-Key", "key")
		mux.ServeHTTP(response, request)
		if response.Code < http.StatusBadRequest {
			t.Fatalf("denied %s status=%d body=%s", test.path, response.Code, response.Body.String())
		}
	}
}
