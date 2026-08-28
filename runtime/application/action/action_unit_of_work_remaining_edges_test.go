package action

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionruntime "github.com/domainry/domainry-runtime/runtime/domain/action/runtime"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
)

type actionUnitOfWorkBeginFailureStore struct {
	actionUnitOfWorkStoreProbe
	err error
}

func (s *actionUnitOfWorkBeginFailureStore) BeginExecutionTransaction(context.Context) (actioncontract.ActionExecutionTransaction, error) {
	return nil, s.err
}

func TestActionUnitOfWorkRemainingNilBoundaries(t *testing.T) {
	var manager *ActionUnitOfWorkManager
	if failures := manager.ValidationErrors(); len(failures) != 1 {
		t.Fatalf("validation failures=%v", failures)
	}
	if manager.executionRuntime() != nil {
		t.Fatal("nil manager exposed an execution runtime")
	}

	action := definitionmodel.ActionSchema{Key: "booking.reserve", ObjectKey: "booking"}
	cached, unitOfWork, replay, err := manager.begin(t.Context(), actionmodel.ActionInvocation{}, action)
	if err != nil || replay || cached.Status != "" || unitOfWork == nil || unitOfWork.manager == nil {
		t.Fatalf("cached=%+v unit=%+v replay=%v err=%v", cached, unitOfWork, replay, err)
	}

	var nilUnitOfWork *actionUnitOfWork
	if nilUnitOfWork.executionID() != "" {
		t.Fatal("nil unit of work returned an execution ID")
	}
	if err := nilUnitOfWork.commit(t.Context(), nil, nil, nil); err != nil {
		t.Fatalf("nil commit err=%v", err)
	}
	if _, err := nilUnitOfWork.beginWriting(t.Context()); apperror.CodeOf(err) != "backend.action.transaction_unavailable" {
		t.Fatalf("nil begin writing err=%v", err)
	}
	if _, err := nilUnitOfWork.acquireSynchronousConnectorCall(); apperror.CodeOf(err) != runtimeext.ConnectorActionExecutionRequiredErrorCode {
		t.Fatalf("nil connector lease err=%v", err)
	}
	if got := nilUnitOfWork.executionContext(t.Context()); got != t.Context() {
		t.Fatal("nil execution context did not preserve the caller context")
	}
	nilUnitOfWork.rollBack(t.Context())
	if err := nilUnitOfWork.fail(t.Context(), actionmodel.ActionInvocationResult{}, errors.New("failed"), nil); err != nil {
		t.Fatalf("nil fail err=%v", err)
	}

	managerless := &actionUnitOfWork{phases: newActionExecutionPhaseMachine()}
	if err := managerless.commit(t.Context(), nil, nil, nil); err != nil {
		t.Fatalf("managerless commit err=%v", err)
	}
	if _, err := managerless.beginWriting(t.Context()); apperror.CodeOf(err) != "backend.action.transaction_unavailable" {
		t.Fatalf("managerless begin writing err=%v", err)
	}
	if err := managerless.fail(t.Context(), actionmodel.ActionInvocationResult{}, errors.New("failed"), nil); err != nil {
		t.Fatalf("managerless fail err=%v", err)
	}

	validManager := NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(&actionUnitOfWorkStoreProbe{}))
	if failures := validManager.ValidationErrors(); len(failures) != 0 {
		t.Fatalf("valid manager failures=%v", failures)
	}
}

func TestActionSynchronousConnectorLeaseRemainingReleaseBoundaries(t *testing.T) {
	var nilLease *actionSynchronousConnectorCallLease
	nilLease.Release()

	(&actionSynchronousConnectorCallLease{}).Release()

	unitOfWork := &actionUnitOfWork{}
	(&actionSynchronousConnectorCallLease{unitOfWork: unitOfWork}).Release()
	if unitOfWork.activeSynchronousConnectorCalls != 0 {
		t.Fatalf("active calls=%d", unitOfWork.activeSynchronousConnectorCalls)
	}
}

func TestActionUnitOfWorkBeginReturnsCachedRecordReplay(t *testing.T) {
	events := []string{}
	store := &governedExecutionStoreProbe{
		events:   &events,
		decision: idempotency.DecisionReplay,
		result: map[string]any{
			"action_key": "booking.reserve",
			"object_key": "booking",
			"record_id":  "booking-1",
			"record":     map[string]any{"id": "booking-1"},
		},
	}
	manager := NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(store))
	action := definitionmodel.ActionSchema{Key: "booking.reserve", ObjectKey: "booking"}
	cached, unitOfWork, replay, err := manager.begin(t.Context(), actionmodel.ActionInvocation{
		RecordID: "booking-1", IdempotencyKey: "replay-1",
	}, action)
	if err != nil || !replay || cached.Record == nil || unitOfWork.executionID() != "execution-1" {
		t.Fatalf("cached=%+v replay=%v execution=%q err=%v", cached, replay, unitOfWork.executionID(), err)
	}
}

func TestActionUnitOfWorkRemainingTransactionBoundaries(t *testing.T) {
	t.Run("commit rejects active connector after transaction exists", func(t *testing.T) {
		store := &actionUnitOfWorkStoreProbe{}
		unitOfWork := &actionUnitOfWork{
			manager:                         NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(store)),
			phases:                          newActionExecutionPhaseMachine(),
			transaction:                     &actionUnitOfWorkTransactionProbe{store: store},
			activeSynchronousConnectorCalls: 1,
		}
		if err := unitOfWork.commit(t.Context(), nil, nil, nil); apperror.CodeOf(err) != runtimeext.ActionWriteDuringConnectorCallErrorCode {
			t.Fatalf("commit err=%v", err)
		}
	})

	t.Run("begin transaction failure rolls back phase", func(t *testing.T) {
		store := &actionUnitOfWorkBeginFailureStore{err: errors.New("begin failed")}
		unitOfWork := &actionUnitOfWork{
			manager: NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(store)),
			phases:  newActionExecutionPhaseMachine(),
		}
		if _, err := unitOfWork.beginWriting(t.Context()); err == nil {
			t.Fatal("begin writing unexpectedly succeeded")
		}
		if unitOfWork.phases.current() != runtimeext.ExecutionPhaseRolledBack {
			t.Fatalf("phase=%s", unitOfWork.phases.current())
		}
	})

	t.Run("invalid phase rolls back opened transaction", func(t *testing.T) {
		store := &actionUnitOfWorkStoreProbe{}
		unitOfWork := &actionUnitOfWork{
			manager: NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(store)),
			phases:  newActionExecutionPhaseMachine(),
		}
		if err := unitOfWork.phases.rollBack(); err != nil {
			t.Fatal(err)
		}
		if _, err := unitOfWork.beginWriting(t.Context()); err == nil {
			t.Fatal("invalid phase transition unexpectedly succeeded")
		}
	})
}

func TestActionUnitOfWorkFailureSkipsUnknownCommit(t *testing.T) {
	unitOfWork := &actionUnitOfWork{
		manager: NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(&actionUnitOfWorkStoreProbe{})),
		phases:  newActionExecutionPhaseMachine(),
	}
	unknown := mutation.TransactionCommitUnknown("action", "execution-1", errors.New("lost response"))
	if err := unitOfWork.fail(t.Context(), actionmodel.ActionInvocationResult{}, unknown, nil); err != nil {
		t.Fatalf("unknown commit failure err=%v", err)
	}
}
