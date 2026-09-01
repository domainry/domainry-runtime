package record

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestRecordConditionalArithmeticCoversEveryNumericAndDecimalOutcome(t *testing.T) {
	cases := []struct {
		name      string
		field     definitionmodel.FieldSchema
		current   any
		operation string
		operand   any
		want      any
		code      string
	}{
		{name: "number increment", field: definitionmodel.FieldSchema{Key: "count", Type: "number"}, current: 5, operation: "increment", operand: 2, want: float64(7)},
		{name: "number decrement", field: definitionmodel.FieldSchema{Key: "count", Type: "number"}, current: 5, operation: "decrement", operand: 2, want: float64(3)},
		{name: "percent increment", field: definitionmodel.FieldSchema{Key: "rate", Type: "percent"}, current: "1.5", operation: "increment", operand: "0.5", want: float64(2)},
		{name: "integer increment", field: definitionmodel.FieldSchema{Key: "visits", Type: "integer"}, current: int64(5), operation: "increment", operand: int64(2), want: int64(7)},
		{name: "integer decrement", field: definitionmodel.FieldSchema{Key: "visits", Type: "integer"}, current: int64(5), operation: "decrement", operand: int64(2), want: int64(3)},
		{name: "integer increment overflow", field: definitionmodel.FieldSchema{Key: "visits", Type: "integer"}, current: int64(math.MaxInt64), operation: "increment", operand: int64(1), code: "backend.mutation.arithmetic_overflow"},
		{name: "integer decrement overflow", field: definitionmodel.FieldSchema{Key: "visits", Type: "integer"}, current: int64(math.MinInt64), operation: "decrement", operand: int64(1), code: "backend.mutation.arithmetic_overflow"},
		{name: "integer fractional operand", field: definitionmodel.FieldSchema{Key: "visits", Type: "integer"}, current: int64(5), operation: "increment", operand: 1.5, code: "backend.mutation.arithmetic_operand_invalid"},
		{name: "integer invalid current", field: definitionmodel.FieldSchema{Key: "visits", Type: "integer"}, current: "bad", operation: "increment", operand: int64(1), code: "backend.mutation.arithmetic_operand_invalid"},
		{name: "integer nil current", field: definitionmodel.FieldSchema{Key: "visits", Type: "integer"}, current: nil, operation: "increment", operand: int64(1), code: "backend.mutation.arithmetic_overflow"},
		{name: "integer nil operand", field: definitionmodel.FieldSchema{Key: "visits", Type: "integer"}, current: int64(1), operation: "increment", operand: nil, code: "backend.mutation.arithmetic_overflow"},
		{name: "unsupported operation", field: definitionmodel.FieldSchema{Key: "count", Type: "number"}, current: 5, operation: "multiply", operand: 2, code: "backend.mutation.arithmetic_operation_invalid"},
		{name: "unsupported field", field: definitionmodel.FieldSchema{Key: "name", Type: "text"}, current: "a", operation: "increment", operand: 2, code: "backend.mutation.arithmetic_field_invalid"},
		{name: "invalid left operand", field: definitionmodel.FieldSchema{Key: "count", Type: "number"}, current: "bad", operation: "increment", operand: 2, code: "backend.mutation.arithmetic_operand_invalid"},
		{name: "invalid right operand", field: definitionmodel.FieldSchema{Key: "count", Type: "number"}, current: 2, operation: "increment", operand: "bad", code: "backend.mutation.arithmetic_operand_invalid"},
		{name: "currency increment", field: definitionmodel.FieldSchema{Key: "amount", Type: "currency", Config: map[string]any{"precision": 12, "scale": 2}}, current: "10.25", operation: "increment", operand: "1.50", want: "11.75"},
		{name: "currency decrement", field: definitionmodel.FieldSchema{Key: "amount", Type: "currency", Config: map[string]any{"precision": 12, "scale": 2}}, current: "10.25", operation: "decrement", operand: "1.50", want: "8.75"},
		{name: "invalid currency config", field: definitionmodel.FieldSchema{Key: "amount", Type: "currency", Config: map[string]any{"scale": -1}}, current: "10.25", operation: "increment", operand: "1.50", code: "backend.decimal.scale_invalid"},
		{name: "invalid currency operand", field: definitionmodel.FieldSchema{Key: "amount", Type: "currency"}, current: "10.25", operation: "increment", operand: "bad", code: "backend.decimal.value_invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := recordConditionalArithmetic(tc.field, tc.current, transactionmodel.MutationArithmetic{Operation: tc.operation, Operand: tc.operand})
			if tc.code != "" {
				if apperror.CodeOf(err) != tc.code && !strings.Contains(err.Error(), tc.code) {
					t.Fatalf("error=%v code=%q", err, apperror.CodeOf(err))
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got=%#v err=%v want=%#v", got, err, tc.want)
			}
		})
	}
}

