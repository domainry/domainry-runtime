package action

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestSystemOperationExecutorValidationAndExecutionEdges(t *testing.T) {
	handler := func(context.Context, actionmodel.ActionInvocation, definitionmodel.ActionSchema, map[string]any) (ActionExecutionResult, error) {
		return ActionExecutionResult{Record: &actionmodel.ActionResult{RecordID: "done"}}, nil
	}
	nilCatalog := NewSystemOperationExecutor(nil,
		SystemOperationBinding{},
		SystemOperationBinding{Key: "orphan", Handler: nil},
		SystemOperationBinding{Key: "orphan", Handler: handler},
		SystemOperationBinding{Key: "orphan", Handler: handler},
	)
	if len(nilCatalog.ValidationErrors()) < 3 {
		t.Fatalf("nil catalog errors=%v", nilCatalog.ValidationErrors())
	}
	invalidCatalog := NewSystemOperationExecutor(NewSystemOperationCatalog(SystemOperationDescriptor{}))
	if len(invalidCatalog.ValidationErrors()) == 0 {
		t.Fatal("invalid catalog errors were dropped")
	}
	catalog := NewSystemOperationCatalog(
		SystemOperationDescriptor{Key: "bound", Kind: "bound", WriteOperation: "update"},
		SystemOperationDescriptor{Key: "missing", Kind: "missing", WriteOperation: "delete"},
	)
	executor := NewSystemOperationExecutor(catalog, SystemOperationBinding{Key: "bound", Handler: handler})
	if len(executor.ValidationErrors()) != 1 {
		t.Fatalf("executor errors=%v", executor.ValidationErrors())
	}
	var nilExecutor *SystemOperationExecutor
	if len(nilExecutor.ValidationErrors()) != 1 {
		t.Fatal("nil executor validation changed")
	}
	governed := governedActionExecution{entry: ActionCatalogEntry{SystemOperation: "bound"}}
	if _, err := nilExecutor.execute(t.Context(), governed); err == nil {
		t.Fatal("nil executor executed")
	}
	if _, err := executor.execute(t.Context(), governed); err == nil {
		t.Fatal("invalid executor executed")
	}
	valid := NewSystemOperationExecutor(NewSystemOperationCatalog(
		SystemOperationDescriptor{Key: "bound", Kind: "bound", WriteOperation: "update"},
	), SystemOperationBinding{Key: "bound", Handler: handler})
	result, err := valid.execute(t.Context(), governed)
	if err != nil || result.Record == nil || result.Record.RecordID != "done" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if _, err := valid.execute(t.Context(), governedActionExecution{entry: ActionCatalogEntry{SystemOperation: "unknown"}}); err == nil {
		t.Fatal("unknown system operation executed")
	}
}

func TestRecordSystemOperationHandlerMissingDependencies(t *testing.T) {
	handlers := NewRecordSystemOperationHandlers(RecordSystemOperationDependencies{})
	invocation := actionmodel.ActionInvocation{RecordID: "record"}
	action := definitionmodel.ActionSchema{Key: "object.action", ObjectKey: "object"}
	for name, handler := range map[string]SystemOperationHandler{
		"create": handlers.Create, "update": handlers.Update, "delete": handlers.Delete,
		"restore": handlers.Restore, "transition": handlers.Transition,
		"conditional": handlers.ConditionalUpdate,
	} {
		if _, err := handler(t.Context(), invocation, action, nil); err == nil {
			t.Fatalf("%s missing dependency accepted", name)
		}
	}
	conditionalOnlyObjectLookup := NewRecordSystemOperationHandlers(RecordSystemOperationDependencies{
		ObjectForKey: func(string) (definitionmodel.ObjectSchema, bool) {
			return definitionmodel.ObjectSchema{}, true
		},
	})
	if _, err := conditionalOnlyObjectLookup.ConditionalUpdate(t.Context(), invocation, action, nil); err == nil {
		t.Fatal("conditional update without planner accepted")
	}
}

