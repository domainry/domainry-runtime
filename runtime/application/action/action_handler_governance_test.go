package action

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionruntime "github.com/domainry/domainry-runtime/runtime/domain/action/runtime"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

type governedHandlerProbe struct {
	descriptor runtimeext.HandlerDescriptor
	events     *[]string
	invoked    int
	input      json.RawMessage
	allowEmpty bool
}

func (h *governedHandlerProbe) Descriptor() runtimeext.HandlerDescriptor { return h.descriptor }

func (h *governedHandlerProbe) Invoke(_ context.Context, _ runtimeext.ActionExecution, input json.RawMessage) (json.RawMessage, error) {
	h.invoked++
	h.input = append(h.input[:0], input...)
	*h.events = append(*h.events, "handler")
	var payload map[string]any
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if h.allowEmpty {
		if len(payload) != 0 {
			return nil, errors.New("handler received undeclared input")
		}
		return json.RawMessage(`{"accepted":true}`), nil
	}
	if payload["amount"] != 12.5 {
		return nil, errors.New("handler received unnormalized payload")
	}
	return json.RawMessage(`{"accepted":true}`), nil
}

func TestBusinessHandlerReceivesOnlyPublishedInputFields(t *testing.T) {
	events := []string{}
	handler := newGovernedHandlerProbe(&events)
	store := &governedExecutionStoreProbe{events: &events}
	service := newGovernedHandlerApplication(t, handler, ActionAuthorization{}, ActionAssurance{}, store)

	_, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, governedHandlerInvocation(map[string]any{
		"amount":          "12.5",
		"idempotency_key": "transport-command-1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if string(handler.input) != `{"amount":12.5}` {
		t.Fatalf("handler input=%s", handler.input)
	}
}

func TestBusinessHandlerBindsDeclaredIdempotencyFieldFromInvocationMetadata(t *testing.T) {
	events := []string{}
	handler := newGovernedHandlerProbe(&events)
	store := &governedExecutionStoreProbe{events: &events}
	service := newGovernedHandlerApplication(t, handler, ActionAuthorization{}, ActionAssurance{}, store)
	entry, ok := service.dependencies.Catalog.Entry("booking.reserve")
	if !ok {
		t.Fatal("missing action entry")
	}
	entry.Definition.PayloadFields = append(entry.Definition.PayloadFields, definitionmodel.ActionPayloadField{Key: "idempotency_key", Type: "string", Required: true})
	service.dependencies.Catalog.entries["booking.reserve"] = entry

	invocation := governedHandlerInvocation(map[string]any{"amount": "12.5"})
	invocation.IdempotencyKey = "header-command-1"
	if _, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, invocation); err != nil {
		t.Fatal(err)
	}
	if string(handler.input) != `{"amount":12.5,"idempotency_key":"header-command-1"}` {
		t.Fatalf("handler input=%s", handler.input)
	}
}

func TestBusinessHandlerEmptyContractReceivesEmptyJSONObject(t *testing.T) {
	events := []string{}
	handler := newGovernedHandlerProbe(&events)
	handler.allowEmpty = true
	store := &governedExecutionStoreProbe{events: &events}
	service := newGovernedHandlerApplication(t, handler, ActionAuthorization{}, ActionAssurance{}, store)
	entry, ok := service.dependencies.Catalog.Entry("booking.reserve")
	if !ok {
		t.Fatal("missing action entry")
	}
	entry.Definition.PayloadFields = nil
	service.dependencies.Catalog.entries["booking.reserve"] = entry

	result, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, governedHandlerInvocation(map[string]any{
		"idempotency_key": "transport-command-1",
	}))
	if err != nil || result.Object == nil {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if string(handler.input) != `{}` {
		t.Fatalf("handler input=%s error=%v", handler.input, err)
	}
}

type governedExecutionStoreProbe struct {
	events   *[]string
	beginErr error
	decision idempotency.Decision
	result   map[string]any
}

type governedExecutionTransaction struct {
	store *governedExecutionStoreProbe
}

