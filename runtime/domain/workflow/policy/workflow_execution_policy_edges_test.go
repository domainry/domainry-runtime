package policy

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowExecutionPermissionAndInputPolicies(t *testing.T) {
	unknown := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{Permissions: []string{"*"}})
	if WorkflowPermissionAllows(unknown, "run") || WorkflowDefinitionPermissionAllows(unknown, "workflow.publish") {
		t.Fatal("unknown principal must be denied")
	}
	if !WorkflowPermissionAllows(accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"ops.workflow.run"}}), "run") {
		t.Fatal("operations permission should allow workflow action")
	}
	if !WorkflowPermissionAllows(accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workflow.cancel"}}), "cancel") {
		t.Fatal("workflow object permission should allow action")
	}
	if WorkflowPermissionAllows(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, "run") {
		t.Fatal("missing permission must be denied")
	}
	if WorkflowDefinitionPermissionAllows(accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}}), "workflow.publish") {
		t.Fatal("workspace.admin must not expand to workflow.publish")
	}
	if !WorkflowDefinitionPermissionAllows(accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workflow.publish"}}), "workflow.publish") {
		t.Fatal("definition permission should allow its exact grant")
	}
	if WorkflowDefinitionPermissionAllows(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, "workflow.publish") {
		t.Fatal("definition permission must reject missing grant")
	}

	workflow := definitionmodel.WorkflowSchema{TriggerContract: &definitionmodel.WorkflowTriggerContract{FieldKey: "status"}}
	if got := WorkflowChangedFieldsFromTrigger(workflow, " record_updated:order.amount "); !reflect.DeepEqual(got, []string{"amount", "status"}) {
		t.Fatalf("changed fields = %#v", got)
	}
	if got := WorkflowChangedFieldsFromTrigger(workflow, "record_updated:"); !reflect.DeepEqual(got, []string{"status"}) {
		t.Fatalf("invalid trigger fields = %#v", got)
	}
	if got := WorkflowChangedFieldsFromTrigger(definitionmodel.WorkflowSchema{TriggerContract: &definitionmodel.WorkflowTriggerContract{FieldKey: " amount "}}, "record_updated:order.amount"); !reflect.DeepEqual(got, []string{"amount"}) {
		t.Fatalf("duplicate changed fields = %#v", got)
	}
	if got := WorkflowChangedFieldsFromTrigger(definitionmodel.WorkflowSchema{}, "manual"); len(got) != 0 {
		t.Fatalf("manual changed fields = %#v", got)
	}
	if got := WorkflowPayloadString(map[string]any{"value": " text ", "nil": nil}, "value"); got != "text" {
		t.Fatalf("payload value = %q", got)
	}
	if WorkflowPayloadString(map[string]any{"nil": nil}, "nil") != "" || WorkflowPayloadString(nil, "missing") != "" {
		t.Fatal("missing payload value must be empty")
	}
}

func TestWorkflowRetryConversionAndSchedulingMatrix(t *testing.T) {
	for _, test := range []struct {
		value any
		want  int
	}{{1, 1}, {int32(2), 2}, {int64(3), 3}, {float32(4.9), 4}, {float64(5.9), 5}, {json.Number("6"), 6}, {json.Number("bad"), 0}, {"7", 0}} {
		if got := WorkflowRetryCount(test.value); got != test.want {
			t.Fatalf("retry count %#v = %d, want %d", test.value, got, test.want)
		}
	}
	for _, execution := range []workflowmodel.WorkflowExecution{
		{Status: "completed", NextRunAt: "now"},
		{Status: "running", NextRunAt: "now"},
		{Status: "failed", Attempt: 3, MaxAttempts: 3, NextRunAt: "now"},
		{Status: "pending", NextRunAt: ""},
	} {
		if WorkflowExecutionRetryScheduled(execution) {
			t.Fatalf("retry unexpectedly scheduled for %+v", execution)
		}
	}
	if !WorkflowExecutionRetryScheduled(workflowmodel.WorkflowExecution{Status: " pending ", Attempt: 1, MaxAttempts: 3, NextRunAt: " now "}) {
		t.Fatal("pending retry should be scheduled")
	}

	now := time.Date(2026, 7, 19, 12, 0, 0, 500, time.UTC)
	for _, test := range []struct {
		name      string
		execution workflowmodel.WorkflowExecution
		want      bool
	}{
		{name: "running expired nano", execution: workflowmodel.WorkflowExecution{Status: "running", LeaseExpiresAt: now.Add(-time.Nanosecond).Format(time.RFC3339Nano)}, want: true},
		{name: "running expired seconds", execution: workflowmodel.WorkflowExecution{Status: "running", LeaseExpiresAt: now.Add(-time.Second).Truncate(time.Second).Format(time.RFC3339)}, want: true},
		{name: "running future", execution: workflowmodel.WorkflowExecution{Status: "running", LeaseExpiresAt: now.Add(time.Minute).Format(time.RFC3339Nano)}},
		{name: "running invalid", execution: workflowmodel.WorkflowExecution{Status: "running", LeaseExpiresAt: "invalid"}},
		{name: "completed", execution: workflowmodel.WorkflowExecution{Status: "completed"}},
		{name: "max attempts", execution: workflowmodel.WorkflowExecution{Status: "failed", Attempt: 2, MaxAttempts: 2}},
		{name: "pending immediate", execution: workflowmodel.WorkflowExecution{Status: "pending"}, want: true},
		{name: "failed without next", execution: workflowmodel.WorkflowExecution{Status: "failed"}},
		{name: "pending due nano", execution: workflowmodel.WorkflowExecution{Status: "pending", NextRunAt: now.Add(-time.Nanosecond).Format(time.RFC3339Nano)}, want: true},
		{name: "pending due seconds", execution: workflowmodel.WorkflowExecution{Status: "pending", NextRunAt: now.Add(-time.Second).Truncate(time.Second).Format(time.RFC3339)}, want: true},
		{name: "pending future", execution: workflowmodel.WorkflowExecution{Status: "pending", NextRunAt: now.Add(time.Minute).Format(time.RFC3339Nano)}},
		{name: "pending invalid", execution: workflowmodel.WorkflowExecution{Status: "pending", NextRunAt: "invalid"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := WorkflowExecutionDue(test.execution, now); got != test.want {
				t.Fatalf("due = %v, want %v", got, test.want)
			}
		})
	}
}

