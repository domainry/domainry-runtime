package action

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"

	actionruntime "github.com/domainry/domainry-runtime/runtime/domain/action/runtime"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

type actionUnitOfWorkStoreProbe struct {
	commits               [][]transactionmodel.RecordMutationCommit
	completions           []actionmodel.ActionExecutionCompletion
	directCompletions     []actionmodel.ActionExecutionCompletion
	completeCalls         int
	beginTransactionCalls int
	rollbackCalls         int
	commitErr             error
	completeErr           error
	onCommit              func()
}

type actionUnitOfWorkTransactionContextKey struct{}

type actionUnitOfWorkTransactionProbe struct {
	store *actionUnitOfWorkStoreProbe
}

func (*actionUnitOfWorkTransactionProbe) Context(ctx context.Context) context.Context {
	return context.WithValue(ctx, actionUnitOfWorkTransactionContextKey{}, true)
}

func (t *actionUnitOfWorkTransactionProbe) Commit(ctx context.Context, commits []transactionmodel.RecordMutationCommit, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	return t.store.commitTransaction(ctx, commits, completion)
}

func (t *actionUnitOfWorkTransactionProbe) RollBack(context.Context) error {
	t.store.rollbackCalls++
	return nil
}

func (s *actionUnitOfWorkStoreProbe) BeginExecutionTransaction(ctx context.Context) (actioncontract.ActionExecutionTransaction, error) {
	s.beginTransactionCalls++
	return &actionUnitOfWorkTransactionProbe{store: s}, nil
}

func (*actionUnitOfWorkStoreProbe) TryBeginExecution(_ context.Context, request actionmodel.ActionExecutionClaimRequest) (actionmodel.ActionExecutionClaimResult, error) {
	execution := request.Execution
	execution.ID, execution.LeaseOwner, execution.FencingToken = "execution-1", request.LeaseOwner, 1
	return actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionAcquired, Execution: execution}, nil
}

func (*actionUnitOfWorkStoreProbe) HeartbeatExecution(context.Context, string, string, int64, time.Time, time.Time) error {
	return nil
}

func (s *actionUnitOfWorkStoreProbe) CompleteExecution(_ context.Context, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	s.completeCalls++
	s.directCompletions = append(s.directCompletions, completion)
	if s.completeErr != nil {
		return actionmodel.ActionBusinessExecution{}, s.completeErr
	}
	return completion.Execution, nil
}

func (s *actionUnitOfWorkStoreProbe) commitTransaction(_ context.Context, commits []transactionmodel.RecordMutationCommit, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	if s.onCommit != nil {
		s.onCommit()
	}
	if s.commitErr != nil {
		return actionmodel.ActionBusinessExecution{}, s.commitErr
	}
	cloned := append([]transactionmodel.RecordMutationCommit(nil), commits...)
	s.commits = append(s.commits, cloned)
	s.completions = append(s.completions, completion)
	return completion.Execution, nil
}

type actionPhaseMutationHandler struct {
	descriptor runtimeext.HandlerDescriptor
	execution  runtimeext.ActionExecution
	observed   []runtimeext.ExecutionPhase
	fail       error
	rawOutput  json.RawMessage
	panicValue any
}

type actionLockingReadHandler struct {
	descriptor runtimeext.HandlerDescriptor
	observed   []runtimeext.ExecutionPhase
}

func (h *actionLockingReadHandler) Descriptor() runtimeext.HandlerDescriptor { return h.descriptor }

func (h *actionLockingReadHandler) Invoke(ctx context.Context, execution runtimeext.ActionExecution, _ json.RawMessage) (json.RawMessage, error) {
	h.observed = append(h.observed, execution.Phase())
	if _, err := execution.QueryRecords(ctx, runtimeext.RecordQuery{Operation: runtimeext.QueryGet, ObjectKey: "booking", RecordID: "booking-1"}); err != nil {
		return nil, err
	}
	h.observed = append(h.observed, execution.Phase())
	if _, err := execution.QueryRecords(ctx, runtimeext.RecordQuery{Operation: runtimeext.QueryGetForUpdate, ObjectKey: "booking", RecordID: "booking-1"}); err != nil {
		return nil, err
	}
	h.observed = append(h.observed, execution.Phase())
	return json.RawMessage(`{"locked":true}`), nil
}

func (h *actionPhaseMutationHandler) Descriptor() runtimeext.HandlerDescriptor { return h.descriptor }

func (h *actionPhaseMutationHandler) Invoke(ctx context.Context, execution runtimeext.ActionExecution, _ json.RawMessage) (json.RawMessage, error) {
	h.execution = execution
	h.observed = append(h.observed, execution.Phase())
	if _, err := execution.ApplyRecordMutation(ctx, runtimeext.RecordMutation{Operation: runtimeext.MutationCreate, ObjectKey: "booking", Fields: map[string]any{"status": "reserved"}}); err != nil {
		return nil, err
	}
	h.observed = append(h.observed, execution.Phase())
	if h.panicValue != nil {
		panic(h.panicValue)
	}
	if h.fail != nil {
		return nil, h.fail
	}
	if h.rawOutput != nil {
		return h.rawOutput, nil
	}
	return json.RawMessage(`{"accepted":true}`), nil
}

type actionUnitOfWorkMutationHandler struct {
	descriptor runtimeext.HandlerDescriptor
}

func (h actionUnitOfWorkMutationHandler) Descriptor() runtimeext.HandlerDescriptor {
	return h.descriptor
}

func (h actionUnitOfWorkMutationHandler) Invoke(ctx context.Context, execution runtimeext.ActionExecution, _ json.RawMessage) (json.RawMessage, error) {
	_, err := execution.ApplyRecordMutation(ctx, runtimeext.RecordMutation{Operation: runtimeext.MutationCreate, ObjectKey: "booking", Fields: map[string]any{"status": "reserved"}})
	return json.RawMessage(`{"accepted":true}`), err
}

type bookClassUnitOfWorkHandler struct {
	descriptor runtimeext.HandlerDescriptor
	receipt    *runtimeext.DurableIntentReceipt
}

func (h bookClassUnitOfWorkHandler) Descriptor() runtimeext.HandlerDescriptor {
	return h.descriptor
}

func (h bookClassUnitOfWorkHandler) Invoke(ctx context.Context, execution runtimeext.ActionExecution, _ json.RawMessage) (json.RawMessage, error) {
	locked, err := execution.QueryRecords(ctx, runtimeext.RecordQuery{
		Operation: runtimeext.QueryGetForUpdate, ObjectKey: "group_class", RecordID: "class-1",
	})
	if err != nil {
		return nil, err
	}
	if len(locked.Records) != 1 || locked.Records[0].ID != "class-1" {
		return nil, errors.New("locked class is missing")
	}
	if _, err := execution.ApplyRecordMutation(ctx, runtimeext.RecordMutation{
		Operation: runtimeext.MutationConditionalUpdate, ObjectKey: "group_class", RecordID: "class-1",
		Predicates: []runtimeext.Predicate{{
			Field: "remaining_capacity", Operator: "gte", Value: int64(1), ErrorCode: "gym.class_capacity_full",
		}},
		Arithmetic: []runtimeext.Arithmetic{{Field: "remaining_capacity", Operation: "decrement", Operand: int64(1)}},
	}); err != nil {
		return nil, err
	}
	if _, err := execution.ApplyRecordMutation(ctx, runtimeext.RecordMutation{
		Operation: runtimeext.MutationCreate, ObjectKey: "class_booking",
		Fields: map[string]any{"class_id": "class-1", "member_id": "member-1", "status": "booked"},
	}); err != nil {
		return nil, err
	}
	receipt, err := execution.StageDurableIntent(ctx, runtimeext.DurableIntent{
		ConsumerKey: "member_center", ConnectionKey: "primary", OperationKey: "send_notice",
		ContractSHA256: strings.Repeat("c", 64), Payload: map[string]any{"member_id": "member-1", "sequence": int64(1)},
	})
	if err != nil {
		return nil, err
	}
	if h.receipt != nil {
		*h.receipt = receipt
	}
	return json.RawMessage(`{"booking_id":"booking-1","status":"booked"}`), nil
}

