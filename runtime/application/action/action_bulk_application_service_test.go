package action

import (
	"errors"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionruntime "github.com/domainry/domainry-runtime/runtime/domain/action/runtime"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"context"
	"reflect"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

type bulkExecutionProbe struct {
	execution    actionmodel.ActionBusinessExecution
	allowReclaim bool
	beginCalls   int
}

func (p *bulkExecutionProbe) TryBeginExecution(_ context.Context, request actionmodel.ActionExecutionClaimRequest) (actionmodel.ActionExecutionClaimResult, error) {
	p.beginCalls++
	if p.execution.ID != "" {
		if p.execution.RequestFingerprint != request.RequestFingerprint {
			return actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionFingerprintConflict, Execution: p.execution}, nil
		}
		if p.execution.Status == string(idempotency.StatusSucceeded) {
			return actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionReplay, Execution: p.execution}, nil
		}
		if p.allowReclaim {
			p.allowReclaim = false
			p.execution.Status = string(idempotency.StatusProcessing)
			p.execution.LeaseOwner = request.LeaseOwner
			p.execution.FencingToken++
			return actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionAcquired, Execution: p.execution}, nil
		}
		return actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionInProgress, Execution: p.execution}, nil
	}
	p.execution = request.Execution
	p.execution.ID, p.execution.RequestFingerprint = "bulk-execution", request.RequestFingerprint
	p.execution.Status, p.execution.LeaseOwner, p.execution.FencingToken = string(idempotency.StatusProcessing), request.LeaseOwner, 1
	return actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionAcquired, Execution: p.execution}, nil
}

func TestActionBulkReplayRechecksPermissionBeforeReadingReceipt(t *testing.T) {
	executions := &bulkExecutionProbe{execution: actionmodel.ActionBusinessExecution{ID: "existing-receipt", Status: string(idempotency.StatusSucceeded)}}
	service := NewActionBulkApplicationService(ActionBulkDependencies{
		Actions: func(context.Context) []definitionmodel.ActionSchema {
			return []definitionmodel.ActionSchema{{Key: "order.approve", ObjectKey: "order", RequiresPermission: "order.approve"}}
		},
		Allowed: func(principalmodel.Principal, definitionmodel.ActionSchema) bool { return false },
		Invoke: func(context.Context, actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
			t.Fatal("revoked permission must not replay or execute")
			return actionmodel.ActionInvocationResult{}, nil
		},
		Execution: actionruntime.NewActionExecutionRuntime(executions),
	})
	_, err := service.ExecuteBulkAction(t.Context(), "order", "order.approve", actionmodel.ActionBulkRequest{RecordIDs: []string{"record-1"}, IdempotencyKey: "existing-key"}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	if apperror.CodeOf(err) != "backend.action.permission_denied" || executions.beginCalls != 0 {
		t.Fatalf("permission replay error=%v receipt reads=%d", err, executions.beginCalls)
	}
}

func TestActionBulkApplicationServiceResumesRowsAfterOperationReclaim(t *testing.T) {
	executions := &bulkExecutionProbe{}
	effects := map[string]int{}
	firstAttempt := true
	ctx, cancel := context.WithCancel(t.Context())
	service := NewActionBulkApplicationService(ActionBulkDependencies{
		Actions: func(context.Context) []definitionmodel.ActionSchema {
			return []definitionmodel.ActionSchema{{Key: "order.approve", ObjectKey: "order"}}
		},
		Invoke: func(ctx context.Context, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
			key, _ := invocation.Input["idempotency_key"].(string)
			if invocation.RecordID == "record-2" && firstAttempt {
				firstAttempt = false
				cancel()
				return actionmodel.ActionInvocationResult{}, errors.New("worker interrupted")
			}
			if err := ctx.Err(); err != nil {
				return actionmodel.ActionInvocationResult{}, err
			}
			if effects[key] == 0 {
				effects[key]++
			}
			result := actionmodel.ActionResult{ActionKey: invocation.ActionKey, ObjectKey: invocation.ObjectKey, RecordID: invocation.RecordID}
			return actionmodel.ActionInvocationResult{Record: &result}, nil
		},
		Execution: actionruntime.NewActionExecutionRuntime(executions),
	})

	request := actionmodel.ActionBulkRequest{RecordIDs: []string{"record-1", "record-2", "record-3"}, IdempotencyKey: "bulk-resume"}
	if _, err := service.ExecuteBulkAction(ctx, "order", "order.approve", request, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("first execution error=%v", err)
	}
	if effects["bulk-resume:record-1"] != 1 || effects["bulk-resume:record-2"] != 0 || effects["bulk-resume:record-3"] != 0 {
		t.Fatalf("unexpected partial effects: %#v", effects)
	}

	executions.allowReclaim = true
	result, err := service.ExecuteBulkAction(t.Context(), "order", "order.approve", request, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	if err != nil {
		t.Fatalf("resume bulk action: %v", err)
	}
	if result.Succeeded != 3 || result.Failed != 0 {
		t.Fatalf("unexpected resumed result: %#v", result)
	}
	for _, recordID := range request.RecordIDs {
		key := "bulk-resume:" + recordID
		if effects[key] != 1 {
			t.Fatalf("row %s effects=%d, want 1", recordID, effects[key])
		}
	}
}

func (p *bulkExecutionProbe) HeartbeatExecution(context.Context, string, string, int64, time.Time, time.Time) error {
	return nil
}

func (p *bulkExecutionProbe) CompleteExecution(_ context.Context, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	p.execution.Status, p.execution.Result = string(idempotency.StatusSucceeded), completion.Result
	return p.execution, nil
}

func (p *bulkExecutionProbe) CommitExecution(ctx context.Context, _ []transactionmodel.RecordMutationCommit, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	return p.CompleteExecution(ctx, completion)
}

func TestActionBulkApplicationServiceFiltersAndSortsAvailableActions(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{"order.approve", "order.cancel"}})
	service := NewActionBulkApplicationService(ActionBulkDependencies{
		Allowed: func(principal principalmodel.Principal, action definitionmodel.ActionSchema) bool {
			for _, permission := range principal.PermissionKeys() {
				if permission == action.RequiresPermission {
					return true
				}
			}
			return false
		},
		Actions: func(context.Context) []definitionmodel.ActionSchema {
			return []definitionmodel.ActionSchema{
				{Key: "order.cancel", ObjectKey: "order", RequiresPermission: "order.cancel"},
				{Key: "customer.approve", ObjectKey: "customer", RequiresPermission: "customer.approve"},
				{Key: "order.approve", ObjectKey: "order", RequiresPermission: "order.approve"},
			}
		},
	})
	actions, err := service.ActionsForObject(t.Context(), " order ", principal)
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	keys := []string{actions[0].Key, actions[1].Key}
	if !reflect.DeepEqual(keys, []string{"order.approve", "order.cancel"}) {
		t.Fatalf("unexpected actions: %#v", keys)
	}
}