func (*governedExecutionTransaction) Context(ctx context.Context) context.Context { return ctx }

func (t *governedExecutionTransaction) Commit(ctx context.Context, commits []transactionmodel.RecordMutationCommit, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	return t.store.CommitExecution(ctx, commits, completion)
}

func (*governedExecutionTransaction) RollBack(context.Context) error { return nil }

func (s *governedExecutionStoreProbe) BeginExecutionTransaction(context.Context) (actioncontract.ActionExecutionTransaction, error) {
	return &governedExecutionTransaction{store: s}, nil
}

func (s *governedExecutionStoreProbe) TryBeginExecution(_ context.Context, request actionmodel.ActionExecutionClaimRequest) (actionmodel.ActionExecutionClaimResult, error) {
	*s.events = append(*s.events, "idempotency")
	if s.beginErr != nil {
		return actionmodel.ActionExecutionClaimResult{}, s.beginErr
	}
	execution := request.Execution
	execution.ID = "execution-1"
	execution.LeaseOwner = request.LeaseOwner
	execution.FencingToken = 1
	execution.Result = s.result
	decision := s.decision
	if decision == "" {
		decision = idempotency.DecisionAcquired
	}
	return actionmodel.ActionExecutionClaimResult{Decision: decision, Execution: execution}, nil
}

func (*governedExecutionStoreProbe) HeartbeatExecution(context.Context, string, string, int64, time.Time, time.Time) error {
	return nil
}

func (s *governedExecutionStoreProbe) CompleteExecution(_ context.Context, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	*s.events = append(*s.events, "complete")
	return completion.Execution, nil
}

func (s *governedExecutionStoreProbe) CommitExecution(_ context.Context, _ []transactionmodel.RecordMutationCommit, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	*s.events = append(*s.events, "commit")
	return completion.Execution, nil
}

func TestBusinessHandlerRunsOnlyAfterGovernedPreflight(t *testing.T) {
	events := []string{}
	handler := newGovernedHandlerProbe(&events)
	store := &governedExecutionStoreProbe{events: &events}
	service := newGovernedHandlerApplication(t, handler, ActionAuthorization{ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
		events = append(events, "permission")
		return definitionmodel.ObjectSchema{Key: "booking"}, nil
	}}, ActionAssurance{Validate: func(_ context.Context, invocation actionmodel.ActionInvocation) (map[string]string, error) {
		events = append(events, "assurance")
		if invocation.Input["amount"] != 12.5 {
			return nil, errors.New("assurance received unnormalized payload")
		}
		return map[string]string{"method": "otp"}, nil
	}}, store)

	result, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, governedHandlerInvocation(map[string]any{"amount": "12.5"}))
	if err != nil || result.Object == nil || handler.invoked != 1 {
		t.Fatalf("result=%+v handler calls=%d error=%v", result, handler.invoked, err)
	}
	want := []string{"permission", "assurance", "idempotency", "handler", "commit"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("governed order=%v want=%v", events, want)
	}
}

func TestBusinessHandlerPreflightFailuresShortCircuitBeforeInvocation(t *testing.T) {
	permissionErr, assuranceErr, idempotencyErr := errors.New("permission failed"), errors.New("assurance failed"), errors.New("idempotency failed")
	tests := []struct {
		name          string
		input         map[string]any
		permissionErr error
		assuranceErr  error
		beginErr      error
		wantEvents    []string
	}{
		{name: "permission", input: map[string]any{"amount": "12.5"}, permissionErr: permissionErr, wantEvents: []string{"permission"}},
		{name: "payload", input: map[string]any{"amount": "12.5", "unknown": true}, wantEvents: []string{"permission"}},
		{name: "assurance", input: map[string]any{"amount": "12.5"}, assuranceErr: assuranceErr, wantEvents: []string{"permission", "assurance"}},
		{name: "idempotency", input: map[string]any{"amount": "12.5"}, beginErr: idempotencyErr, wantEvents: []string{"permission", "assurance", "idempotency"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := []string{}
			handler := newGovernedHandlerProbe(&events)
			store := &governedExecutionStoreProbe{events: &events, beginErr: test.beginErr}
			service := newGovernedHandlerApplication(t, handler, ActionAuthorization{ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
				events = append(events, "permission")
				return definitionmodel.ObjectSchema{Key: "booking"}, test.permissionErr
			}}, ActionAssurance{Validate: func(context.Context, actionmodel.ActionInvocation) (map[string]string, error) {
				events = append(events, "assurance")
				return nil, test.assuranceErr
			}}, store)

			if _, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, governedHandlerInvocation(test.input)); err == nil {
				t.Fatal("preflight failure unexpectedly succeeded")
			}
			if handler.invoked != 0 || !reflect.DeepEqual(events, test.wantEvents) {
				t.Fatalf("events=%v want=%v handler calls=%d", events, test.wantEvents, handler.invoked)
			}
		})
	}
}