func TestActionUnitOfWorkCommitsSystemAndBusinessOwnersThroughOneBoundary(t *testing.T) {
	newPlan := func(actionKey, objectKey, operation string, fields map[string]any, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
		record := recordmodel.Record{ID: objectKey + "-1", Data: fields}
		mutationContext, err := transactionmodel.NewMutationContext(transactionmodel.MutationContextInput{
			WorkspaceID: principal.WorkspaceID, Source: transactionmodel.MutationSourceAction, ActionKey: actionKey, CorrelationID: "correlation-1", ApplicationSchemaRevision: "snapshot-1",
		})
		if err != nil {
			return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
		}
		plan, err := transactionmodel.NewMutationPlan(mutationContext, transactionmodel.RecordMutationCommit{
			Operation: operation, Object: definitionmodel.ObjectSchema{Key: objectKey}, Record: record,
		}, nil)
		return plan, record, err
	}

	t.Run("system operation", func(t *testing.T) {
		store := &actionUnitOfWorkStoreProbe{}
		action := definitionmodel.ActionSchema{Key: "order.create", ObjectKey: "order", Kind: definitionmodel.ActionKindObjectCreate}
		system := NewSystemOperationCatalog(SystemOperationDescriptor{Key: SystemOperationCreate, Kind: definitionmodel.ActionKindObjectCreate, WriteOperation: "create"})
		handlers := NewRecordSystemOperationHandlers(RecordSystemOperationDependencies{PlanCreateMutation: func(ctx context.Context, objectKey string, fields map[string]any, _ string, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
			if active, _ := ctx.Value(actionUnitOfWorkTransactionContextKey{}).(bool); !active {
				t.Fatal("System Operation planning did not receive Action transaction context")
			}
			return newPlan(action.Key, objectKey, "create", fields, principal)
		}})
		service := NewActionApplication(ActionApplicationDependencies{
			Catalog:          NewActionCatalog([]definitionmodel.ActionSchema{action}, system, frozenEmptyHandlerRegistry(t)),
			SystemOperations: NewSystemOperationExecutor(system, SystemOperationBinding{Key: SystemOperationCreate, Handler: handlers.Create}),
			UnitOfWork:       NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(store)),
		})
		result, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{ActionKey: action.Key, ObjectKey: action.ObjectKey, Input: map[string]any{"status": "new"}, IdempotencyKey: "order-create-1", Principal: actionTestPrincipal("order.create")})
		assertOneActionUnitOfWorkCommit(t, result, err, store, "order")
	})

	t.Run("business handler", func(t *testing.T) {
		store := &actionUnitOfWorkStoreProbe{}
		action := actionTestPublishedContract(definitionmodel.ActionSchema{Key: "booking.reserve", ObjectKey: "booking", Kind: definitionmodel.ActionKindObjectOperation})
		handler := actionUnitOfWorkMutationHandler{descriptor: actionTestHandlerDescriptor(action.Key, []runtimeext.ActionObjectCapability{{ObjectKey: "booking", Operations: []string{"create"}}})}
		registry := runtimeext.NewProjectExtensionRegistry()
		if err := registry.RegisterBusinessHandler(handler); err != nil {
			t.Fatal(err)
		}
		registry.Freeze()
		system := NewSystemOperationCatalog()
		service := NewActionApplication(ActionApplicationDependencies{
			Catalog: NewActionCatalog([]definitionmodel.ActionSchema{action}, system, registry), SystemOperations: NewSystemOperationExecutor(system),
			BusinessHandlers: newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{PlanCreateMutation: func(ctx context.Context, objectKey string, fields map[string]any, _ string, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
				if active, _ := ctx.Value(actionUnitOfWorkTransactionContextKey{}).(bool); active {
					t.Fatal("Business Handler create planning retained Action transaction before external relation validation")
				}
				return newPlan(action.Key, objectKey, "create", fields, principal)
			}}),
			UnitOfWork: NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(store)),
		})
		result, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{ActionKey: action.Key, ObjectKey: action.ObjectKey, IdempotencyKey: "booking-reserve-1", Principal: actionTestPrincipal("booking.reserve")})
		assertOneActionUnitOfWorkCommit(t, result, err, store, "booking")
	})
}

func TestBusinessActionRelationValidationSeesEarlierPlannedCreate(t *testing.T) {
	parent := definitionmodel.ObjectSchema{Key: "parent"}
	child := definitionmodel.ObjectSchema{Key: "child", Fields: []definitionmodel.FieldSchema{{
		Key: "parent_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: parent.Key},
	}}}
	repositoryReads := 0
	repository := &actionRecordRepositoryProbe{
		get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
			repositoryReads++
			return recordmodel.Record{}, false, nil
		},
		list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
			return recordmodel.RecordPageResult{}, nil
		},
	}
	validator := recordservice.NewRecordRelationValidator(recordservice.RecordRelationValidationDependencies{
		Repository: repository,
		Object: func(_ context.Context, key string) (definitionmodel.ObjectSchema, bool) {
			return parent, key == parent.Key
		},
		CanAccessPersistedRecord: func(_ context.Context, _ principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record) (bool, error) {
			return object.Key == parent.Key && record.ID == "parent-1", nil
		},
	})
	newCreatePlan := func(object definitionmodel.ObjectSchema, record recordmodel.Record) (transactionmodel.MutationPlan, error) {
		mutationContext, err := transactionmodel.NewMutationContext(transactionmodel.MutationContextInput{
			WorkspaceID: "workspace-a", Source: transactionmodel.MutationSourceAction,
			ActionKey: "child.create_pair", CorrelationID: "correlation-1", ApplicationSchemaRevision: "snapshot-1",
		})
		if err != nil {
			return transactionmodel.MutationPlan{}, err
		}
		return transactionmodel.NewMutationPlan(mutationContext, transactionmodel.RecordMutationCommit{
			Operation: "create", Object: object, Record: record,
		}, nil)
	}
	execution := &businessActionExecution{
		dependencies: BusinessHandlerExecutionDependencies{
			PlanCreateMutation: func(ctx context.Context, objectKey string, fields map[string]any, _ string, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
				switch objectKey {
				case parent.Key:
					record := recordmodel.Record{ID: "parent-1", Data: fields}
					plan, err := newCreatePlan(parent, record)
					return plan, record, err
				case child.Key:
					if err := validator.Validate(ctx, child, fields, principal); err != nil {
						return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
					}
					record := recordmodel.Record{ID: "child-1", Data: fields}
					plan, err := newCreatePlan(child, record)
					return plan, record, err
				default:
					return transactionmodel.MutationPlan{}, recordmodel.Record{}, errors.New("unexpected object")
				}
			},
		},
		invocation: actionmodel.ActionInvocation{Principal: principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}}},
		action: definitionmodel.ActionSchema{Key: "child.create_pair", EffectSet: &definitionmodel.ActionEffectSet{
			Write: []definitionmodel.ActionObjectEffect{
				{ObjectKey: parent.Key, Operations: []string{"create"}},
				{ObjectKey: child.Key, Operations: []string{"create"}},
			},
		}},
		unitOfWork: newActionTestUnitOfWork(),
	}
	if _, err := execution.ApplyRecordMutation(t.Context(), runtimeext.RecordMutation{
		Operation: runtimeext.MutationCreate, ObjectKey: parent.Key, Fields: map[string]any{"name": "Parent"},
	}); err != nil {
		t.Fatalf("plan parent create: %v", err)
	}
	if _, err := execution.ApplyRecordMutation(t.Context(), runtimeext.RecordMutation{
		Operation: runtimeext.MutationCreate, ObjectKey: child.Key, Fields: map[string]any{"parent_id": "parent-1"},
	}); err != nil {
		t.Fatalf("plan related child create: %v", err)
	}
	if repositoryReads != 0 || len(execution.plans) != 2 {
		t.Fatalf("repository reads=%d plans=%d", repositoryReads, len(execution.plans))
	}
}