func TestRecordPredicateMatchesCoversEqualityOrderingAndInvalidInputs(t *testing.T) {
	cases := []struct {
		name     string
		actual   any
		expected any
		operator string
		matched  bool
		wantErr  bool
	}{
		{name: "deep equal", actual: 1, expected: 1, operator: "eq", matched: true},
		{name: "string equal", actual: 1, expected: "1", operator: "eq", matched: true},
		{name: "not equal false", actual: 1, expected: "1", operator: "ne", matched: false},
		{name: "not equal true", actual: 1, expected: 2, operator: "ne", matched: true},
		{name: "less", actual: 1, expected: 2, operator: "lt", matched: true},
		{name: "less equal", actual: 2, expected: 2, operator: "lte", matched: true},
		{name: "greater", actual: 3, expected: 2, operator: "gt", matched: true},
		{name: "greater equal", actual: 2, expected: 2, operator: "gte", matched: true},
		{name: "ordered false", actual: 3, expected: 2, operator: "lt", matched: false},
		{name: "invalid left", actual: "bad", expected: 2, operator: "lt", wantErr: true},
		{name: "invalid right", actual: 2, expected: "bad", operator: "lt", wantErr: true},
		{name: "unknown operator", actual: 2, expected: 2, operator: "between", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matched, err := recordPredicateMatches(tc.actual, tc.expected, tc.operator)
			if (err != nil) != tc.wantErr || matched != tc.matched {
				t.Fatalf("matched=%v err=%v", matched, err)
			}
		})
	}
}

func TestRecordPredicateMatchesFieldCoversTypedOrderingEdges(t *testing.T) {
	integer := definitionmodel.FieldSchema{Key: "visits", Type: "integer"}
	currency := definitionmodel.FieldSchema{Key: "amount", Type: "currency", Config: map[string]any{"precision": 12, "scale": 2}}
	cases := []struct {
		name     string
		field    definitionmodel.FieldSchema
		actual   any
		expected any
		operator string
		matched  bool
		wantErr  bool
	}{
		{name: "integer not equal", field: integer, actual: int64(1), expected: int64(2), operator: "ne", matched: true},
		{name: "integer left type invalid", field: integer, actual: 1, expected: int64(2), operator: "lt", wantErr: true},
		{name: "integer right type invalid", field: integer, actual: int64(1), expected: 2, operator: "lt", wantErr: true},
		{name: "integer less", field: integer, actual: int64(1), expected: int64(2), operator: "lt", matched: true},
		{name: "integer equal less equal", field: integer, actual: int64(2), expected: int64(2), operator: "lte", matched: true},
		{name: "integer equal greater equal", field: integer, actual: int64(2), expected: int64(2), operator: "gte", matched: true},
		{name: "integer unsupported", field: integer, actual: int64(2), expected: int64(2), operator: "between", wantErr: true},
		{name: "currency less", field: currency, actual: "1.00", expected: "2.00", operator: "lt", matched: true},
		{name: "currency equal less equal", field: currency, actual: "2.00", expected: "2.00", operator: "lte", matched: true},
		{name: "currency equal greater equal", field: currency, actual: "2.00", expected: "2.00", operator: "gte", matched: true},
		{name: "currency config invalid", field: definitionmodel.FieldSchema{Key: "amount", Type: "currency", Config: map[string]any{"scale": -1}}, actual: "1", expected: "2", operator: "lt", wantErr: true},
		{name: "currency comparison invalid", field: currency, actual: true, expected: "2.00", operator: "lt", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matched, err := recordPredicateMatchesField(tc.field, tc.actual, tc.expected, tc.operator)
			if (err != nil) != tc.wantErr || matched != tc.matched {
				t.Fatalf("matched=%v err=%v", matched, err)
			}
		})
	}
}