func TestBusinessHandlerIdempotentReplayDoesNotInvokeHandler(t *testing.T) {
	events := []string{}
	handler := newGovernedHandlerProbe(&events)
	store := &governedExecutionStoreProbe{events: &events, decision: idempotency.DecisionReplay, result: map[string]any{
		"action_key": "booking.reserve", "object_key": "booking", "status": "success", "output": map[string]any{"accepted": true},
	}}
	service := newGovernedHandlerApplication(t, handler, ActionAuthorization{ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
		events = append(events, "permission")
		return definitionmodel.ObjectSchema{Key: "booking"}, nil
	}}, ActionAssurance{Validate: func(context.Context, actionmodel.ActionInvocation) (map[string]string, error) {
		events = append(events, "assurance")
		return map[string]string{"method": "otp"}, nil
	}}, store)

	result, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, governedHandlerInvocation(map[string]any{"amount": "12.5"}))
	if err != nil || result.Object == nil || result.Object.Message != "backend.action.idempotent_replay" || handler.invoked != 0 {
		t.Fatalf("result=%+v handler calls=%d error=%v", result, handler.invoked, err)
	}
	want := []string{"permission", "assurance", "idempotency"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("replay order=%v want=%v", events, want)
	}
}

func newGovernedHandlerProbe(events *[]string) *governedHandlerProbe {
	return &governedHandlerProbe{events: events, descriptor: actionTestHandlerDescriptor(
		"booking.reserve",
		[]runtimeext.ActionObjectCapability{{ObjectKey: "booking", Operations: []string{"update"}}},
	)}
}

func newGovernedHandlerApplication(t *testing.T, handler runtimeext.BusinessHandler, authorization ActionAuthorization, assurance ActionAssurance, store *governedExecutionStoreProbe) *ActionApplicationService {
	t.Helper()
	registry := runtimeext.NewBusinessHandlerRegistry()
	if err := registry.Register(handler); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	action := actionTestPublishedContract(definitionmodel.ActionSchema{
		Key: "booking.reserve", ObjectKey: "booking", Kind: definitionmodel.ActionKindObjectOperation, RequiresPermission: "booking.reserve",
		PayloadFields: []definitionmodel.ActionPayloadField{{Key: "amount", Type: "number", Required: true}},
	})
	system := NewSystemOperationCatalog()
	return NewActionApplication(ActionApplicationDependencies{
		Catalog: NewActionCatalog([]definitionmodel.ActionSchema{action}, system, registry), SystemOperations: NewSystemOperationExecutor(system),
		BusinessHandlers: newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{}), Authorization: authorization, Assurance: assurance,
		UnitOfWork: NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(store)), NewInvocationID: func(context.Context) string { return "invocation-1" },
	})
}

func governedHandlerInvocation(input map[string]any) actionmodel.ActionInvocation {
	return actionmodel.ActionInvocation{
		ActionKey: "booking.reserve", ObjectKey: "booking", Input: input, IdempotencyKey: "command-1",
		Principal: actionTestPrincipal("booking.reserve"),
	}
}