func TestActionUnitOfWorkFailsClosedWhenMutationStoreIsMissing(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "order.create", ObjectKey: "order", Kind: definitionmodel.ActionKindObjectCreate}
	system := NewSystemOperationCatalog(SystemOperationDescriptor{Key: SystemOperationCreate, Kind: definitionmodel.ActionKindObjectCreate, WriteOperation: "create"})
	handlers := NewRecordSystemOperationHandlers(RecordSystemOperationDependencies{PlanCreateMutation: func(_ context.Context, objectKey string, fields map[string]any, _ string, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
		context, err := transactionmodel.NewMutationContext(transactionmodel.MutationContextInput{WorkspaceID: principal.WorkspaceID, Source: transactionmodel.MutationSourceAction, ActionKey: action.Key, CorrelationID: "correlation-1", ApplicationSchemaRevision: "snapshot-1"})
		if err != nil {
			return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
		}
		record := recordmodel.Record{ID: "order-1", Data: fields}
		plan, err := transactionmodel.NewMutationPlan(context, transactionmodel.RecordMutationCommit{Operation: "create", Object: definitionmodel.ObjectSchema{Key: objectKey}, Record: record}, nil)
		return plan, record, err
	}})
	service := NewActionApplication(ActionApplicationDependencies{
		Catalog: NewActionCatalog([]definitionmodel.ActionSchema{action}, system, frozenEmptyHandlerRegistry(t)), SystemOperations: NewSystemOperationExecutor(system, SystemOperationBinding{Key: SystemOperationCreate, Handler: handlers.Create}),
	})
	result, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{ActionKey: action.Key, ObjectKey: action.ObjectKey, Input: map[string]any{"status": "new"}, IdempotencyKey: "order-create-no-store", Principal: actionTestPrincipal("order.create")})
	if apperror.CodeOf(err) != idempotency.ErrorCodeReceiptUnavailable || result.Status != "failed" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func TestActionUnitOfWorkReadinessRequiresTransactionalStore(t *testing.T) {
	events := []string{}
	nonTransactional := struct {
		actioncontract.ActionExecutionStore
	}{ActionExecutionStore: &governedExecutionStoreProbe{events: &events}}
	manager := NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(nonTransactional))
	errors := manager.ValidationErrors()
	if len(errors) != 1 || errors[0].Error() != "action unit of work transaction store is required" {
		t.Fatalf("validation errors = %v", errors)
	}
}