func TestRecordCanonicalPredicatesCoversUpdatedAtNormalizationAndFailures(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "counter", Fields: []definitionmodel.FieldSchema{
		{Key: "count", Type: "number"},
		{Key: "amount", Type: "currency", Config: map[string]any{"precision": 19, "scale": 2}},
		{Key: "exact_count", Type: "integer"},
	}}
	record := recordmodel.Record{UpdatedAt: "revision-1", Data: map[string]any{"count": float64(3), "amount": "9007199254740993.01", "exact_count": int64(math.MaxInt64)}}

	got, err := recordCanonicalPredicates(object, record, []transactionmodel.MutationPredicate{
		{Field: " updated_at ", Operator: " eq ", Value: "revision-1"},
		{Field: " count ", Operator: "gte", Value: 3},
		{Field: "amount", Operator: "gt", Value: "9007199254740993.00"},
		{Field: "exact_count", Operator: "gt", Value: int64(math.MaxInt64 - 1)},
	})
	if err != nil || len(got) != 4 || got[0].Field != "updated_at" || got[1].Field != "count" {
		t.Fatalf("predicates=%#v err=%v", got, err)
	}

	cases := []struct {
		name       string
		predicate  transactionmodel.MutationPredicate
		wantCode   string
		wantParams map[string]string
	}{
		{name: "unknown field", predicate: transactionmodel.MutationPredicate{Field: "missing", Operator: "eq", Value: 1}, wantCode: "backend.mutation.predicate_invalid"},
		{name: "normalization error", predicate: transactionmodel.MutationPredicate{Field: "amount", Operator: "eq", Value: true}, wantCode: "backend.decimal.value_invalid"},
		{name: "invalid operator", predicate: transactionmodel.MutationPredicate{Field: "count", Operator: "contains", Value: 3}, wantCode: "backend.mutation.predicate_invalid"},
		{name: "default conflict", predicate: transactionmodel.MutationPredicate{Field: "count", Operator: "eq", Value: 4}, wantCode: "backend.mutation.predicate_failed"},
		{name: "custom conflict", predicate: transactionmodel.MutationPredicate{Field: "count", Operator: "eq", Value: 4, ErrorCode: " business.counter_changed "}, wantCode: "business.counter_changed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := recordCanonicalPredicates(object, record, []transactionmodel.MutationPredicate{tc.predicate})
			if apperror.CodeOf(err) != tc.wantCode && !strings.Contains(err.Error(), tc.wantCode) {
				t.Fatalf("error=%v code=%q", err, apperror.CodeOf(err))
			}
		})
	}

	badStoredRecord := recordmodel.Record{Data: map[string]any{"amount": true}}
	if _, err := recordCanonicalPredicates(object, badStoredRecord, []transactionmodel.MutationPredicate{{Field: "amount", Operator: "eq", Value: "1.00"}}); apperror.CodeOf(err) != "backend.decimal.value_invalid" && !strings.Contains(err.Error(), "backend.decimal.value_invalid") {
		t.Fatalf("stored normalization err=%v", err)
	}
}