func TestWorkflowDefaultsManualAndFailureTransitions(t *testing.T) {
	if WorkflowManualRunAllowed(definitionmodel.WorkflowSchema{}) || WorkflowManualRunAllowed(definitionmodel.WorkflowSchema{TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "event"}}) || !WorkflowManualRunAllowed(definitionmodel.WorkflowSchema{TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: " manual "}}) {
		t.Fatal("manual-run policy mismatch")
	}
	if WorkflowMaxAttempts(definitionmodel.WorkflowSchema{}) != 3 || WorkflowMaxAttempts(definitionmodel.WorkflowSchema{Retry: &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 4}}) != 4 || WorkflowMaxAttempts(definitionmodel.WorkflowSchema{Retry: &definitionmodel.WorkflowRetryPolicy{MaxAttempts: -1}}) != 3 {
		t.Fatal("max attempts policy mismatch")
	}
	if WorkflowRetryDelaySeconds(definitionmodel.WorkflowSchema{}) != 60 || WorkflowRetryDelaySeconds(definitionmodel.WorkflowSchema{Retry: &definitionmodel.WorkflowRetryPolicy{DelaySeconds: 9}}) != 9 || WorkflowRetryDelaySeconds(definitionmodel.WorkflowSchema{Retry: &definitionmodel.WorkflowRetryPolicy{DelaySeconds: -1}}) != 60 {
		t.Fatal("retry delay policy mismatch")
	}
	if WorkflowRunAs(definitionmodel.WorkflowSchema{RunAs: " operator "}) != "operator" {
		t.Fatal("run-as normalization mismatch")
	}

	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	retry := workflowmodel.WorkflowExecution{Attempt: 1}
	WorkflowMarkFailed(&retry, definitionmodel.WorkflowSchema{Retry: &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 2, DelaySeconds: 5}}, nil, now)
	if retry.Status != "failed" || retry.MaxAttempts != 2 || retry.NextRunAt != now.Add(5*time.Second).Format(time.RFC3339) || retry.Result["retry_scheduled"] != true || retry.Result["last_error_code"] != "backend.internal" {
		t.Fatalf("retry transition = %+v", retry)
	}
	dead := workflowmodel.WorkflowExecution{Attempt: 2, MaxAttempts: 2, Result: map[string]any{}}
	appErr := apperror.New(apperror.KindForbidden, "workflow.denied", errors.New("denied"), map[string]string{"role": "viewer"})
	WorkflowMarkFailed(&dead, definitionmodel.WorkflowSchema{}, appErr, now)
	if dead.Status != "dead_letter" || dead.NextRunAt != "" || dead.Result["dead_lettered"] != true || dead.LastError != "workflow.denied" || !reflect.DeepEqual(dead.Result["last_error_params"], map[string]string{"role": "viewer"}) {
		t.Fatalf("dead-letter transition = %+v", dead)
	}
}

func TestWorkflowIdempotencyCloneAndErrorNormalization(t *testing.T) {
	if WorkflowIdempotencyKey(definitionmodel.WorkflowSchema{}, nil) != "" {
		t.Fatal("workflow without keys must not produce idempotency key")
	}
	invalid := definitionmodel.WorkflowSchema{Key: "workflow", IdempotencyKeys: []string{"value"}}
	if WorkflowIdempotencyKey(invalid, map[string]any{"value": make(chan int)}) != "" {
		t.Fatal("unencodable projection must not produce idempotency key")
	}
	original := map[string]any{"value": 1}
	clone := WorkflowCloneMap(original)
	clone["value"] = 2
	if original["value"] != 1 || WorkflowCloneMap(nil) == nil {
		t.Fatal("workflow clone must isolate map and preserve non-nil empty output")
	}
	for _, test := range []struct {
		value any
		want  bool
	}{{nil, true}, {"", true}, {"  ", true}, {"x", false}, {0, false}} {
		if got := WorkflowValueIsEmpty(test.value); got != test.want {
			t.Fatalf("empty(%#v) = %v", test.value, got)
		}
	}
	if code, params := workflowErrorCode(errors.New("raw")); code != "backend.internal" || !reflect.DeepEqual(params, map[string]string{"operation": "workflow"}) {
		t.Fatalf("raw error = %q/%#v", code, params)
	}
	for _, test := range []struct {
		kind apperror.ErrorKind
		code string
		want string
	}{{apperror.KindBadRequest, "bad", "backend.bad_request"}, {apperror.KindForbidden, "bad", "backend.forbidden"}, {apperror.KindNotFound, "bad", "backend.not_found"}, {apperror.KindConflict, "bad", "backend.conflict"}, {apperror.KindInternal, "bad", "backend.internal"}, {apperror.KindInternal, " custom.code ", "custom.code"}} {
		if got := workflowNormalizedErrorCode(test.kind, test.code); got != test.want {
			t.Fatalf("normalized %s/%q = %q, want %q", test.kind, test.code, got, test.want)
		}
	}
	if !workflowContainsText([]string{"a", "b"}, "b") || workflowContainsText([]string{"a"}, "b") {
		t.Fatal("contains text policy mismatch")
	}
}
