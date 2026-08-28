// Instruction domain service tests.
package automation

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"errors"
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"

	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type instructionRepositoryStub struct {
	claim     func(context.Context, automationmodel.AutomationInstructionExecution, string, string) (automationmodel.AutomationInstructionExecution, bool, error)
	heartbeat func(context.Context, string, string, string, int64, string) (automationmodel.AutomationInstructionExecution, error)
	complete  func(context.Context, string, string, string, int64, string, map[string]any, string) (automationmodel.AutomationInstructionExecution, error)
}

func (s instructionRepositoryStub) ClaimInstruction(ctx context.Context, _ string, execution automationmodel.AutomationInstructionExecution, _ string, now, expires string) (automationmodel.AutomationInstructionExecution, bool, error) {
	return s.claim(ctx, execution, now, expires)
}

func (s instructionRepositoryStub) HeartbeatInstruction(ctx context.Context, workspaceID, key, owner string, fencingToken int64, expires, _ string) (automationmodel.AutomationInstructionExecution, error) {
	if s.heartbeat == nil {
		return automationmodel.AutomationInstructionExecution{}, nil
	}
	return s.heartbeat(ctx, workspaceID, key, owner, fencingToken, expires)
}

func (s instructionRepositoryStub) CompleteInstruction(ctx context.Context, workspaceID, key, owner string, fencingToken int64, status string, result map[string]any, errorCode, _ string) (automationmodel.AutomationInstructionExecution, error) {
	return s.complete(ctx, workspaceID, key, owner, fencingToken, status, result, errorCode)
}

func TestInstructionServiceReplaysSucceededExecution(t *testing.T) {
	repository := instructionRepositoryStub{
		claim: func(context.Context, automationmodel.AutomationInstructionExecution, string, string) (automationmodel.AutomationInstructionExecution, bool, error) {
			return automationmodel.AutomationInstructionExecution{Status: "succeeded", Result: map[string]any{
				"key": "old", "type": "old", "status": "succeeded", "invocation_id": "inv-1", "data": map[string]any{"sent": true},
			}}, false, nil
		},
		complete: func(context.Context, string, string, string, int64, string, map[string]any, string) (automationmodel.AutomationInstructionExecution, error) {
			t.Fatal("replay must not complete an unclaimed execution")
			return automationmodel.AutomationInstructionExecution{}, nil
		},
	}
	result, err := NewAutomationInstructionExecutionApplicationService(repository).Execute(t.Context(), afterInstructionRequest(func(context.Context) (automationmodel.AutomationInstructionResult, error) {
		t.Fatal("replay must not execute side effects")
		return automationmodel.AutomationInstructionResult{}, nil
	}))
	if err != nil || result.Status != "idempotent_replay" || result.Key != "send" || result.InvocationID != "inv-1" || result.Data["sent"] != true {
		t.Fatalf("unexpected replay result=%#v err=%v", result, err)
	}
}

func TestInstructionServiceMapsCompletionLeaseLoss(t *testing.T) {
	repository := instructionRepositoryStub{
		claim: func(_ context.Context, execution automationmodel.AutomationInstructionExecution, _, _ string) (automationmodel.AutomationInstructionExecution, bool, error) {
			execution.LeaseOwner = "worker-1"
			execution.FencingToken = 1
			return execution, true, nil
		},
		complete: func(context.Context, string, string, string, int64, string, map[string]any, string) (automationmodel.AutomationInstructionExecution, error) {
			return automationmodel.AutomationInstructionExecution{}, mutation.MutationConflict("automation_instruction", "key", mutation.MutationConflictLeaseLost, nil)
		},
	}
	result, err := NewAutomationInstructionExecutionApplicationService(repository).Execute(t.Context(), afterInstructionRequest(func(context.Context) (automationmodel.AutomationInstructionResult, error) {
		return automationmodel.AutomationInstructionResult{Key: "send", Type: "emit_event", Status: "success"}, nil
	}))
	assertAutomationError(t, err, apperror.KindConflict, "backend.automation.instruction_lease_lost")
	if result.Status != "failed" || result.ErrorCode != "backend.automation.instruction_lease_lost" {
		t.Fatalf("unexpected failed result: %#v", result)
	}
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Params["instruction"] != "send" {
		t.Fatalf("unexpected error params: %#v", err)
	}
}

func afterInstructionRequest(execute func(context.Context) (automationmodel.AutomationInstructionResult, error)) AutomationInstructionExecutionRequest {
	return AutomationInstructionExecutionRequest{
		Phase: "after", WorkspaceID: "workspace-1",
		Rule:        automationmodel.AutomationRuleSchema{Key: "notify", ObjectKey: "order", Trigger: automationmodel.AutomationTriggerSchema{Operation: "update"}},
		Instruction: automationmodel.AutomationInstructionSchema{Key: "send", Type: "emit_event"},
		Record:      &recordmodel.Record{ID: "order-1", UpdatedAt: "v1"}, Execute: execute,
	}
}
