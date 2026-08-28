package integration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type actionConnectorGatewayLease struct {
	released int
}

func (l *actionConnectorGatewayLease) Release() { l.released++ }

type actionConnectorGatewayExecution struct {
	lease      runtimeext.SynchronousConnectorCallLease
	acquireErr error
	principal  runtimeext.Principal
	workspace  runtimeext.Workspace
}

func (e actionConnectorGatewayExecution) Identity() runtimeext.ExecutionIdentity {
	return runtimeext.ExecutionIdentity{}
}
func (e actionConnectorGatewayExecution) Principal() runtimeext.Principal {
	return e.principal
}
func (e actionConnectorGatewayExecution) Workspace() runtimeext.Workspace {
	return e.workspace
}
func (e actionConnectorGatewayExecution) Phase() runtimeext.ExecutionPhase {
	return runtimeext.ExecutionPhasePrewrite
}
func (e actionConnectorGatewayExecution) QueryRecords(context.Context, runtimeext.RecordQuery) (runtimeext.RecordQueryResult, error) {
	return runtimeext.RecordQueryResult{}, nil
}
func (e actionConnectorGatewayExecution) ApplyRecordMutation(context.Context, runtimeext.RecordMutation) (runtimeext.RecordMutationResult, error) {
	return runtimeext.RecordMutationResult{}, nil
}
func (e actionConnectorGatewayExecution) StageDurableIntent(context.Context, runtimeext.DurableIntent) (runtimeext.DurableIntentReceipt, error) {
	return runtimeext.DurableIntentReceipt{}, nil
}
func (e actionConnectorGatewayExecution) AcquireSynchronousConnectorCall(runtimeext.ActionConnectorCapability) (runtimeext.SynchronousConnectorCallLease, error) {
	return e.lease, e.acquireErr
}

func TestActionConnectorGatewayHoldsRuntimeLeaseAcrossCall(t *testing.T) {
	want := errors.New("provider failed")
	lease := &actionConnectorGatewayLease{}
	executed := 0
	ctx := t.Context()
	gateway := &ActionConnectorGateway{execute: func(actual context.Context, request SyncCallRequest, principal principalmodel.Principal) (SyncCallResult, error) {
		executed++
		if actual != ctx || request.Operation != "lookup" || principal.UserID != "user-1" || principal.WorkspaceID != "workspace-1" || principal.DepartmentID != "department-1" || principal.RoleKey != "operator" || principal.RequestID != "request-1" || principal.CorrelationID != "correlation-1" || principal.CausationID != "causation-1" || principal.SurfaceKey != "surface-1" || principal.AuthorizationRevision != "authorization-1" || !principal.Known || lease.released != 0 {
			t.Fatalf("ctx=%v request=%+v principal=%+v released=%d", actual, request, principal, lease.released)
		}
		return SyncCallResult{}, want
	}}
	execution := actionConnectorGatewayExecution{
		lease: lease, principal: runtimeext.Principal{
			UserID: "user-1", RoleKey: "operator", DepartmentID: "department-1", RequestID: "request-1",
			CorrelationID: "correlation-1", CausationID: "causation-1", SurfaceKey: "surface-1",
			AuthorizationRevision: "authorization-1", Known: true,
		},
		workspace: runtimeext.Workspace{ID: "workspace-1"},
	}
	_, err := gateway.Call(ctx, execution, SyncCallRequest{ConnectorKey: "directory", ConnectionKey: "primary", Operation: "lookup", OperationMode: "call", OperationEffect: "read", ContractSHA256: strings.Repeat("a", 64)})
	if !errors.Is(err, want) || executed != 1 || lease.released != 1 {
		t.Fatalf("executed=%d released=%d error=%v", executed, lease.released, err)
	}
}

func TestActionConnectorGatewayRejectsBeforeApplicationExecution(t *testing.T) {
	executed := 0
	gateway := &ActionConnectorGateway{execute: func(context.Context, SyncCallRequest, principalmodel.Principal) (SyncCallResult, error) {
		executed++
		return SyncCallResult{}, nil
	}}
	for name, execution := range map[string]runtimeext.ActionExecution{
		"missing execution": nil,
		"missing lease":     actionConnectorGatewayExecution{},
		"writing phase":     actionConnectorGatewayExecution{acquireErr: apperror.New(apperror.KindConflict, runtimeext.ConnectorCallAfterWriteErrorCode, nil, nil)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := gateway.Call(t.Context(), execution, SyncCallRequest{})
			if name == "writing phase" {
				if apperror.CodeOf(err) != runtimeext.ConnectorCallAfterWriteErrorCode {
					t.Fatalf("error=%v", err)
				}
			} else if apperror.CodeOf(err) != runtimeext.ConnectorActionExecutionRequiredErrorCode {
				t.Fatalf("error=%v", err)
			}
		})
	}
	_, err := gateway.Call(t.Context(), actionConnectorGatewayExecution{lease: &actionConnectorGatewayLease{}}, SyncCallRequest{})
	if apperror.CodeOf(err) != "backend.integration.sync_call.operation_identity_required" {
		t.Fatalf("missing operation identity error=%v", err)
	}
	if executed != 0 {
		t.Fatalf("application executed %d calls after gate rejection", executed)
	}
}

func TestActionConnectorGatewayConstructorAndUnavailableEdges(t *testing.T) {
	if gateway := NewActionConnectorGateway(&IntegrationApplicationService{}); gateway == nil || gateway.execute == nil {
		t.Fatalf("application gateway=%#v", gateway)
	}
	execution := actionConnectorGatewayExecution{lease: &actionConnectorGatewayLease{}}
	request := SyncCallRequest{ContractSHA256: strings.Repeat("a", 64), OperationMode: "call"}

	var nilGateway *ActionConnectorGateway
	if _, err := nilGateway.Call(t.Context(), execution, request); apperror.CodeOf(err) != "backend.connector.gateway_unavailable" {
		t.Fatalf("nil gateway error=%v", err)
	}
	if _, err := NewActionConnectorGateway(nil).Call(t.Context(), execution, request); apperror.CodeOf(err) != "backend.connector.gateway_unavailable" {
		t.Fatalf("nil application gateway error=%v", err)
	}

	executed := 0
	gateway := &ActionConnectorGateway{execute: func(context.Context, SyncCallRequest, principalmodel.Principal) (SyncCallResult, error) {
		executed++
		return SyncCallResult{}, nil
	}}
	if _, err := gateway.Call(t.Context(), execution, SyncCallRequest{ContractSHA256: strings.Repeat("a", 64)}); apperror.CodeOf(err) != "backend.integration.sync_call.operation_identity_required" {
		t.Fatalf("missing operation mode error=%v", err)
	}
	if executed != 0 {
		t.Fatalf("application executed %d incomplete identity calls", executed)
	}
}