func TestActionExecutionPhaseFollowsRuntimeOwnedUnitOfWork(t *testing.T) {
	newService := func(t *testing.T, store *actionUnitOfWorkStoreProbe, handler *actionPhaseMutationHandler) *ActionApplicationService {
		t.Helper()
		registry := runtimeext.NewProjectExtensionRegistry()
		if err := registry.RegisterBusinessHandler(handler); err != nil {
			t.Fatal(err)
		}
		registry.Freeze()
		action := actionTestPublishedContract(definitionmodel.ActionSchema{Key: "booking.reserve", ObjectKey: "booking", Kind: definitionmodel.ActionKindObjectOperation})
		system := NewSystemOperationCatalog()
		return NewActionApplication(ActionApplicationDependencies{
			Catalog: NewActionCatalog([]definitionmodel.ActionSchema{action}, system, registry), SystemOperations: NewSystemOperationExecutor(system),
			BusinessHandlers: newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{PlanCreateMutation: func(_ context.Context, objectKey string, fields map[string]any, _ string, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
				record := recordmodel.Record{ID: "booking-1", Data: fields}
				mutationContext, err := transactionmodel.NewMutationContext(transactionmodel.MutationContextInput{WorkspaceID: principal.WorkspaceID, Source: transactionmodel.MutationSourceAction, ActionKey: action.Key, CorrelationID: "correlation-1", ApplicationSchemaRevision: "snapshot-1"})
				if err != nil {
					return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
				}
				plan, err := transactionmodel.NewMutationPlan(mutationContext, transactionmodel.RecordMutationCommit{Operation: "create", Object: definitionmodel.ObjectSchema{Key: objectKey}, Record: record}, nil)
				return plan, record, err
			}}),
			UnitOfWork: NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(store)),
		})
	}
	newHandler := func() *actionPhaseMutationHandler {
		return &actionPhaseMutationHandler{descriptor: actionTestHandlerDescriptor("booking.reserve", []runtimeext.ActionObjectCapability{{ObjectKey: "booking", Operations: []string{"create"}}})}
	}
	invocation := actionmodel.ActionInvocation{ActionKey: "booking.reserve", ObjectKey: "booking", IdempotencyKey: "booking-phase-1", Principal: actionTestPrincipal("booking.reserve")}

	t.Run("commit", func(t *testing.T) {
		handler, store := newHandler(), &actionUnitOfWorkStoreProbe{}
		store.onCommit = func() {
			if got := handler.execution.Phase(); got != runtimeext.ExecutionPhaseWriting {
				t.Fatalf("phase during commit = %q", got)
			}
		}
		result, err := newService(t, store, handler).Invoke(t.Context(), actionmodel.ActionSourceHTTP, invocation)
		if err != nil || result.Status != "success" || handler.execution.Phase() != runtimeext.ExecutionPhaseCommitted {
			t.Fatalf("result=%+v phase=%q error=%v", result, handler.execution.Phase(), err)
		}
		want := []runtimeext.ExecutionPhase{runtimeext.ExecutionPhasePrewrite, runtimeext.ExecutionPhaseWriting}
		if len(handler.observed) != len(want) || handler.observed[0] != want[0] || handler.observed[1] != want[1] {
			t.Fatalf("observed phases = %v", handler.observed)
		}
	})

	t.Run("handler failure rolls back", func(t *testing.T) {
		handler, store := newHandler(), &actionUnitOfWorkStoreProbe{}
		handler.fail = errors.New("handler failed")
		if _, err := newService(t, store, handler).Invoke(t.Context(), actionmodel.ActionSourceHTTP, invocation); err == nil || handler.execution.Phase() != runtimeext.ExecutionPhaseRolledBack {
			t.Fatalf("phase=%q error=%v", handler.execution.Phase(), err)
		}
		if len(store.commits) != 0 {
			t.Fatalf("commits after handler failure = %d", len(store.commits))
		}
		assertActionFailureCompletion(t, store, "backend.action.handler_failed", true)
	})

	t.Run("panic rolls back", func(t *testing.T) {
		handler, store := newHandler(), &actionUnitOfWorkStoreProbe{}
		handler.panicValue = "handler panic"
		result, err := newService(t, store, handler).Invoke(t.Context(), actionmodel.ActionSourceHTTP, invocation)
		if apperror.CodeOf(err) != "backend.action.handler_panicked" || result.Status != "failed" || handler.execution.Phase() != runtimeext.ExecutionPhaseRolledBack || len(store.commits) != 0 {
			t.Fatalf("result=%+v phase=%q commits=%d error=%v", result, handler.execution.Phase(), len(store.commits), err)
		}
		assertActionFailureCompletion(t, store, "backend.action.handler_panicked", true)
	})

	t.Run("cancel rolls back and is retryable", func(t *testing.T) {
		handler, store := newHandler(), &actionUnitOfWorkStoreProbe{}
		handler.fail = context.Canceled
		result, err := newService(t, store, handler).Invoke(t.Context(), actionmodel.ActionSourceHTTP, invocation)
		if apperror.CodeOf(err) != "backend.action.cancelled" || !errors.Is(err, context.Canceled) || !result.Retryable || handler.execution.Phase() != runtimeext.ExecutionPhaseRolledBack || len(store.commits) != 0 {
			t.Fatalf("result=%+v phase=%q commits=%d error=%v", result, handler.execution.Phase(), len(store.commits), err)
		}
		assertActionFailureCompletion(t, store, "backend.action.cancelled", true)
	})

	t.Run("timeout rolls back and is retryable", func(t *testing.T) {
		handler, store := newHandler(), &actionUnitOfWorkStoreProbe{}
		handler.fail = context.DeadlineExceeded
		result, err := newService(t, store, handler).Invoke(t.Context(), actionmodel.ActionSourceHTTP, invocation)
		if apperror.CodeOf(err) != "backend.action.timeout" || !errors.Is(err, context.DeadlineExceeded) || !result.Retryable || handler.execution.Phase() != runtimeext.ExecutionPhaseRolledBack || len(store.commits) != 0 {
			t.Fatalf("result=%+v phase=%q commits=%d error=%v", result, handler.execution.Phase(), len(store.commits), err)
		}
		assertActionFailureCompletion(t, store, "backend.action.timeout", true)
	})

	t.Run("failure receipt completion error replaces business result", func(t *testing.T) {
		handler := newHandler()
		handler.fail = &runtimeext.BusinessError{Code: "gym.class_waitlist_full"}
		store := &actionUnitOfWorkStoreProbe{commitErr: errors.New("receipt unavailable")}
		result, err := newService(t, store, handler).Invoke(t.Context(), actionmodel.ActionSourceHTTP, invocation)
		if apperror.CodeOf(err) != "backend.internal" || !result.Retryable ||
			handler.execution.Phase() != runtimeext.ExecutionPhaseRolledBack || store.completeCalls != 0 || store.beginTransactionCalls != 1 {
			t.Fatalf("result=%+v phase=%q complete=%d begins=%d error=%v", result, handler.execution.Phase(), store.completeCalls, store.beginTransactionCalls, err)
		}
	})

	t.Run("invalid typed output rolls back before commit", func(t *testing.T) {
		handler, store := newHandler(), &actionUnitOfWorkStoreProbe{}
		handler.rawOutput = json.RawMessage(`null`)
		result, err := newService(t, store, handler).Invoke(t.Context(), actionmodel.ActionSourceHTTP, invocation)
		if apperror.CodeOf(err) != "backend.action.handler_output_invalid" || result.Status != "failed" || handler.execution.Phase() != runtimeext.ExecutionPhaseRolledBack {
			t.Fatalf("result=%+v phase=%q error=%v", result, handler.execution.Phase(), err)
		}
		if len(store.commits) != 0 || store.completeCalls != 1 ||
			len(store.directCompletions) != 1 ||
			store.directCompletions[0].ErrorCode != "backend.action.handler_output_invalid" ||
			!store.directCompletions[0].Retryable {
			t.Fatalf("invalid output commits=%d complete=%d", len(store.commits), store.completeCalls)
		}
	})

	t.Run("generated output contract failure is internal and rolls back", func(t *testing.T) {
		handler, store := newHandler(), &actionUnitOfWorkStoreProbe{}
		handler.fail = &runtimeext.BusinessError{Code: "backend.generated.action_output_invalid", Message: "output does not match contract"}
		result, err := newService(t, store, handler).Invoke(t.Context(), actionmodel.ActionSourceHTTP, invocation)
		var appErr *apperror.AppError
		if apperror.CodeOf(err) != "backend.generated.action_output_invalid" || !errors.As(err, &appErr) || appErr.Kind != apperror.KindInternal || result.Status != "failed" || handler.execution.Phase() != runtimeext.ExecutionPhaseRolledBack {
			t.Fatalf("result=%+v phase=%q error=%v", result, handler.execution.Phase(), err)
		}
		if len(store.commits) != 0 || store.completeCalls != 1 ||
			len(store.directCompletions) != 1 ||
			store.directCompletions[0].ErrorCode != "backend.generated.action_output_invalid" ||
			!store.directCompletions[0].Retryable {
			t.Fatalf("invalid generated output commits=%d complete=%d", len(store.commits), store.completeCalls)
		}
	})

	t.Run("commit failure rolls back", func(t *testing.T) {
		handler, store := newHandler(), &actionUnitOfWorkStoreProbe{commitErr: errors.New("commit failed")}
		if _, err := newService(t, store, handler).Invoke(t.Context(), actionmodel.ActionSourceHTTP, invocation); err == nil || handler.execution.Phase() != runtimeext.ExecutionPhaseRolledBack {
			t.Fatalf("phase=%q error=%v", handler.execution.Phase(), err)
		}
	})

	t.Run("deadlock is retryable and rolls back", func(t *testing.T) {
		handler := newHandler()
		store := &actionUnitOfWorkStoreProbe{commitErr: mutation.TransactionTransient("booking", "booking-1", mutation.TransactionTransientDeadlock, errors.New("deadlock detected"))}
		result, err := newService(t, store, handler).Invoke(t.Context(), actionmodel.ActionSourceHTTP, invocation)
		if apperror.CodeOf(err) != "backend.transaction.deadlock" || !mutation.IsTransactionTransient(err, mutation.TransactionTransientDeadlock) || !result.Retryable || handler.execution.Phase() != runtimeext.ExecutionPhaseRolledBack {
			t.Fatalf("result=%+v phase=%q error=%v", result, handler.execution.Phase(), err)
		}
	})

	t.Run("business conflict is not retryable and rolls back", func(t *testing.T) {
		handler := newHandler()
		store := &actionUnitOfWorkStoreProbe{commitErr: mutation.PolicyConflict("backend.booking.capacity_conflict", "booking", "booking-1", "capacity")}
		result, err := newService(t, store, handler).Invoke(t.Context(), actionmodel.ActionSourceHTTP, invocation)
		if apperror.CodeOf(err) != "backend.booking.capacity_conflict" || result.Retryable || handler.execution.Phase() != runtimeext.ExecutionPhaseRolledBack {
			t.Fatalf("result=%+v phase=%q error=%v", result, handler.execution.Phase(), err)
		}
		assertActionFailureCompletion(t, store, "backend.booking.capacity_conflict", false)
	})

	t.Run("unknown commit remains unresolved and is retryable", func(t *testing.T) {
		handler := newHandler()
		store := &actionUnitOfWorkStoreProbe{commitErr: mutation.TransactionCommitUnknown("business_action_execution", "execution-1", errors.New("connection lost"))}
		result, err := newService(t, store, handler).Invoke(t.Context(), actionmodel.ActionSourceHTTP, invocation)
		if apperror.CodeOf(err) != mutation.TransactionCommitUnknownCode || !mutation.IsTransactionCommitUnknown(err) || !result.Retryable || handler.execution.Phase() != runtimeext.ExecutionPhaseWriting {
			t.Fatalf("result=%+v phase=%q error=%v", result, handler.execution.Phase(), err)
		}
		if store.completeCalls != 0 {
			t.Fatalf("commit unknown completed failure receipt calls=%d", store.completeCalls)
		}
	})
}

