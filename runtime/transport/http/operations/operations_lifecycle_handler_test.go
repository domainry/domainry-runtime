package operations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	lifecyclehttp "github.com/domainry/domainry-runtime/runtime/transport/http/lifecycle"
)

type cleanupGovernanceStub struct {
	lifecyclesdk.Governance
	workspaceID string
	jobID       string
	batchSize   int
	principal   lifecycleaccess.Principal
}

func (s *cleanupGovernanceStub) ProcessCleanupJob(_ context.Context, workspaceID, jobID, _ string, _ time.Duration, batchSize int, _ time.Time, principal lifecycleaccess.Principal) (lifecyclemodel.CleanupJob, error) {
	s.workspaceID, s.jobID, s.batchSize, s.principal = workspaceID, jobID, batchSize, principal
	return lifecyclemodel.CleanupJob{ID: jobID, WorkspaceID: workspaceID, RequestedBy: principal.UserID, Status: lifecyclemodel.CleanupStatusSucceeded}, nil
}

type cleanupOperationRunnerStub struct {
	request operationsapplication.OperationsOwnerExecutionRequest
}

func (s *cleanupOperationRunnerStub) ExecuteOwnerOperation(ctx context.Context, request operationsapplication.OperationsOwnerExecutionRequest, _ principalmodel.Principal, run func(context.Context) (any, error)) (operationsapplication.OperationsOwnerExecutionResult, error) {
	s.request = request
	value, err := run(ctx)
	return operationsapplication.OperationsOwnerExecutionResult{
		Value:   value,
		Receipt: operationsmodel.OperationsReceipt{Command: operationsmodel.OperationsCommand{ID: "operation-1"}, StatusURL: "/operations/operation-1"},
	}, err
}

func TestRuntimeLifecycleHTTPKeepsOnlyDurableCleanupRunOrchestration(t *testing.T) {
	governance, operations := &cleanupGovernanceStub{}, &cleanupOperationRunnerStub{}
	handler := lifecyclehttp.NewLifecycleHandler(lifecyclehttp.LifecycleDependencies{
		Service: governance, Operations: operations,
		Principal: func(*http.Request) principalmodel.Principal {
			principal := principalmodel.NewSystemPrincipal("operator-a", principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test cleanup"))
			principal.WorkspaceID = "workspace-a"
			return principal
		},
		CleanupProcessorPrincipal: lifecycleaccess.NewSystemPrincipal("runtime-lifecycle-http", lifecycleaccess.NewSystemScope(lifecycleaccess.SystemScopeGlobal, "test cleanup processor")),
		Authenticated:             func(next http.HandlerFunc) http.HandlerFunc { return next },
		WriteJSON: func(writer http.ResponseWriter, status int, value any) {
			writer.WriteHeader(status)
			_ = json.NewEncoder(writer).Encode(value)
		},
		WriteServiceError: func(writer http.ResponseWriter, _ *http.Request, _ error) {
			writer.WriteHeader(http.StatusInternalServerError)
		},
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	request := httptest.NewRequest(http.MethodPost, "/lifecycle/cleanup/jobs/job-1/run?batch_size=25", nil)
	request.Header.Set("Idempotency-Key", "cleanup-1")
	request.Header.Set("X-Operation-Reason", "verified retention cleanup")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if governance.workspaceID != "workspace-a" || governance.jobID != "job-1" || governance.batchSize != 25 {
		t.Fatalf("cleanup call=%#v", governance)
	}
	if !governance.principal.Known || !governance.principal.SystemScope.Valid() || len(governance.principal.Permissions) != 0 {
		t.Fatalf("cleanup processor authority=%#v", governance.principal)
	}
	if operations.request.Key != "cleanup-1" || operations.request.Reason != "verified retention cleanup" || response.Header().Get("Operation-ID") != "operation-1" {
		t.Fatalf("operation request=%#v headers=%v", operations.request, response.Header())
	}

	migrated := httptest.NewRecorder()
	mux.ServeHTTP(migrated, httptest.NewRequest(http.MethodGet, "/lifecycle/policies", nil))
	if migrated.Code != http.StatusNotFound {
		t.Fatalf("module-owned Lifecycle route remained on Runtime router: %d", migrated.Code)
	}
}
