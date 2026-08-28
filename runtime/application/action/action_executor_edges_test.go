package action

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
)

type actionExecutorEdgeHandler struct {
	descriptor runtimeext.HandlerDescriptor
	output     json.RawMessage
	err        error
	invoke     func(context.Context, runtimeext.ActionExecution) error
}

func (h actionExecutorEdgeHandler) Descriptor() runtimeext.HandlerDescriptor { return h.descriptor }

func (h actionExecutorEdgeHandler) Invoke(ctx context.Context, execution runtimeext.ActionExecution, _ json.RawMessage) (json.RawMessage, error) {
	if h.invoke != nil {
		if err := h.invoke(ctx, execution); err != nil {
			return nil, err
		}
	}
	if h.err != nil {
		return nil, h.err
	}
	if h.output == nil {
		return json.RawMessage(`{}`), nil
	}
	return h.output, nil
}

func actionExecutorEdgeGoverned(handler runtimeext.BusinessHandler, action definitionmodel.ActionSchema) governedActionExecution {
	return governedActionExecution{
		invocation: actionmodel.ActionInvocation{
			ActionKey: action.Key, ObjectKey: action.ObjectKey, Principal: actionTestPrincipal(action.Key),
		},
		entry: ActionCatalogEntry{
			Definition: action,
			HandlerBinding: runtimeext.BusinessHandlerBinding{
				Descriptor: handler.Descriptor(),
				Handler:    handler,
			},
		},
		executionID: "execution-edge",
		unitOfWork:  newActionTestUnitOfWork(),
	}
}

func TestBusinessHandlerExecutorRejectsInvalidGovernedInputs(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "booking.reserve", ObjectKey: "booking"}
	handler := actionExecutorEdgeHandler{descriptor: actionTestHandlerDescriptor(action.Key, nil)}
	governed := actionExecutorEdgeGoverned(handler, action)

	tests := []struct {
		name     string
		executor *BusinessHandlerExecutor
		mutate   func(*governedActionExecution)
		code     string
	}{
		{name: "nil executor", executor: nil, code: "backend.action.business_handler_executor_required"},
		{name: "nil handler", executor: newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{}), mutate: func(g *governedActionExecution) { g.entry.HandlerBinding.Handler = nil }, code: "backend.action.business_handler_executor_required"},
		{name: "descriptor mismatch", executor: newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{}), mutate: func(g *governedActionExecution) { g.entry.HandlerBinding.Descriptor.ActionKey = "other" }, code: "backend.action.handler_contract_mismatch"},
		{name: "payload marshal", executor: newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{}), mutate: func(g *governedActionExecution) {
			g.entry.Definition.PayloadFields = []definitionmodel.ActionPayloadField{{Key: "bad"}}
			g.payload = map[string]any{"bad": func() {}}
		}, code: "backend.action.payload_invalid"},
		{name: "unit of work", executor: newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{}), mutate: func(g *governedActionExecution) { g.unitOfWork = nil }, code: "backend.action.execution_phase_required"},
		{name: "identity", executor: NewBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{}), code: "backend.action.execution_identity_incomplete"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := governed
			if test.mutate != nil {
				test.mutate(&input)
			}
			_, err := test.executor.execute(t.Context(), input)
			if got := apperror.CodeOf(err); got != test.code {
				t.Fatalf("code=%q want=%q error=%v", got, test.code, err)
			}
		})
	}
}