func TestPlanConditionalUpdateMutationCoversAuthorizationLookupScopeAndPlanning(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "counter", Fields: []definitionmodel.FieldSchema{{Key: "count", Type: "number"}, {Key: "rate", Type: "percent"}, {Key: "visits", Type: "integer"}}}
	record := recordmodel.Record{ID: "counter-1", UpdatedAt: "revision-1", Data: map[string]any{"count": float64(3), "rate": float64(1), "visits": int64(5)}}
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	newService := func(repository *updateRepositoryProbe, objectForAction func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error), canAccess func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool) *RecordUpdateApplicationService {
		return NewRecordUpdateApplicationService(RecordUpdateDependencies{Repository: repository, ObjectForAction: objectForAction, CanAccess: canAccess, CanWrite: func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true }})
	}
	objectForAction := func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
		return object, nil
	}

	repository := &updateRepositoryProbe{found: true, record: record}
	service := newService(repository, objectForAction, nil)
	plan, updated, err := service.PlanConditionalUpdateMutation(t.Context(), object.Key, record.ID, transactionmodel.ConditionalUpdateInput{
		Predicates: []transactionmodel.MutationPredicate{{Field: "count", Operator: "eq", Value: 3}},
		Patch:      map[string]any{"rate": 2},
		Arithmetic: []transactionmodel.MutationArithmetic{{Field: "count", Operation: "decrement", Operand: 1}, {Field: "visits", Operation: "increment", Operand: 2}},
	}, principal)
	if err != nil || updated.Data["count"] != float64(2) || updated.Data["rate"] != "2.00" || updated.Data["visits"] != int64(7) || len(plan.WriteSet()) != 1 || len(plan.CanonicalCommit().Predicates) != 3 {
		t.Fatalf("plan=%#v updated=%#v err=%v", plan, updated, err)
	}
	facade := &RecordApplicationService{update: service}
	if _, _, err := facade.PlanConditionalUpdateMutation(t.Context(), object.Key, record.ID, transactionmodel.ConditionalUpdateInput{Patch: map[string]any{"rate": 3}}, principal); err != nil {
		t.Fatalf("facade plan err=%v", err)
	}
	if _, _, err := service.PlanConditionalUpdateMutation(t.Context(), object.Key, record.ID, transactionmodel.ConditionalUpdateInput{Predicates: []transactionmodel.MutationPredicate{{Field: "missing", Operator: "eq", Value: 3}}}, principal); apperror.CodeOf(err) != "backend.mutation.predicate_invalid" {
		t.Fatalf("predicate err=%v", err)
	}

	if _, _, err := service.PlanConditionalUpdateMutation(t.Context(), object.Key, record.ID, transactionmodel.ConditionalUpdateInput{}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization err=%v", err)
	}
	lookupFailure := errors.New("lookup failed")
	service = newService(repository, func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
		return definitionmodel.ObjectSchema{}, lookupFailure
	}, nil)
	if _, _, err := service.PlanConditionalUpdateMutation(t.Context(), object.Key, record.ID, transactionmodel.ConditionalUpdateInput{}, principal); !errors.Is(err, lookupFailure) {
		t.Fatalf("lookup err=%v", err)
	}
	timerObject := object
	timerObject.Key = "record_timer"
	timerObject.Config = map[string]any{"record_timer_runtime": true}
	service = newService(repository, func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
		return timerObject, nil
	}, nil)
	if _, _, err := service.PlanConditionalUpdateMutation(t.Context(), timerObject.Key, record.ID, transactionmodel.ConditionalUpdateInput{}, principal); apperror.CodeOf(err) != "backend.record_timer.runtime_api_required" {
		t.Fatalf("record timer err=%v", err)
	}

	repository = &updateRepositoryProbe{err: errors.New("store unavailable")}
	service = newService(repository, objectForAction, nil)
	if _, _, err := service.PlanConditionalUpdateMutation(t.Context(), object.Key, record.ID, transactionmodel.ConditionalUpdateInput{}, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("repository err=%v", err)
	}
	repository = &updateRepositoryProbe{}
	service = newService(repository, objectForAction, nil)
	if _, _, err := service.PlanConditionalUpdateMutation(t.Context(), object.Key, record.ID, transactionmodel.ConditionalUpdateInput{}, principal); apperror.CodeOf(err) != "backend.record.not_found" {
		t.Fatalf("not found err=%v", err)
	}
	repository = &updateRepositoryProbe{found: true, record: record}
	service = newService(repository, objectForAction, func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return false })
	if _, _, err := service.PlanConditionalUpdateMutation(t.Context(), object.Key, record.ID, transactionmodel.ConditionalUpdateInput{}, principal); apperror.CodeOf(err) != "backend.record.outside_scope" {
		t.Fatalf("scope err=%v", err)
	}
	scopeFailure := errors.New("scope unavailable")
	service = NewRecordUpdateApplicationService(RecordUpdateDependencies{
		Repository: repository, ObjectForAction: objectForAction,
		CanAccessScope: func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record, bool) (bool, error) {
			return false, scopeFailure
		},
	})
	if _, _, err := service.PlanConditionalUpdateMutation(t.Context(), object.Key, record.ID, transactionmodel.ConditionalUpdateInput{}, principal); !errors.Is(err, scopeFailure) {
		t.Fatalf("scope failure err=%v", err)
	}

	service = newService(repository, objectForAction, func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true })
	if _, _, err := service.PlanConditionalUpdateMutation(t.Context(), object.Key, record.ID, transactionmodel.ConditionalUpdateInput{Arithmetic: []transactionmodel.MutationArithmetic{{Field: "missing", Operation: "increment", Operand: 1}}}, principal); apperror.CodeOf(err) != "backend.mutation.arithmetic_field_invalid" {
		t.Fatalf("field err=%v", err)
	}
	if _, _, err := service.PlanConditionalUpdateMutation(t.Context(), object.Key, record.ID, transactionmodel.ConditionalUpdateInput{Arithmetic: []transactionmodel.MutationArithmetic{{Field: "count", Operation: "multiply", Operand: 1}}}, principal); apperror.CodeOf(err) != "backend.mutation.arithmetic_operation_invalid" {
		t.Fatalf("arithmetic err=%v", err)
	}
	service = NewRecordUpdateApplicationService(RecordUpdateDependencies{Repository: repository, ObjectForAction: objectForAction, CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true }, CanWrite: func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return false }})
	if _, _, err := service.PlanConditionalUpdateMutation(t.Context(), object.Key, record.ID, transactionmodel.ConditionalUpdateInput{Patch: map[string]any{"rate": 2}}, principal); apperror.CodeOf(err) != "backend.record.owner_write_denied" {
		t.Fatalf("planning err=%v", err)
	}
	var scopeChecks []bool
	service = NewRecordUpdateApplicationService(RecordUpdateDependencies{
		Repository: repository, ObjectForAction: objectForAction,
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return false },
		CanWrite:  func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return false },
		CanAccessScope: func(_ context.Context, _ principalmodel.Principal, _ definitionmodel.ObjectSchema, _ recordmodel.Record, write bool) (bool, error) {
			scopeChecks = append(scopeChecks, write)
			return true, nil
		},
	})
	if _, _, err := service.PlanConditionalUpdateMutation(t.Context(), object.Key, record.ID, transactionmodel.ConditionalUpdateInput{Patch: map[string]any{"rate": 2}}, principal); err != nil || !reflect.DeepEqual(scopeChecks, []bool{false, true}) {
		t.Fatalf("contextual scope checks=%v err=%v", scopeChecks, err)
	}
}

