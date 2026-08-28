package runtime

import (
	"context"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

type executionRepositoryProbe struct {
	execution actionmodel.ActionBusinessExecution
}

func (p *executionRepositoryProbe) TryBeginExecution(_ context.Context, request actionmodel.ActionExecutionClaimRequest) (actionmodel.ActionExecutionClaimResult, error) {
	if p.execution.ID != "" {
		if idempotency.Status(p.execution.Status) == idempotency.StatusFailedRetryable {
			p.execution.Status = string(idempotency.StatusProcessing)
			p.execution.LeaseOwner = request.LeaseOwner
			p.execution.FencingToken++
			return actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionAcquired, Execution: p.execution}, nil
		}
		return actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionReplay, Execution: p.execution}, nil
	}
	p.execution = request.Execution
	p.execution.ID, p.execution.RequestFingerprint = "execution-1", request.RequestFingerprint
	p.execution.Status, p.execution.LeaseOwner, p.execution.FencingToken = string(idempotency.StatusProcessing), request.LeaseOwner, 1
	return actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionAcquired, Execution: p.execution}, nil
}

func (p *executionRepositoryProbe) HeartbeatExecution(context.Context, string, string, int64, time.Time, time.Time) error {
	return nil
}

func (p *executionRepositoryProbe) CompleteExecution(_ context.Context, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	status := idempotency.StatusSucceeded
	if completion.ErrorCode != "" {
		status = idempotency.StatusFailedTerminal
		if completion.Retryable {
			status = idempotency.StatusFailedRetryable
		}
	}
	p.execution.Status, p.execution.Result = string(status), completion.Result
	p.execution.ErrorCode = completion.ErrorCode
	return p.execution, nil
}

func TestExecutionRuntimeOwnsAtomicClaimAndReplay(t *testing.T) {
	repository := &executionRepositoryProbe{}
	service := NewActionExecutionRuntime(repository)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace", UserID: "user"}, RequestID: "request-owner"}, accessfixture.Bundle{Key: "admin"})
	want := actionmodel.ActionObjectResult{ActionKey: "order.create", ObjectKey: "order", Output: map[string]any{"status": "created"}}
	input := idempotency.FingerprintInput{UseCase: "action.execute_object", ResourceType: "object_action", TargetID: "order/order.create", Payload: map[string]any{"name": "A"}}
	_, claim, replay, err := service.BeginObject(t.Context(), "order", want.ActionKey, "request-1", input, principal)
	if err != nil || replay || claim.Decision != idempotency.DecisionAcquired {
		t.Fatalf("first claim=%#v replay=%v err=%v", claim, replay, err)
	}
	if err := service.Complete(t.Context(), claim, want); err != nil {
		t.Fatal(err)
	}
	got, replayClaim, replay, err := service.BeginObject(t.Context(), "order", want.ActionKey, "request-1", input, principal)
	if err != nil || !replay || replayClaim.Decision != idempotency.DecisionReplay || got.Output["status"] != "created" || got.Message != "backend.action.idempotent_replay" {
		t.Fatalf("replayed result=%#v claim=%#v replay=%v err=%v", got, replayClaim, replay, err)
	}
}

func TestExecutionRuntimePersistsAndReplaysTerminalFailureWhileReclaimingRetryableFailure(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace", UserID: "user"}, RequestID: "request-owner"}
	input := idempotency.FingerprintInput{UseCase: "action.invoke", ResourceType: "action", TargetID: "booking.reserve"}

	terminalRepository := &executionRepositoryProbe{}
	terminal := NewActionExecutionRuntime(terminalRepository)
	_, claim, replay, err := terminal.BeginObject(t.Context(), "booking", "booking.reserve", "terminal-key", input, principal)
	if err != nil || replay {
		t.Fatalf("terminal claim=%#v replay=%v error=%v", claim, replay, err)
	}
	failure := &apperror.AppError{Kind: apperror.KindConflict, Code: "gym.class_waitlist_full", Params: map[string]string{"class_id": "class-1"}}
	if err := terminal.Fail(t.Context(), claim, actionmodel.ActionInvocationResult{
		InvocationID: "terminal-key", Status: "failed", ErrorCode: failure.Code,
	}, failure, nil); err != nil {
		t.Fatal(err)
	}
	if terminalRepository.execution.Status != string(idempotency.StatusFailedTerminal) {
		t.Fatalf("terminal status=%q", terminalRepository.execution.Status)
	}
	_, replayClaim, replay, err := terminal.BeginObject(t.Context(), "booking", "booking.reserve", "terminal-key", input, principal)
	if replay || replayClaim.Decision != idempotency.DecisionReplay ||
		apperror.CodeOf(err) != failure.Code || apperror.KindOf(err) != failure.Kind ||
		apperror.ParamsOf(err)["class_id"] != "class-1" {
		t.Fatalf("terminal replay claim=%#v replay=%v error=%v", replayClaim, replay, err)
	}

	retryableRepository := &executionRepositoryProbe{}
	retryable := NewActionExecutionRuntime(retryableRepository)
	_, claim, replay, err = retryable.BeginObject(t.Context(), "booking", "booking.reserve", "retryable-key", input, principal)
	if err != nil || replay {
		t.Fatalf("retryable claim=%#v replay=%v error=%v", claim, replay, err)
	}
	if err := retryable.Fail(t.Context(), claim, actionmodel.ActionInvocationResult{
		InvocationID: "retryable-key", Status: "failed", ErrorCode: "backend.action.timeout", Retryable: true,
	}, apperror.New(apperror.KindUnavailable, "backend.action.timeout", context.DeadlineExceeded, nil), nil); err != nil {
		t.Fatal(err)
	}
	if retryableRepository.execution.Status != string(idempotency.StatusFailedRetryable) {
		t.Fatalf("retryable status=%q", retryableRepository.execution.Status)
	}
	_, reclaimed, replay, err := retryable.BeginObject(t.Context(), "booking", "booking.reserve", "retryable-key", input, principal)
	if err != nil || replay || reclaimed.Decision != idempotency.DecisionAcquired || reclaimed.Execution.FencingToken != 2 {
		t.Fatalf("retryable reclaim=%#v replay=%v error=%v", reclaimed, replay, err)
	}
}

func TestExecutionRuntimeAllowsActionWithoutIdempotencyKeyOrRepository(t *testing.T) {
	var nilService *ActionExecutionRuntime
	for _, service := range []*ActionExecutionRuntime{nilService, NewActionExecutionRuntime(nil)} {
		_, claim, replay, err := service.BeginRecord(t.Context(), "order", "order-1", "order.update", "request-1", idempotency.FingerprintInput{}, principalmodel.Principal{})
		if err != nil || replay || claim.Decision != idempotency.DecisionAcquired {
			t.Fatalf("optional claim=%#v replay=%v error=%v", claim, replay, err)
		}
		if err := service.Complete(t.Context(), claim, actionmodel.ActionResult{}); err != nil {
			t.Fatalf("optional completion error=%v", err)
		}
	}
}