func TestBusinessHandlerExecutorPreservesClassifiedHandlerErrors(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "booking.reserve", ObjectKey: "booking"}
	tests := []struct {
		name string
		err  error
	}{
		{name: "business conflict", err: mutation.BusinessConflict("booking.full", "booking", "1", "status")},
		{name: "mutation conflict", err: mutation.MutationConflict("booking", "1", mutation.MutationConflictOptimistic, errors.New("changed"))},
		{name: "transient", err: mutation.TransactionTransient("booking", "1", mutation.TransactionTransientDeadlock, errors.New("deadlock"))},
		{name: "commit unknown", err: mutation.TransactionCommitUnknown("booking", "1", errors.New("lost response"))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := actionExecutorEdgeHandler{descriptor: actionTestHandlerDescriptor(action.Key, nil), err: test.err}
			_, err := newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{}).execute(t.Context(), actionExecutorEdgeGoverned(handler, action))
			if !errors.Is(err, test.err) {
				t.Fatalf("error=%v want identity with %v", err, test.err)
			}
		})
	}

	for _, businessError := range []*runtimeext.BusinessError{
		{Code: ""},
		{Code: "booking.invalid", Message: "invalid"},
	} {
		handler := actionExecutorEdgeHandler{descriptor: actionTestHandlerDescriptor(action.Key, nil), err: businessError}
		_, err := newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{}).execute(t.Context(), actionExecutorEdgeGoverned(handler, action))
		if businessError.Valid() && apperror.CodeOf(err) != businessError.Code {
			t.Fatalf("error=%v", err)
		}
		if !businessError.Valid() && apperror.CodeOf(err) != "backend.action.handler_failed" {
			t.Fatalf("invalid business error=%v", err)
		}
	}
}

func TestBusinessHandlerExecutorRecordAndCanonicalCommitEdges(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "booking.reserve", ObjectKey: "booking"}
	handler := actionExecutorEdgeHandler{descriptor: actionTestHandlerDescriptor(action.Key, nil), output: json.RawMessage(`{"booking_id":"booking-output-1"}`)}
	governed := actionExecutorEdgeGoverned(handler, action)
	governed.invocation.RecordID = "booking-1"

	_, err := newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{}).execute(t.Context(), governed)
	if apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("missing get error=%v", err)
	}

	loadErr := errors.New("load failed")
	executor := newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{
		GetRecord: func(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error) {
			return recordmodel.Record{}, loadErr
		},
	})
	if _, err := executor.execute(t.Context(), governed); !errors.Is(err, loadErr) {
		t.Fatalf("load error=%v", err)
	}

	executor = newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{
		GetRecord: func(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error) {
			return recordmodel.Record{ID: "booking-1"}, nil
		},
	})
	result, err := executor.execute(t.Context(), governed)
	if err != nil || result.Record == nil || result.Record.Record.ID != "booking-1" || result.Record.Output["booking_id"] != "booking-output-1" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	handler = actionExecutorEdgeHandler{
		descriptor: actionTestHandlerDescriptor(action.Key, nil),
		invoke: func(_ context.Context, execution runtimeext.ActionExecution) error {
			session := execution.(*businessActionExecution)
			session.mutatedRecords = map[string]recordmodel.Record{"booking\x00booking-1": {ID: "booking-1"}}
			_ = session.Workspace()
			return nil
		},
	}
	governed = actionExecutorEdgeGoverned(handler, action)
	governed.invocation.RecordID = "booking-1"
	result, err = newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{}).execute(t.Context(), governed)
	if err != nil || result.Record == nil {
		t.Fatalf("mutated result=%+v error=%v", result, err)
	}

	handler = actionExecutorEdgeHandler{
		descriptor: actionTestHandlerDescriptor(action.Key, nil),
		invoke: func(_ context.Context, execution runtimeext.ActionExecution) error {
			session := execution.(*businessActionExecution)
			session.mutatedRecords = map[string]recordmodel.Record{"booking\x00booking-internal": {ID: "booking-internal"}}
			return nil
		},
	}
	governed = actionExecutorEdgeGoverned(handler, action)
	governed.invocation.RecordID = "merchant-booking-number"
	result, err = newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{}).execute(t.Context(), governed)
	if err != nil || result.Record == nil || result.Record.RecordID != "booking-internal" || result.Record.Record.ID != "booking-internal" {
		t.Fatalf("business-key target result=%+v error=%v", result, err)
	}

	handler = actionExecutorEdgeHandler{
		descriptor: actionTestHandlerDescriptor(action.Key, nil),
		invoke: func(_ context.Context, execution runtimeext.ActionExecution) error {
			session := execution.(*businessActionExecution)
			session.observedRecords = map[string]recordmodel.Record{"booking\x00booking-internal": {ID: "booking-internal"}}
			return nil
		},
	}
	governed = actionExecutorEdgeGoverned(handler, action)
	governed.invocation.RecordID = "merchant-booking-number"
	result, err = newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{}).execute(t.Context(), governed)
	if err != nil || result.Record == nil || result.Record.RecordID != "booking-internal" || result.Record.Record.ID != "booking-internal" {
		t.Fatalf("observed business-key target result=%+v error=%v", result, err)
	}

	intent := runtimeext.DurableIntent{
		ConsumerKey: "email", ConnectionKey: "primary", OperationKey: "send",
		ContractSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	grant := runtimeext.ActionConnectorCapability{
		ConnectorKey: intent.ConsumerKey, ConnectionKey: intent.ConnectionKey, OperationKey: intent.OperationKey,
		ContractSHA256: intent.ContractSHA256, Mode: runtimeext.ConnectorModeEnqueue, Effect: runtimeext.ConnectorEffectWrite,
	}
	handler = actionExecutorEdgeHandler{
		descriptor: actionTestHandlerDescriptor(action.Key, nil, grant),
		invoke: func(ctx context.Context, execution runtimeext.ActionExecution) error {
			_, err := execution.StageDurableIntent(ctx, intent)
			return err
		},
	}
	governed = actionExecutorEdgeGoverned(handler, action)
	executor = newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{
		ValidateDurableIntent: func(context.Context, runtimeext.DurableIntent, principalmodel.Principal) error { return nil },
	})
	if _, err := executor.execute(t.Context(), governed); apperror.CodeOf(err) != "backend.action.durable_intent_requires_business_mutation" {
		t.Fatalf("canonical commit error=%v", err)
	}
}