func TestActionBulkApplicationServiceOwnsBulkAggregationAndExpectedVersions(t *testing.T) {
	invocations := []actionmodel.ActionInvocation{}
	audited := false
	executions := &bulkExecutionProbe{}
	service := NewActionBulkApplicationService(ActionBulkDependencies{
		Actions: func(context.Context) []definitionmodel.ActionSchema {
			return []definitionmodel.ActionSchema{{Key: "order.approve", ObjectKey: "order"}}
		},
		Invoke: func(_ context.Context, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
			invocations = append(invocations, invocation)
			result := actionmodel.ActionResult{ActionKey: invocation.ActionKey, ObjectKey: invocation.ObjectKey, RecordID: invocation.RecordID}
			return actionmodel.ActionInvocationResult{Record: &result}, nil
		},
		AuditBulk: func(_ context.Context, result actionmodel.ActionBulkResult, recordIDs []string, _ principalmodel.Principal) {
			audited = result.Succeeded == 2 && reflect.DeepEqual(recordIDs, []string{"record-2", "record-1"})
		},
		Execution: actionruntime.NewActionExecutionRuntime(executions),
	})
	result, err := service.ExecuteBulkAction(t.Context(), "order", "order.approve", actionmodel.ActionBulkRequest{
		RecordIDs: []string{"record-2", "record-1", "record-2"}, Data: map[string]any{"approved": true}, ExpectedVersions: map[string]int{"record-1": 7}, IdempotencyKey: "bulk-key",
	}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	if err != nil {
		t.Fatalf("bulk action: %v", err)
	}
	if result.Total != 2 || result.Succeeded != 2 || !audited {
		t.Fatalf("unexpected result: %+v audited=%v", result, audited)
	}
	if invocations[0].RecordID != "record-2" || invocations[0].Input["idempotency_key"] != "bulk-key:record-2" || invocations[1].Input["idempotency_key"] != "bulk-key:record-1" || invocations[1].Input["expected_version"] != 7 || invocations[1].Source != actionmodel.ActionSourceBulk {
		t.Fatalf("unexpected invocations: %+v", invocations)
	}
	replayed, err := service.ExecuteBulkAction(t.Context(), "order", "order.approve", actionmodel.ActionBulkRequest{
		RecordIDs: []string{"record-2", "record-1", "record-2"}, Data: map[string]any{"approved": true}, ExpectedVersions: map[string]int{"record-1": 7}, IdempotencyKey: "bulk-key",
	}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	if err != nil || replayed.Message != "backend.action.idempotent_replay" || len(invocations) != 2 {
		t.Fatalf("replay=%#v invocations=%d err=%v", replayed, len(invocations), err)
	}
	_, err = service.ExecuteBulkAction(t.Context(), "order", "order.approve", actionmodel.ActionBulkRequest{RecordIDs: []string{"record-3"}, Data: map[string]any{"approved": true}, IdempotencyKey: "bulk-key"}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	if apperror.CodeOf(err) != idempotency.ErrorCodeKeyReused {
		t.Fatalf("bulk fingerprint conflict=%v", err)
	}
}