func assertActionFailureCompletion(t *testing.T, store *actionUnitOfWorkStoreProbe, code string, retryable bool) {
	t.Helper()
	if store.completeCalls != 1 || len(store.directCompletions) != 1 {
		t.Fatalf("failure completions calls=%d values=%d", store.completeCalls, len(store.directCompletions))
	}
	completion := store.directCompletions[0]
	if completion.ErrorCode != code || completion.Retryable != retryable || completion.ResponseStatus == 0 {
		t.Fatalf(
			"failure completion code=%q retryable=%v response_status=%d",
			completion.ErrorCode, completion.Retryable, completion.ResponseStatus,
		)
	}
}

func TestActionUnitOfWorkOpensLazilyForGetForUpdate(t *testing.T) {
	store := &actionUnitOfWorkStoreProbe{}
	handler := &actionLockingReadHandler{descriptor: actionTestHandlerDescriptor("booking.lock", []runtimeext.ActionObjectCapability{{ObjectKey: "booking", Operations: []string{"get", "get_for_update"}}})}
	registry := runtimeext.NewProjectExtensionRegistry()
	if err := registry.RegisterBusinessHandler(handler); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	action := actionTestPublishedContract(definitionmodel.ActionSchema{Key: "booking.lock", ObjectKey: "booking", Kind: definitionmodel.ActionKindObjectOperation})
	system := NewSystemOperationCatalog()
	service := NewActionApplication(ActionApplicationDependencies{
		Catalog: NewActionCatalog([]definitionmodel.ActionSchema{action}, system, registry), SystemOperations: NewSystemOperationExecutor(system),
		BusinessHandlers: newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{
			GetRecord: func(ctx context.Context, _, _ string, _ principalmodel.Principal) (recordmodel.Record, error) {
				if active, _ := ctx.Value(actionUnitOfWorkTransactionContextKey{}).(bool); active {
					t.Fatal("ordinary prewrite read unexpectedly entered Action transaction")
				}
				return recordmodel.Record{ID: "booking-1"}, nil
			},
			GetRecordForUpdate: func(ctx context.Context, _, _ string, _ principalmodel.Principal) (recordmodel.Record, error) {
				if active, _ := ctx.Value(actionUnitOfWorkTransactionContextKey{}).(bool); !active {
					t.Fatal("GetForUpdate did not receive Action transaction context")
				}
				return recordmodel.Record{ID: "booking-1"}, nil
			},
		}),
		UnitOfWork: NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(store)),
	})
	result, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{ActionKey: action.Key, ObjectKey: action.ObjectKey, IdempotencyKey: "booking-lock-1", Principal: actionTestPrincipal("booking.lock")})
	want := []runtimeext.ExecutionPhase{runtimeext.ExecutionPhasePrewrite, runtimeext.ExecutionPhasePrewrite, runtimeext.ExecutionPhaseWriting}
	if err != nil || result.Status != "success" || len(handler.observed) != len(want) || handler.observed[0] != want[0] || handler.observed[1] != want[1] || handler.observed[2] != want[2] {
		t.Fatalf("result=%+v phases=%v error=%v", result, handler.observed, err)
	}
	if store.beginTransactionCalls != 1 || len(store.commits) != 1 || len(store.commits[0]) != 0 || store.completeCalls != 0 {
		t.Fatalf("transaction begins=%d commits=%v complete=%d", store.beginTransactionCalls, store.commits, store.completeCalls)
	}
}

func TestActionScopeDenialAfterLockingReadRollsBackBeforeAtomicFailureAudit(t *testing.T) {
	store := &actionUnitOfWorkStoreProbe{}
	handler := &actionLockingReadHandler{descriptor: actionTestHandlerDescriptor("booking.lock", []runtimeext.ActionObjectCapability{{ObjectKey: "booking", Operations: []string{"get", "get_for_update"}}})}
	registry := runtimeext.NewProjectExtensionRegistry()
	if err := registry.RegisterBusinessHandler(handler); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	action := actionTestPublishedContract(definitionmodel.ActionSchema{Key: "booking.lock", ObjectKey: "booking", Kind: definitionmodel.ActionKindRecordOperation})
	system := NewSystemOperationCatalog()
	service := NewActionApplication(ActionApplicationDependencies{
		Catalog: NewActionCatalog([]definitionmodel.ActionSchema{action}, system, registry), SystemOperations: NewSystemOperationExecutor(system),
		BusinessHandlers: newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{
			GetRecord: func(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error) {
				return recordmodel.Record{ID: "booking-1"}, nil
			},
			GetRecordForUpdate: func(ctx context.Context, objectKey, recordID string, _ principalmodel.Principal) (recordmodel.Record, error) {
				if active, _ := ctx.Value(actionUnitOfWorkTransactionContextKey{}).(bool); !active {
					t.Fatal("scope-denied locking read did not receive Action transaction context")
				}
				return recordmodel.Record{}, apperror.New(
					apperror.KindNotFound,
					"backend.record.not_found",
					nil,
					map[string]string{"object_key": objectKey, "record_id": recordID},
				)
			},
		}),
		UnitOfWork: NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(store)),
	})
	principal := actionTestPrincipal("booking.lock")
	principal = accessfixture.WithMutation(principal, func(role *accessfixture.Bundle) {
		role.DataPolicies = []accessfixture.DataPolicyFixture{{
			ObjectKey: "booking", Read: true, Write: true, AuditDenial: true,
		}}
	})
	result, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		ActionKey: action.Key, ObjectKey: action.ObjectKey, RecordID: "booking-1", IdempotencyKey: "scope-denied", Principal: principal,
	})
	if apperror.CodeOf(err) != "backend.record.not_found" || result.Status != "failed" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if store.beginTransactionCalls != 2 {
		t.Fatalf("transaction begins=%d, want locking read plus failure completion", store.beginTransactionCalls)
	}
	if len(store.commits) != 1 || len(store.commits[0]) != 0 || len(store.completions) != 1 {
		t.Fatalf("commits=%v completions=%v", store.commits, store.completions)
	}
	completion := store.completions[0]
	if completion.ErrorCode != "backend.record.not_found" || completion.ResponseStatus != 404 || len(completion.AuditEvents) != 2 {
		t.Fatalf("failure completion=%+v", completion)
	}
	if completion.AuditEvents[0].Event != "action_execution_failed" || completion.AuditEvents[0].Metadata["result"] != "failed" {
		t.Fatalf("terminal failure audit=%+v", completion.AuditEvents[0])
	}
	audit := completion.AuditEvents[1]
	if audit.Event != "data_scope_access_denied" || audit.ObjectKey != "booking" || audit.RecordID != "booking-1" ||
		audit.Metadata["action_key"] != "booking.lock" || audit.Metadata["decision"] != "denied" {
		t.Fatalf("denial audit=%+v", audit)
	}
	if store.completeCalls != 0 {
		t.Fatalf("failure audit bypassed atomic transaction with direct completions=%d", store.completeCalls)
	}
}