func TestConditionalUpdateCoversLifecycleErrorsAndWorkflow(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "counter", Fields: []definitionmodel.FieldSchema{{Key: "count", Type: "number"}}}
	record := recordmodel.Record{ID: "counter-1", UpdatedAt: "revision-1", Data: map[string]any{"count": float64(3)}}
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	input := transactionmodel.ConditionalUpdateInput{Patch: map[string]any{"count": 4}}
	newService := func(repository *updateRepositoryProbe, executeWorkflow func(context.Context, []workflowmodel.WorkflowExecution, principalmodel.Principal)) *RecordUpdateApplicationService {
		return NewRecordUpdateApplicationService(RecordUpdateDependencies{
			Repository: repository,
			ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
				return object, nil
			},
			CanAccess:       func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
			CanWrite:        func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
			ExecuteWorkflow: executeWorkflow,
		})
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := newService(&updateRepositoryProbe{}, nil).ConditionalUpdate(cancelled, object.Key, record.ID, input, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancel err=%v", err)
	}
	if _, err := newService(&updateRepositoryProbe{}, nil).ConditionalUpdate(t.Context(), object.Key, record.ID, input, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("plan err=%v", err)
	}
	commitFailure := errors.New("commit unavailable")
	if _, err := newService(&updateRepositoryProbe{found: true, record: record, commitErr: commitFailure}, nil).ConditionalUpdate(t.Context(), object.Key, record.ID, input, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("commit err=%v", err)
	}

	postCommit, cancelPostCommit := context.WithCancel(t.Context())
	repository := &updateRepositoryProbe{found: true, record: record, commitHook: cancelPostCommit}
	if _, err := newService(repository, nil).ConditionalUpdate(postCommit, object.Key, record.ID, input, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("post-commit err=%v", err)
	}
	workflowCalls := 0
	repository = &updateRepositoryProbe{found: true, record: record}
	updated, err := newService(repository, func(context.Context, []workflowmodel.WorkflowExecution, principalmodel.Principal) {
		workflowCalls++
	}).ConditionalUpdate(t.Context(), object.Key, record.ID, input, principal)
	if err != nil || updated.Data["count"] != float64(4) || workflowCalls != 1 {
		t.Fatalf("updated=%#v workflowCalls=%d err=%v", updated, workflowCalls, err)
	}
	repository = &updateRepositoryProbe{found: true, record: record}
	if _, err := newService(repository, nil).ConditionalUpdate(t.Context(), object.Key, record.ID, input, principal); err != nil {
		t.Fatalf("nil workflow callback err=%v", err)
	}
}

func TestRecordApplicationPlanMutationFacadesDelegate(t *testing.T) {
	deleteFacade := &RecordApplicationService{delete: NewRecordDeleteApplicationService(RecordDeleteDependencies{})}
	if _, err := deleteFacade.PlanDeleteMutation(t.Context(), "customer", "customer-1", "", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("delete facade err=%v", err)
	}
	restoreFacade := &RecordApplicationService{restore: NewRecordRestoreApplicationService(RecordRestoreDependencies{})}
	if _, _, err := restoreFacade.PlanRestoreMutation(t.Context(), "customer", "customer-1", "", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("restore facade err=%v", err)
	}
}
