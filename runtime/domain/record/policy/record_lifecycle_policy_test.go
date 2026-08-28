package policy

import (
	"reflect"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestRecordAutomationTransitionCandidateMatrix(t *testing.T) {
	for _, test := range []struct {
		before, candidate map[string]any
		want              bool
	}{{nil, nil, false}, {map[string]any{"status": "open"}, map[string]any{"status": "closed"}, true}, {map[string]any{"status": "same", "state": 1}, map[string]any{"status": "same", "state": 2}, true}, {map[string]any{"status": "same", "state": "same", "current_stage": "a"}, map[string]any{"status": "same", "state": "same", "current_stage": "b"}, true}, {map[string]any{"status": "open"}, map[string]any{"status": "open"}, false}, {map[string]any{}, map[string]any{"status": "closed"}, false}} {
		if got := RecordAutomationTransitionCandidate(test.before, test.candidate); got != test.want {
			t.Fatalf("transition %#v -> %#v = %v", test.before, test.candidate, got)
		}
	}
}

func TestRecordSchedulerCRUDSoftDeleteAndRestorePolicy(t *testing.T) {
	for _, test := range []struct {
		object    definitionmodel.ObjectSchema
		operation string
		code      string
	}{
		{object: definitionmodel.ObjectSchema{Key: "job_run"}},
		{object: definitionmodel.ObjectSchema{Key: "job_run", Config: map[string]any{"scheduler_runtime": true}}, operation: "update", code: "backend.scheduler.runtime_api_required"},
		{object: definitionmodel.ObjectSchema{Key: "job_run_event", Config: map[string]any{"scheduler_runtime": "true"}}, operation: "delete", code: "backend.scheduler.runtime_api_required"},
		{object: definitionmodel.ObjectSchema{Key: "job_dead_letter", Config: map[string]any{"scheduler_runtime": true}}, operation: "create", code: "backend.scheduler.runtime_api_required"},
		{object: definitionmodel.ObjectSchema{Key: "scheduler_cursor", Config: map[string]any{"scheduler_runtime": true}}, operation: "update", code: "backend.scheduler.runtime_api_required"},
		{object: definitionmodel.ObjectSchema{Key: "record_timer", Config: map[string]any{"scheduler_runtime": true}}, operation: "delete", code: "backend.scheduler.runtime_api_required"},
		{object: definitionmodel.ObjectSchema{Key: "other", Config: map[string]any{"scheduler_runtime": true}}, operation: "delete"},
	} {
		err := RecordValidateSchedulerOperationalCRUD(test.object, test.operation)
		if test.code == "" && err != nil || test.code != "" && apperror.CodeOf(err) != test.code {
			t.Fatalf("CRUD %#v/%s = %v", test.object, test.operation, err)
		}
	}
	soft := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "status"}, {Key: "deleted_at"}, {Key: "deleted_by"}}}
	if !RecordUsesSoftDelete(soft) || RecordUsesSoftDelete(definitionmodel.ObjectSchema{Fields: soft.Fields[:2]}) {
		t.Fatal("soft delete classification mismatch")
	}
	for _, test := range []struct {
		config map[string]any
		want   string
	}{{map[string]any{"restore_status": " restored ", "active_status": "active"}, "restored"}, {map[string]any{"restore_status": "", "active_status": " enabled "}, "enabled"}, {map[string]any{"restore_status": nil, "active_status": " enabled "}, "enabled"}, {nil, "active"}} {
		if got := RecordRestoreStatus(definitionmodel.ObjectSchema{Config: test.config}); got != test.want {
			t.Fatalf("restore %#v = %q", test.config, got)
		}
	}
}

func TestRecordRelationLifecycleAndScalarHelpers(t *testing.T) {
	for _, test := range []struct {
		value any
		want  string
	}{{nil, "restrict"}, {"", "restrict"}, {" cascade ", "cascade"}} {
		if got := RecordRelationDeletePolicy(definitionmodel.FieldSchema{Config: map[string]any{"on_delete": test.value}}); got != test.want {
			t.Fatalf("delete policy %#v = %q", test.value, got)
		}
	}
	err := lifecycleError(apperror.KindBadRequest, "code", "", "ignored", "field", "status", "orphan")
	var appErr *apperror.AppError
	if !reflect.TypeOf(err).AssignableTo(reflect.TypeOf(appErr)) || apperror.CodeOf(err) != "code" {
		t.Fatalf("lifecycle error = %v", err)
	}
	if empty := lifecycleError(apperror.KindBadRequest, "empty"); apperror.CodeOf(empty) != "empty" {
		t.Fatalf("empty lifecycle error = %v", empty)
	}
	for _, test := range []struct {
		value any
		want  bool
	}{{true, true}, {false, false}, {" true ", true}, {"false", false}, {1, false}} {
		if got := boolFromAny(test.value); got != test.want {
			t.Fatalf("bool %#v = %v", test.value, got)
		}
	}
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "one"}, {Key: "two"}}}
	if !recordLifecycleFieldExists(object, "two") || recordLifecycleFieldExists(object, "missing") {
		t.Fatal("lifecycle field lookup mismatch")
	}
}

func TestRecordLifecycleMutationPolicy(t *testing.T) {
	appendOnly := definitionmodel.ObjectSchema{Key: "entry", LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleAppendOnly}}
	if err := RecordValidateLifecycleMutation(appendOnly, "create", nil); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"update", "delete", "restore"} {
		if code := apperror.CodeOf(RecordValidateLifecycleMutation(appendOnly, operation, nil)); code != "backend.record.lifecycle_append_only" {
			t.Fatalf("operation=%s code=%s", operation, code)
		}
	}
	locked := definitionmodel.ObjectSchema{Key: "settlement", LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleImmutableAfterState, StateField: "status", ImmutableStates: []string{"confirmed", "reversed"}}}
	if err := RecordValidateLifecycleMutation(locked, "update", map[string]any{"status": "draft"}); err != nil {
		t.Fatal(err)
	}
	if code := apperror.CodeOf(RecordValidateLifecycleMutation(locked, "update", map[string]any{"status": "confirmed"})); code != "backend.record.lifecycle_state_immutable" {
		t.Fatalf("immutable code=%s", code)
	}
	soft := definitionmodel.ObjectSchema{LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleSoftDeleteOnly}}
	if !RecordRequiresSoftDelete(soft) || RecordUsesSoftDelete(soft) {
		t.Fatal("soft-delete lifecycle must require its three storage fields")
	}
	blankMode := definitionmodel.ObjectSchema{LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: " "}}
	if got := RecordLifecycleMode(blankMode); got != definitionmodel.ObjectLifecycleMutable {
		t.Fatalf("blank lifecycle mode=%q", got)
	}
	if err := RecordValidateLifecycleMutation(definitionmodel.ObjectSchema{LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleImmutableAfterState}}, "create", nil); err != nil {
		t.Fatalf("immutable create=%v", err)
	}
}