func TestSynchronousConnectorCallLeaseExcludesActionTransactionAndLocks(t *testing.T) {
	store := &actionUnitOfWorkStoreProbe{}
	unitOfWork := &actionUnitOfWork{
		manager: NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(store)),
		claim:   actionmodel.ActionExecutionClaimResult{Execution: actionmodel.ActionBusinessExecution{ID: "execution-lease"}},
		phases:  newActionExecutionPhaseMachine(),
	}
	lease, err := unitOfWork.acquireSynchronousConnectorCall()
	if err != nil || lease == nil || unitOfWork.activeSynchronousConnectorCalls != 1 {
		t.Fatalf("lease=%T active=%d error=%v", lease, unitOfWork.activeSynchronousConnectorCalls, err)
	}
	if identified, ok := lease.(interface{ ConnectorRequestID() string }); !ok || identified.ConnectorRequestID() != "execution-lease:connector:1" {
		t.Fatalf("lease request identity=%T", lease)
	}
	if _, err := unitOfWork.beginWriting(t.Context()); apperror.CodeOf(err) != runtimeext.ActionWriteDuringConnectorCallErrorCode {
		t.Fatalf("write during Connector call error=%v", err)
	}
	if store.beginTransactionCalls != 0 || unitOfWork.phases.current() != runtimeext.ExecutionPhasePrewrite {
		t.Fatalf("blocked write opened transaction=%d phase=%s", store.beginTransactionCalls, unitOfWork.phases.current())
	}
	lease.Release()
	lease.Release()
	if unitOfWork.activeSynchronousConnectorCalls != 0 {
		t.Fatalf("idempotent lease release left active=%d", unitOfWork.activeSynchronousConnectorCalls)
	}
	if _, err := unitOfWork.beginWriting(t.Context()); err != nil {
		t.Fatal(err)
	}
	if store.beginTransactionCalls != 1 || unitOfWork.phases.current() != runtimeext.ExecutionPhaseWriting {
		t.Fatalf("transaction begins=%d phase=%s", store.beginTransactionCalls, unitOfWork.phases.current())
	}
	if _, err := unitOfWork.acquireSynchronousConnectorCall(); apperror.CodeOf(err) != runtimeext.ConnectorCallAfterWriteErrorCode {
		t.Fatalf("Connector call after write error=%v", err)
	}
}

func TestSynchronousConnectorLeaseAndWritingTransitionHaveNoCheckThenActWindow(t *testing.T) {
	type acquireResult struct {
		lease runtimeext.SynchronousConnectorCallLease
		err   error
	}
	for iteration := 0; iteration < 100; iteration++ {
		store := &actionUnitOfWorkStoreProbe{}
		unitOfWork := &actionUnitOfWork{
			manager: NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(store)),
			phases:  newActionExecutionPhaseMachine(),
		}
		start := make(chan struct{})
		acquired := make(chan acquireResult, 1)
		writing := make(chan error, 1)
		go func() {
			<-start
			lease, err := unitOfWork.acquireSynchronousConnectorCall()
			acquired <- acquireResult{lease: lease, err: err}
		}()
		go func() {
			<-start
			_, err := unitOfWork.beginWriting(t.Context())
			writing <- err
		}()
		close(start)
		callResult, writeErr := <-acquired, <-writing
		switch {
		case callResult.err == nil:
			if apperror.CodeOf(writeErr) != runtimeext.ActionWriteDuringConnectorCallErrorCode || callResult.lease == nil || store.beginTransactionCalls != 0 || unitOfWork.phases.current() != runtimeext.ExecutionPhasePrewrite {
				t.Fatalf("iteration=%d call won: lease=%T write=%v transaction=%d phase=%s", iteration, callResult.lease, writeErr, store.beginTransactionCalls, unitOfWork.phases.current())
			}
			callResult.lease.Release()
		case writeErr == nil:
			if apperror.CodeOf(callResult.err) != runtimeext.ConnectorCallAfterWriteErrorCode || callResult.lease != nil || store.beginTransactionCalls != 1 || unitOfWork.phases.current() != runtimeext.ExecutionPhaseWriting {
				t.Fatalf("iteration=%d write won: lease=%T call=%v transaction=%d phase=%s", iteration, callResult.lease, callResult.err, store.beginTransactionCalls, unitOfWork.phases.current())
			}
			unitOfWork.rollBack(t.Context())
		default:
			t.Fatalf("iteration=%d neither operation won: call=%v write=%v", iteration, callResult.err, writeErr)
		}
	}
}

func TestActionCommitFailsClosedWhileSynchronousConnectorCallIsActive(t *testing.T) {
	unitOfWork := &actionUnitOfWork{manager: NewActionUnitOfWorkManager(nil), phases: newActionExecutionPhaseMachine()}
	lease, err := unitOfWork.acquireSynchronousConnectorCall()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if err := unitOfWork.commit(t.Context(), actionmodel.ActionInvocationResult{}, nil, nil); apperror.CodeOf(err) != runtimeext.ActionWriteDuringConnectorCallErrorCode {
		t.Fatalf("commit during Connector call error=%v", err)
	}
}

func TestActionRollbackClosesSynchronousConnectorCallGate(t *testing.T) {
	unitOfWork := &actionUnitOfWork{manager: NewActionUnitOfWorkManager(nil), phases: newActionExecutionPhaseMachine()}
	unitOfWork.rollBack(t.Context())
	if unitOfWork.phases.current() != runtimeext.ExecutionPhaseRolledBack {
		t.Fatalf("rollback phase=%s", unitOfWork.phases.current())
	}
	if _, err := unitOfWork.acquireSynchronousConnectorCall(); apperror.CodeOf(err) != runtimeext.ConnectorCallAfterWriteErrorCode {
		t.Fatalf("Connector call after rollback error=%v", err)
	}
}