func TestRecordSystemOperationHandlerSuccessAndFailureEdges(t *testing.T) {
	wantErr := errors.New("mutation failed")
	fail := false
	var conditional transactionmodel.ConditionalUpdateInput
	dependencies := RecordSystemOperationDependencies{
		ObjectForKey: func(key string) (definitionmodel.ObjectSchema, bool) {
			if key == "missing" {
				return definitionmodel.ObjectSchema{}, false
			}
			return definitionmodel.ObjectSchema{Key: key, Fields: []definitionmodel.FieldSchema{{Key: "name"}}}, true
		},
		PlanCreateMutation: func(context.Context, string, map[string]any, string, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
			if fail {
				return transactionmodel.MutationPlan{}, recordmodel.Record{}, wantErr
			}
			return transactionmodel.MutationPlan{}, recordmodel.Record{ID: "created"}, nil
		},
		PlanUpdateMutation: func(context.Context, string, string, map[string]any, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
			if fail {
				return transactionmodel.MutationPlan{}, recordmodel.Record{}, wantErr
			}
			return transactionmodel.MutationPlan{}, recordmodel.Record{ID: "updated"}, nil
		},
		PlanDeleteMutation: func(context.Context, string, string, string, principalmodel.Principal) ([]transactionmodel.MutationPlan, error) {
			if fail {
				return nil, wantErr
			}
			return []transactionmodel.MutationPlan{{}, {}}, nil
		},
		PlanRestoreMutation: func(context.Context, string, string, string, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
			if fail {
				return transactionmodel.MutationPlan{}, recordmodel.Record{}, wantErr
			}
			return transactionmodel.MutationPlan{}, recordmodel.Record{ID: "restored"}, nil
		},
		PlanConditionalUpdate: func(_ context.Context, _ string, _ string, input transactionmodel.ConditionalUpdateInput, _ principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
			conditional = input
			if fail {
				return transactionmodel.MutationPlan{}, recordmodel.Record{}, wantErr
			}
			return transactionmodel.MutationPlan{}, recordmodel.Record{ID: "conditional"}, nil
		},
	}
	handlers := NewRecordSystemOperationHandlers(dependencies)
	invocation := actionmodel.ActionInvocation{RecordID: "record", Principal: principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}}
	action := definitionmodel.ActionSchema{Key: "object.action", ObjectKey: "object"}
	cases := []struct {
		name    string
		handler SystemOperationHandler
		payload map[string]any
	}{
		{"create", handlers.Create, map[string]any{"name": "created"}},
		{"update", handlers.Update, map[string]any{"name": "updated"}},
		{"delete", handlers.Delete, nil},
		{"restore", handlers.Restore, nil},
		{"transition", handlers.Transition, map[string]any{"name": "transitioned"}},
		{"conditional", handlers.ConditionalUpdate, map[string]any{"name": "conditional", "expected_version": 3, "expected_updated_at": "2026-07-26T00:00:00Z"}},
	}
	for _, test := range cases {
		result, err := test.handler(t.Context(), invocation, action, test.payload)
		if err != nil || result.Record == nil && result.Object == nil || len(result.Commits) == 0 {
			t.Fatalf("%s result=%+v error=%v", test.name, result, err)
		}
		fail = true
		if _, err := test.handler(t.Context(), invocation, action, test.payload); err != wantErr {
			t.Fatalf("%s failure=%v", test.name, err)
		}
		fail = false
	}
	if conditional.Patch["expected_version"] != 3 || conditional.Patch["expected_updated_at"] == nil {
		t.Fatalf("conditional input=%+v", conditional)
	}
	missingAction := action
	missingAction.ObjectKey = "missing"
	if _, err := handlers.ConditionalUpdate(t.Context(), invocation, missingAction, nil); apperror.CodeOf(err) != "backend.action.object_schema_missing" {
		t.Fatalf("missing object error=%v", err)
	}
	if _, err := handlers.ConditionalUpdate(t.Context(), invocation, action, map[string]any{"expected_updated_at": nil}); err != nil {
		t.Fatalf("nil expected updated at error=%v", err)
	}
	if _, err := handlers.ConditionalUpdate(t.Context(), invocation, action, map[string]any{"expected_updated_at": ""}); err != nil {
		t.Fatalf("empty expected updated at error=%v", err)
	}
	if commits := canonicalMutationCommits([]transactionmodel.MutationPlan{{}, {}}); len(commits) != 2 {
		t.Fatalf("commits=%+v", commits)
	}
}

func TestTransitionSystemOperationFailsClosedWithoutStatePayload(t *testing.T) {
	planned := 0
	dependencies := RecordSystemOperationDependencies{
		ObjectForKey: func(key string) (definitionmodel.ObjectSchema, bool) {
			if key == "missing" {
				return definitionmodel.ObjectSchema{}, false
			}
			return definitionmodel.ObjectSchema{Key: key, Fields: []definitionmodel.FieldSchema{{Key: "status"}}}, true
		},
		PlanUpdateMutation: func(context.Context, string, string, map[string]any, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
			planned++
			return transactionmodel.MutationPlan{}, recordmodel.Record{ID: "record"}, nil
		},
	}
	handlers := NewRecordSystemOperationHandlers(dependencies)
	invocation := actionmodel.ActionInvocation{RecordID: "record", Principal: principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}}
	action := definitionmodel.ActionSchema{Key: "object.approve", ObjectKey: "object", Kind: definitionmodel.ActionKindTransitionState}
	for name, payload := range map[string]map[string]any{
		"nil payload":                 nil,
		"empty payload":               {},
		"payload without state field": {"expected_version": 2, "idempotency_key": "key-1"},
	} {
		if _, err := handlers.Transition(t.Context(), invocation, action, payload); apperror.CodeOf(err) != "backend.action.transition_payload_required" {
			t.Fatalf("%s: error=%v", name, err)
		}
		if planned != 0 {
			t.Fatalf("%s: transition without state payload planned %d mutations", name, planned)
		}
	}
	result, err := handlers.Transition(t.Context(), invocation, action, map[string]any{"status": "approved"})
	if err != nil || planned != 1 || result.Record == nil || len(result.Commits) != 1 {
		t.Fatalf("transition with state payload result=%+v planned=%d error=%v", result, planned, err)
	}
	missingAction := action
	missingAction.ObjectKey = "missing"
	if _, err := handlers.Transition(t.Context(), invocation, missingAction, map[string]any{"status": "approved"}); apperror.CodeOf(err) != "backend.action.object_schema_missing" {
		t.Fatalf("missing object error=%v", err)
	}
}