func TestBusinessActionQueryEdges(t *testing.T) {
	readAction := definitionmodel.ActionSchema{EffectSet: &definitionmodel.ActionEffectSet{Read: []definitionmodel.ActionObjectEffect{{ObjectKey: "booking"}}}}
	execution := &businessActionExecution{action: readAction, unitOfWork: newActionTestUnitOfWork()}
	if _, err := execution.QueryRecords(t.Context(), runtimeext.RecordQuery{}); apperror.CodeOf(err) != "backend.action.query_invalid" {
		t.Fatalf("invalid query error=%v", err)
	}
	if _, err := execution.QueryRecords(t.Context(), runtimeext.RecordQuery{Operation: runtimeext.QueryGet, ObjectKey: "other", RecordID: "1"}); apperror.CodeOf(err) != "backend.action.effect_authority_denied" {
		t.Fatalf("authority error=%v", err)
	}
	if _, err := execution.QueryRecords(t.Context(), runtimeext.RecordQuery{Operation: runtimeext.QueryGet, ObjectKey: "booking", RecordID: "1"}); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("get port error=%v", err)
	}

	loadErr := errors.New("load failed")
	execution.dependencies.GetRecord = func(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error) {
		return recordmodel.Record{}, loadErr
	}
	if _, err := execution.QueryRecords(t.Context(), runtimeext.RecordQuery{Operation: runtimeext.QueryGet, ObjectKey: "booking", RecordID: "1"}); !errors.Is(err, loadErr) {
		t.Fatalf("get error=%v", err)
	}
	if _, err := execution.QueryRecords(t.Context(), runtimeext.RecordQuery{Operation: runtimeext.QueryGetForUpdate, ObjectKey: "booking", RecordID: "1"}); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("update port error=%v", err)
	}

	execution.dependencies.ListRecords = nil
	if _, err := execution.QueryRecords(t.Context(), runtimeext.RecordQuery{Operation: runtimeext.QueryList, ObjectKey: "booking"}); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("list port error=%v", err)
	}
	execution.dependencies.ListRecords = func(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{}, loadErr
	}
	for _, operation := range []runtimeext.QueryOperation{runtimeext.QueryExists, runtimeext.QueryCount} {
		if _, err := execution.QueryRecords(t.Context(), runtimeext.RecordQuery{Operation: operation, ObjectKey: "booking"}); !errors.Is(err, loadErr) {
			t.Fatalf("%s error=%v", operation, err)
		}
	}
	execution.dependencies.ListRecords = func(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{Total: 2}, nil
	}
	if result, err := execution.QueryRecords(t.Context(), runtimeext.RecordQuery{Operation: runtimeext.QueryCount, ObjectKey: "booking"}); err != nil || result.Count != 2 || result.Records != nil {
		t.Fatalf("count result=%+v error=%v", result, err)
	}
	for _, sort := range []runtimeext.Sort{{Field: "", Direction: "asc"}, {Field: "name", Direction: "asc"}} {
		execution.dependencies.ListRecords = func(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error) {
			return recordmodel.RecordPageResult{}, nil
		}
		result, err := execution.QueryRecords(t.Context(), runtimeext.RecordQuery{Operation: runtimeext.QueryList, ObjectKey: "booking", Sorts: []runtimeext.Sort{sort}})
		if sort.Field == "" && apperror.CodeOf(err) != "backend.action.query_sort_invalid" {
			t.Fatalf("sort error=%v", err)
		}
		if sort.Field != "" && (err != nil || result.Records == nil) {
			t.Fatalf("list result=%+v error=%v", result, err)
		}
	}
}