func TestBusinessActionExecutionEnforcesConnectorLeaseAtEveryWriteBoundary(t *testing.T) {
	version := int64(7)
	tests := map[string]func(context.Context, *businessActionExecution) error{
		"mutation": func(ctx context.Context, execution *businessActionExecution) error {
			_, err := execution.ApplyRecordMutation(ctx, runtimeext.RecordMutation{Operation: runtimeext.MutationCreate, ObjectKey: "booking", Fields: map[string]any{"status": "reserved"}})
			return err
		},
		"cas": func(ctx context.Context, execution *businessActionExecution) error {
			_, err := execution.ApplyRecordMutation(ctx, runtimeext.RecordMutation{Operation: runtimeext.MutationConditionalUpdate, ObjectKey: "booking", RecordID: "booking-1", Fields: map[string]any{"status": "reserved"}, ExpectedVersion: &version})
			return err
		},
		"get_for_update": func(ctx context.Context, execution *businessActionExecution) error {
			_, err := execution.QueryRecords(ctx, runtimeext.RecordQuery{Operation: runtimeext.QueryGetForUpdate, ObjectKey: "booking", RecordID: "booking-1"})
			return err
		},
	}
	for name, write := range tests {
		t.Run(name, func(t *testing.T) {
			store := &actionUnitOfWorkStoreProbe{}
			unitOfWork := &actionUnitOfWork{manager: NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(store)), phases: newActionExecutionPhaseMachine()}
			dependencyCalls := 0
			connectorGrant := runtimeext.ActionConnectorCapability{ConnectorKey: "directory", ConnectionKey: "primary", OperationKey: "lookup", ContractSHA256: strings.Repeat("a", 64), Mode: runtimeext.ConnectorModeCall, Effect: runtimeext.ConnectorEffectRead}
			execution := &businessActionExecution{
				unitOfWork:      unitOfWork,
				connectorGrants: []runtimeext.ActionConnectorCapability{connectorGrant},
				action: definitionmodel.ActionSchema{Key: "booking.reserve", EffectSet: &definitionmodel.ActionEffectSet{
					Read: []definitionmodel.ActionObjectEffect{{ObjectKey: "booking"}}, Write: []definitionmodel.ActionObjectEffect{{ObjectKey: "booking"}},
				}},
				dependencies: BusinessHandlerExecutionDependencies{
					PlanCreateMutation: func(context.Context, string, map[string]any, string, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
						dependencyCalls++
						return transactionmodel.MutationPlan{}, recordmodel.Record{ID: "booking-1"}, nil
					},
					PlanConditionalUpdate: func(context.Context, string, string, transactionmodel.ConditionalUpdateInput, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
						dependencyCalls++
						return transactionmodel.MutationPlan{}, recordmodel.Record{ID: "booking-1"}, nil
					},
					GetRecordForUpdate: func(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error) {
						dependencyCalls++
						return recordmodel.Record{ID: "booking-1"}, nil
					},
				},
			}
			lease, err := execution.AcquireSynchronousConnectorCall(connectorGrant)
			if err != nil {
				t.Fatal(err)
			}
			if err := write(t.Context(), execution); apperror.CodeOf(err) != runtimeext.ActionWriteDuringConnectorCallErrorCode {
				t.Fatalf("write during Connector call error=%v", err)
			}
			if store.beginTransactionCalls != 0 || dependencyCalls != 0 || execution.Phase() != runtimeext.ExecutionPhasePrewrite {
				t.Fatalf("blocked write transaction=%d dependencies=%d phase=%s", store.beginTransactionCalls, dependencyCalls, execution.Phase())
			}
			lease.Release()
			if err := write(t.Context(), execution); err != nil {
				t.Fatal(err)
			}
			wantTransactions := 1
			if name == "mutation" {
				wantTransactions = 0
			}
			if store.beginTransactionCalls != wantTransactions || dependencyCalls != 1 || execution.Phase() != runtimeext.ExecutionPhaseWriting {
				t.Fatalf("allowed write transaction=%d dependencies=%d phase=%s", store.beginTransactionCalls, dependencyCalls, execution.Phase())
			}
			if _, err := execution.AcquireSynchronousConnectorCall(connectorGrant); apperror.CodeOf(err) != runtimeext.ConnectorCallAfterWriteErrorCode {
				t.Fatalf("Connector call after %s error=%v", name, err)
			}
		})
	}
}

func TestActionSuccessAuditAndReceiptShareTransactionWithoutBusinessMutation(t *testing.T) {
	store := &actionUnitOfWorkStoreProbe{}
	handler := &catalogHandler{descriptor: actionTestHandlerDescriptor("booking.preview", []runtimeext.ActionObjectCapability{{ObjectKey: "booking", Operations: []string{"get"}}})}
	registry := runtimeext.NewProjectExtensionRegistry()
	if err := registry.RegisterBusinessHandler(handler); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	action := actionTestPublishedContract(definitionmodel.ActionSchema{Key: "booking.preview", ObjectKey: "booking", Kind: definitionmodel.ActionKindObjectOperation})
	system := NewSystemOperationCatalog()
	service := NewActionApplication(ActionApplicationDependencies{
		Catalog: NewActionCatalog([]definitionmodel.ActionSchema{action}, system, registry), SystemOperations: NewSystemOperationExecutor(system),
		BusinessHandlers: newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{}),
		UnitOfWork:       NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(store)),
		Audit: ActionAudit{BuildSuccess: func(context.Context, definitionmodel.ActionSchema, actionmodel.ActionInvocation, actionmodel.ActionInvocationResult) auditmodel.AuditEvent {
			return auditmodel.AuditEvent{ID: "action-audit-1", WorkspaceID: "workspace-a", Family: auditmodel.EventFamilyBusinessEntity, Event: "booking.previewed", CreatedAt: "2026-07-22T00:00:00Z"}
		}},
	})
	result, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{ActionKey: action.Key, ObjectKey: action.ObjectKey, IdempotencyKey: "booking-preview-1", Principal: actionTestPrincipal("booking.preview")})
	if err != nil || result.Status != "success" || store.beginTransactionCalls != 1 || store.completeCalls != 0 || len(store.completions) != 1 {
		t.Fatalf("result=%+v begins=%d complete=%d completions=%+v error=%v", result, store.beginTransactionCalls, store.completeCalls, store.completions, err)
	}
	if audits := store.completions[0].AuditEvents; len(audits) != 1 || audits[0].ID != "action-audit-1" {
		t.Fatalf("transactional Action audits=%+v", audits)
	}
}

func TestBookClassCommitsClassBookingAuditOutboxAndReceiptThroughOneUnitOfWork(t *testing.T) {
	newPlan := func(operation string, object definitionmodel.ObjectSchema, record recordmodel.Record, predicates []transactionmodel.MutationPredicate) (transactionmodel.MutationPlan, error) {
		mutationContext, err := transactionmodel.NewMutationContext(transactionmodel.MutationContextInput{
			WorkspaceID: "workspace-a", ActorID: "member-1", RoleKey: "member",
			Source: transactionmodel.MutationSourceAction, ActionKey: "group_class.book_class",
			CorrelationID: "book-class-1", ApplicationSchemaRevision: "snapshot-1",
		})
		if err != nil {
			return transactionmodel.MutationPlan{}, err
		}
		return transactionmodel.NewMutationPlan(mutationContext, transactionmodel.RecordMutationCommit{
			Operation: operation, Object: object, Record: record, Predicates: predicates,
		}, nil)
	}
	groupClass := definitionmodel.ObjectSchema{Key: "group_class", Fields: []definitionmodel.FieldSchema{{Key: "remaining_capacity", Type: "integer"}}}
	classBooking := definitionmodel.ObjectSchema{Key: "class_booking", Fields: []definitionmodel.FieldSchema{
		{Key: "class_id", Type: "relation"}, {Key: "member_id", Type: "relation"}, {Key: "status", Type: "select"},
	}}
	var durableReceipt runtimeext.DurableIntentReceipt
	connectorGrant := runtimeext.ActionConnectorCapability{
		ConnectorKey: "member_center", ConnectionKey: "primary", OperationKey: "send_notice",
		ContractSHA256: strings.Repeat("c", 64), Mode: runtimeext.ConnectorModeEnqueue, Effect: runtimeext.ConnectorEffectWrite,
	}
	handler := bookClassUnitOfWorkHandler{
		descriptor: actionTestHandlerDescriptor("group_class.book_class", []runtimeext.ActionObjectCapability{
			{ObjectKey: "group_class", Operations: []string{"get_for_update", "conditional_update"}},
			{ObjectKey: "class_booking", Operations: []string{"create"}},
		}, connectorGrant),
		receipt: &durableReceipt,
	}
	registry := runtimeext.NewProjectExtensionRegistry()
	if err := registry.RegisterBusinessHandler(handler); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	action := actionTestPublishedContract(definitionmodel.ActionSchema{
		Key: "group_class.book_class", ObjectKey: "group_class", Kind: definitionmodel.ActionKindObjectOperation,
		AuditEvent: "gym.class_booked",
		EffectSet: &definitionmodel.ActionEffectSet{
			Read: []definitionmodel.ActionObjectEffect{
				{ObjectKey: "group_class", Operations: []string{"get_for_update"}, Fields: []string{"remaining_capacity"}},
			},
			Write: []definitionmodel.ActionObjectEffect{
				{ObjectKey: "group_class", Operations: []string{"conditional_update"}, Fields: []string{"remaining_capacity"}},
				{ObjectKey: "class_booking", Operations: []string{"create"}, Fields: []string{"class_id", "member_id", "status"}},
			},
		},
	})
	store := &actionUnitOfWorkStoreProbe{}
	lockedInTransaction := false
	system := NewSystemOperationCatalog()
	service := NewActionApplication(ActionApplicationDependencies{
		Catalog: NewActionCatalog([]definitionmodel.ActionSchema{action}, system, registry), SystemOperations: NewSystemOperationExecutor(system),
		BusinessHandlers: newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{
			GetRecordForUpdate: func(ctx context.Context, objectKey, recordID string, _ principalmodel.Principal) (recordmodel.Record, error) {
				lockedInTransaction, _ = ctx.Value(actionUnitOfWorkTransactionContextKey{}).(bool)
				if objectKey != groupClass.Key || recordID != "class-1" {
					return recordmodel.Record{}, errors.New("unexpected class lock")
				}
				return recordmodel.Record{ID: recordID, Data: map[string]any{"remaining_capacity": int64(1)}}, nil
			},
			PlanConditionalUpdate: func(_ context.Context, objectKey, recordID string, input transactionmodel.ConditionalUpdateInput, _ principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
				if objectKey != groupClass.Key || recordID != "class-1" || len(input.Predicates) != 1 || len(input.Arithmetic) != 1 {
					return transactionmodel.MutationPlan{}, recordmodel.Record{}, errors.New("unexpected class reservation plan")
				}
				record := recordmodel.Record{ID: recordID, Data: map[string]any{"remaining_capacity": int64(0)}}
				plan, err := newPlan("update", groupClass, record, input.Predicates)
				return plan, record, err
			},
			PlanCreateMutation: func(_ context.Context, objectKey string, fields map[string]any, _ string, _ principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
				if objectKey != classBooking.Key || fields["class_id"] != "class-1" || fields["member_id"] != "member-1" || fields["status"] != "booked" {
					return transactionmodel.MutationPlan{}, recordmodel.Record{}, errors.New("unexpected booking plan")
				}
				record := recordmodel.Record{ID: "booking-1", Data: fields}
				plan, err := newPlan("create", classBooking, record, nil)
				return plan, record, err
			},
			ValidateDurableIntent: func(_ context.Context, intent runtimeext.DurableIntent, _ principalmodel.Principal) error {
				if intent.ConsumerKey != connectorGrant.ConnectorKey || intent.ConnectionKey != connectorGrant.ConnectionKey ||
					intent.OperationKey != connectorGrant.OperationKey || intent.ContractSHA256 != connectorGrant.ContractSHA256 {
					return errors.New("unexpected booking notice intent")
				}
				return nil
			},
		}),
		UnitOfWork: NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(store)),
		Audit: ActionAudit{BuildSuccess: func(context.Context, definitionmodel.ActionSchema, actionmodel.ActionInvocation, actionmodel.ActionInvocationResult) auditmodel.AuditEvent {
			return auditmodel.AuditEvent{ID: "book-class-audit-1", WorkspaceID: "workspace-a", Family: auditmodel.EventFamilyBusinessEntity, Event: "gym.class_booked", CreatedAt: "2026-07-23T00:00:00Z"}
		}},
	})
	result, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		ActionKey: action.Key, ObjectKey: action.ObjectKey, IdempotencyKey: "book-class-1",
		Principal: actionTestPrincipal("group_class.book_class"),
	})
	if err != nil || result.Status != "success" || !lockedInTransaction || store.beginTransactionCalls != 1 || store.completeCalls != 0 ||
		len(store.commits) != 1 || len(store.commits[0]) != 2 || len(store.completions) != 1 {
		t.Fatalf("result=%+v begins=%d complete=%d commits=%+v completions=%+v err=%v",
			result, store.beginTransactionCalls, store.completeCalls, store.commits, store.completions, err)
	}
	commits := store.commits[0]
	if commits[0].Object.Key != "group_class" || commits[0].Operation != "update" ||
		len(commits[0].Predicates) != 1 || commits[0].Predicates[0].ErrorCode != "gym.class_capacity_full" ||
		commits[1].Object.Key != "class_booking" || commits[1].Operation != "create" ||
		len(commits[0].Outbox) != 0 || len(commits[1].Outbox) != 1 {
		t.Fatalf("transactional BookClass commits=%+v", commits)
	}
	intent := commits[1].Outbox[0]
	completion := store.completions[0]
	if durableReceipt.ID != "durable_intent:execution-1:0" || intent.ID != durableReceipt.ID ||
		intent.ConnectorKey != "member_center" || intent.Operation != "send_notice" ||
		completion.ExecutionID != "execution-1" || len(completion.AuditEvents) != 1 ||
		completion.AuditEvents[0].ID != "book-class-audit-1" {
		t.Fatalf("receipt=%+v intent=%+v completion=%+v", durableReceipt, intent, completion)
	}
}

func frozenEmptyHandlerRegistry(t *testing.T) *runtimeext.ProjectExtensionRegistry {
	t.Helper()
	registry := runtimeext.NewProjectExtensionRegistry()
	registry.Freeze()
	return registry
}

func assertOneActionUnitOfWorkCommit(t *testing.T, result actionmodel.ActionInvocationResult, err error, store *actionUnitOfWorkStoreProbe, objectKey string) {
	t.Helper()
	if err != nil || result.Status != "success" || store.beginTransactionCalls != 1 || len(store.commits) != 1 || len(store.commits[0]) != 1 || store.commits[0][0].Object.Key != objectKey || store.completeCalls != 0 {
		t.Fatalf("result=%+v transaction begins=%d commits=%+v complete=%d error=%v", result, store.beginTransactionCalls, store.commits, store.completeCalls, err)
	}
}

func newActionTestUnitOfWork() *actionUnitOfWork {
	return &actionUnitOfWork{
		manager: NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(&actionUnitOfWorkStoreProbe{})),
		phases:  newActionExecutionPhaseMachine(),
	}
}