func TestActionRecordFilterNodeConditionEdges(t *testing.T) {
	valid := []runtimeext.Filter{
		{Field: "status", Value: "open"},
		{Field: "status", Operator: "in", Values: []any{"open"}},
		{Field: "status", Operator: "is_null"},
		{Operator: "and", Children: []runtimeext.Filter{{Field: "a", Value: 1}, {Field: "b", Value: 2}}},
		{Operator: "not", Children: []runtimeext.Filter{{Field: "a", Value: 1}}},
	}
	for _, filter := range valid {
		if _, err := actionRecordFilterNode(filter); err != nil {
			t.Fatalf("valid filter=%+v error=%v", filter, err)
		}
	}
	if expression, err := actionRecordFilterExpression([]runtimeext.Filter{{Field: "status", Value: "open"}}); err != nil || expression.Operator != "eq" {
		t.Fatalf("single expression=%+v error=%v", expression, err)
	}
	invalid := []runtimeext.Filter{
		{Field: "x", Operator: "and", Children: []runtimeext.Filter{{Field: "a", Value: 1}, {Field: "b", Value: 2}}},
		{Operator: "and", Value: 1, Children: []runtimeext.Filter{{Field: "a", Value: 1}, {Field: "b", Value: 2}}},
		{Operator: "and", Values: []any{1}, Children: []runtimeext.Filter{{Field: "a", Value: 1}, {Field: "b", Value: 2}}},
		{Operator: "not", Children: []runtimeext.Filter{{Field: "a", Value: 1}, {Field: "b", Value: 2}}},
		{Field: "x", Operator: "not", Children: []runtimeext.Filter{{Field: "a", Value: 1}}},
		{Operator: "not", Value: 1, Children: []runtimeext.Filter{{Field: "a", Value: 1}}},
		{Operator: "not", Values: []any{1}, Children: []runtimeext.Filter{{Field: "a", Value: 1}}},
		{Operator: "eq"},
		{Field: "x", Operator: "eq"},
		{Field: "x", Operator: "eq", Value: 1, Values: []any{1}},
		{Field: "x", Operator: "eq", Value: 1, Children: []runtimeext.Filter{{Field: "a", Value: 1}}},
		{Operator: "in", Values: []any{1}},
		{Field: "x", Operator: "in", Value: 1, Values: []any{1}},
		{Field: "x", Operator: "in", Values: []any{1}, Children: []runtimeext.Filter{{Field: "a", Value: 1}}},
		{Operator: "is_null"},
		{Field: "x", Operator: "is_null", Value: 1},
		{Field: "x", Operator: "is_null", Values: []any{1}},
		{Field: "x", Operator: "is_null", Children: []runtimeext.Filter{{Field: "a", Value: 1}}},
		{Operator: "and", Children: []runtimeext.Filter{{Field: "a", Operator: "unsupported"}, {Field: "b", Value: 2}}},
	}
	for _, filter := range invalid {
		_, err := actionRecordFilterNode(filter)
		if code := apperror.CodeOf(err); code != "backend.action.query_filter_invalid" && code != "backend.action.query_operator_unsupported" {
			t.Fatalf("invalid filter=%+v error=%v", filter, err)
		}
	}
	if !actionEffectAllows(&definitionmodel.ActionEffectSet{Write: []definitionmodel.ActionObjectEffect{{ObjectKey: " booking "}}}, "booking", true) ||
		actionEffectAllows(nil, "booking", false) {
		t.Fatal("effect matching failed")
	}
	if authority := actionEffectAuthority(nil); len(authority) != 0 {
		t.Fatalf("authority=%v", authority)
	}
}
