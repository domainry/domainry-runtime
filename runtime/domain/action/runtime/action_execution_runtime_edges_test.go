package runtime

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

type executionRepositoryEdgeStub struct {
	claim       actionmodel.ActionExecutionClaimResult
	claimErr    error
	completeErr error
	request     actionmodel.ActionExecutionClaimRequest
}

func (stub *executionRepositoryEdgeStub) TryBeginExecution(_ context.Context, request actionmodel.ActionExecutionClaimRequest) (actionmodel.ActionExecutionClaimResult, error) {
	stub.request = request
	return stub.claim, stub.claimErr
}

func (*executionRepositoryEdgeStub) HeartbeatExecution(context.Context, string, string, int64, time.Time, time.Time) error {
	return nil
}

func (stub *executionRepositoryEdgeStub) CompleteExecution(_ context.Context, _ actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	return actionmodel.ActionBusinessExecution{}, stub.completeErr
}

type executionTransactionEdgeStub struct {
	commitErr  error
	rollback   bool
	completion actionmodel.ActionExecutionCompletion
}

func (*executionTransactionEdgeStub) Context(ctx context.Context) context.Context { return ctx }
func (stub *executionTransactionEdgeStub) Commit(_ context.Context, _ []transactionmodel.RecordMutationCommit, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	stub.completion = completion
	return actionmodel.ActionBusinessExecution{}, stub.commitErr
}
func (stub *executionTransactionEdgeStub) RollBack(context.Context) error {
	stub.rollback = true
	return nil
}

type executionTransactionalRepositoryEdgeStub struct {
	*executionRepositoryEdgeStub
	transaction *executionTransactionEdgeStub
	beginErr    error
}

func (stub *executionTransactionalRepositoryEdgeStub) BeginExecutionTransaction(context.Context) (actioncontract.ActionExecutionTransaction, error) {
	if stub.beginErr != nil {
		return nil, stub.beginErr
	}
	return stub.transaction, nil
}

func TestActionExecutionRuntimeDecisionAndRepositoryEdges(t *testing.T) {
	backendErr := errors.New("backend")
	for _, test := range []struct {
		name     string
		decision idempotency.Decision
		claimErr error
		wantCode string
	}{
		{"claim error", idempotency.DecisionAcquired, backendErr, "backend.internal"},
		{"fingerprint conflict", idempotency.DecisionFingerprintConflict, nil, idempotency.ErrorCodeKeyReused},
		{"in progress", idempotency.DecisionInProgress, nil, idempotency.ErrorCodeInProgress},
		{"unknown", idempotency.Decision("unknown"), nil, idempotency.ErrorCodeReceiptUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			stub := &executionRepositoryEdgeStub{claim: actionmodel.ActionExecutionClaimResult{Decision: test.decision, Execution: actionmodel.ActionBusinessExecution{LeaseExpiresAt: "invalid"}}, claimErr: test.claimErr}
			_, _, _, err := NewActionExecutionRuntime(stub).BeginRecord(t.Context(), " object ", " record ", " action ", " key ", idempotency.FingerprintInput{Payload: map[string]any{}}, principalmodel.Principal{})
			if err == nil || apperror.CodeOf(err) != test.wantCode {
				t.Fatalf("error=%v code=%q", err, apperror.CodeOf(err))
			}
			if test.claimErr == nil && (stub.request.Execution.WorkspaceID != "default" || stub.request.LeaseOwner == "" || stub.request.Execution.ObjectKey != "object") {
				t.Fatalf("claim request=%#v", stub.request)
			}
		})
	}
	stub := &executionRepositoryEdgeStub{}
	if _, _, _, err := NewActionExecutionRuntime(stub).BeginObject(t.Context(), "object", "action", "key", idempotency.FingerprintInput{Payload: make(chan int)}, principalmodel.Principal{}); err == nil {
		t.Fatal("unencodable fingerprint accepted")
	}
}

func TestActionExecutionRuntimeCompletionEncodingAndReplayEdges(t *testing.T) {
	claim := actionmodel.ActionExecutionClaimResult{Execution: actionmodel.ActionBusinessExecution{ID: "execution", LeaseOwner: "owner", FencingToken: 1}}
	backendErr := errors.New("backend")
	service := NewActionExecutionRuntime(&executionRepositoryEdgeStub{completeErr: backendErr})
	if err := service.Complete(t.Context(), claim, map[string]any{"ok": true}); err == nil || apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("complete error=%v", err)
	}
	if err := service.Complete(t.Context(), claim, map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatal("unencodable completion accepted")
	}
	if err := NewActionExecutionRuntime(&executionRepositoryEdgeStub{}).Complete(t.Context(), actionmodel.ActionExecutionClaimResult{}, map[string]any{"ok": true}); err != nil {
		t.Fatalf("empty completion error=%v", err)
	}

	for _, begin := range []func(*ActionExecutionRuntime) error{
		func(runtime *ActionExecutionRuntime) error {
			_, _, _, err := runtime.BeginObject(t.Context(), "object", "action", "key", idempotency.FingerprintInput{}, principalmodel.Principal{})
			return err
		},
		func(runtime *ActionExecutionRuntime) error {
			_, _, _, err := runtime.BeginRecord(t.Context(), "object", "record", "action", "key", idempotency.FingerprintInput{}, principalmodel.Principal{})
			return err
		},
		func(runtime *ActionExecutionRuntime) error {
			_, _, _, err := runtime.BeginBulk(t.Context(), "object", "action", "key", actionmodel.ActionBulkRequest{}, principalmodel.Principal{})
			return err
		},
	} {
		replay := &executionRepositoryEdgeStub{claim: actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionReplay, Execution: actionmodel.ActionBusinessExecution{Result: map[string]any{"bad": make(chan int)}}}}
		if err := begin(NewActionExecutionRuntime(replay)); err == nil {
			t.Fatal("invalid cached result accepted")
		}
	}
	bulkReplay := &executionRepositoryEdgeStub{claim: actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionReplay, Execution: actionmodel.ActionBusinessExecution{Result: map[string]any{"action_key": "action"}}}}
	result, _, replayed, err := NewActionExecutionRuntime(bulkReplay).BeginBulk(t.Context(), "object", "action", "key", actionmodel.ActionBulkRequest{}, principalmodel.Principal{})
	if err != nil || !replayed || result.Message != "backend.action.idempotent_replay" {
		t.Fatalf("bulk result=%#v replay=%v error=%v", result, replayed, err)
	}
	if _, claim, replayed, err := NewActionExecutionRuntime(&executionRepositoryEdgeStub{}).BeginBulk(t.Context(), "object", "action", "", actionmodel.ActionBulkRequest{}, principalmodel.Principal{}); err != nil || replayed || claim.Decision != idempotency.DecisionAcquired {
		t.Fatalf("bulk without key claim=%#v replay=%v error=%v", claim, replayed, err)
	}
	if _, _, replayed, err := NewActionExecutionRuntime(&executionRepositoryEdgeStub{}).BeginBulk(t.Context(), "object", "action", "key", actionmodel.ActionBulkRequest{
		Data: map[string]any{"bad": make(chan int)},
	}, principalmodel.Principal{}); err == nil || replayed {
		t.Fatalf("unencodable bulk replay=%v error=%v", replayed, err)
	}
	recordReplay := &executionRepositoryEdgeStub{claim: actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionReplay, Execution: actionmodel.ActionBusinessExecution{Result: map[string]any{"action_key": "action"}}}}
	recordResult, _, replayed, err := NewActionExecutionRuntime(recordReplay).BeginRecord(t.Context(), "object", "record", "action", "key", idempotency.FingerprintInput{}, principalmodel.Principal{})
	if err != nil || !replayed || recordResult.Message != "backend.action.idempotent_replay" {
		t.Fatalf("record result=%#v replay=%v error=%v", recordResult, replayed, err)
	}
	for _, execution := range []actionmodel.ActionBusinessExecution{
		{Status: string(idempotency.StatusFailedTerminal), ErrorCode: "business.failed", Result: map[string]any{"bad": make(chan int)}},
		{Status: string(idempotency.StatusFailedTerminal), ErrorCode: "business.failed", Result: map[string]any{}},
		{
			Status: string(idempotency.StatusFailedTerminal), Result: map[string]any{
				"kind":   string(apperror.KindConflict),
				"result": map[string]any{"status": "failed", "error_code": "business.failed"},
			},
		},
		{
			Status: string(idempotency.StatusFailedTerminal), ErrorCode: "business.failed", Result: map[string]any{
				"kind":   string(apperror.KindConflict),
				"result": map[string]any{"status": "succeeded", "error_code": "business.failed"},
			},
		},
		{
			Status: string(idempotency.StatusFailedTerminal), ErrorCode: "business.failed",
			Result: map[string]any{
				"kind": "unknown",
				"result": map[string]any{
					"status": "failed", "error_code": "business.failed",
				},
			},
		},
		{
			Status: string(idempotency.StatusFailedTerminal), ErrorCode: "business.failed",
			Result: map[string]any{
				"kind": string(apperror.KindConflict),
				"result": map[string]any{
					"status": "failed", "error_code": "different.failed",
				},
			},
		},
		{
			Status: string(idempotency.StatusFailedTerminal), ErrorCode: "business.failed",
			Result: map[string]any{
				"kind": string(apperror.KindConflict),
				"result": map[string]any{
					"status": "failed", "error_code": "business.failed", "retryable": true,
				},
			},
		},
	} {
		failedReplay := &executionRepositoryEdgeStub{claim: actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionReplay, Execution: execution}}
		_, _, replayed, err := NewActionExecutionRuntime(failedReplay).BeginObject(t.Context(), "object", "action", "key", idempotency.FingerprintInput{}, principalmodel.Principal{})
		if replayed || apperror.CodeOf(err) != idempotency.ErrorCodeReceiptUnavailable {
			t.Fatalf("malformed failed replay=%v error=%v", replayed, err)
		}
	}
}

func TestActionExecutionRuntimeTransactionEdges(t *testing.T) {
	backendErr := errors.New("backend")
	var nilService *ActionExecutionRuntime
	if _, err := nilService.BeginTransaction(t.Context()); apperror.CodeOf(err) != idempotency.ErrorCodeReceiptUnavailable {
		t.Fatalf("nil runtime transaction error=%v", err)
	}
	if _, err := NewActionExecutionRuntime(nil).BeginTransaction(t.Context()); apperror.CodeOf(err) != idempotency.ErrorCodeReceiptUnavailable {
		t.Fatalf("nil repository transaction error=%v", err)
	}
	if _, err := NewActionExecutionRuntime(&executionRepositoryEdgeStub{}).BeginTransaction(t.Context()); apperror.CodeOf(err) != "backend.action.transaction_unavailable" {
		t.Fatalf("unsupported transaction error=%v", err)
	}
	failing := &executionTransactionalRepositoryEdgeStub{
		executionRepositoryEdgeStub: &executionRepositoryEdgeStub{}, beginErr: backendErr,
	}
	if _, err := NewActionExecutionRuntime(failing).BeginTransaction(t.Context()); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("begin transaction error=%v", err)
	}
	transaction := &executionTransactionEdgeStub{}
	repository := &executionTransactionalRepositoryEdgeStub{
		executionRepositoryEdgeStub: &executionRepositoryEdgeStub{}, transaction: transaction,
	}
	service := NewActionExecutionRuntime(repository)
	started, err := service.BeginTransaction(t.Context())
	if err != nil || started != transaction || !service.TransactionAvailable() {
		t.Fatalf("started=%T available=%v error=%v", started, service.TransactionAvailable(), err)
	}
	if nilService.TransactionAvailable() || NewActionExecutionRuntime(nil).TransactionAvailable() || NewActionExecutionRuntime(&executionRepositoryEdgeStub{}).TransactionAvailable() {
		t.Fatal("transaction availability accepted an unsupported runtime")
	}
	claim := actionmodel.ActionExecutionClaimResult{Execution: actionmodel.ActionBusinessExecution{ID: "execution", LeaseOwner: "owner", FencingToken: 2}}
	if err := service.CommitTransaction(t.Context(), nil, claim, map[string]any{}, nil, nil); apperror.CodeOf(err) != idempotency.ErrorCodeReceiptUnavailable {
		t.Fatalf("nil commit transaction error=%v", err)
	}
	if err := service.CommitTransaction(t.Context(), transaction, actionmodel.ActionExecutionClaimResult{}, map[string]any{}, nil, nil); apperror.CodeOf(err) != idempotency.ErrorCodeReceiptUnavailable {
		t.Fatalf("empty claim transaction error=%v", err)
	}
	if err := service.CommitTransaction(t.Context(), transaction, claim, map[string]any{"bad": make(chan int)}, nil, nil); err == nil {
		t.Fatal("unencodable transaction result accepted")
	}
	transaction.commitErr = backendErr
	if err := service.CommitTransaction(t.Context(), transaction, claim, map[string]any{"ok": true}, nil, nil); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("commit transaction error=%v", err)
	}
	transaction.commitErr = nil
	if err := service.CommitTransaction(t.Context(), transaction, claim, map[string]any{"ok": true}, nil, []auditmodel.AuditEvent{{ID: "audit"}}); err != nil {
		t.Fatal(err)
	}
	if transaction.completion.ExecutionID != "execution" || len(transaction.completion.AuditEvents) != 1 {
		t.Fatalf("completion=%+v", transaction.completion)
	}
}

func TestActionExecutionRuntimeFailTransactionEdges(t *testing.T) {
	backendErr := errors.New("backend")
	claim := actionmodel.ActionExecutionClaimResult{Execution: actionmodel.ActionBusinessExecution{ID: "execution", LeaseOwner: "owner", FencingToken: 1}}
	result := actionmodel.ActionInvocationResult{Status: "failed", ErrorCode: "business.failed"}
	failure := apperror.New(apperror.KindConflict, result.ErrorCode, nil, nil)
	audits := []auditmodel.AuditEvent{{ID: "audit"}}

	for _, runtime := range []*ActionExecutionRuntime{nil, NewActionExecutionRuntime(nil), NewActionExecutionRuntime(&executionRepositoryEdgeStub{})} {
		if err := runtime.Fail(t.Context(), actionmodel.ActionExecutionClaimResult{}, result, failure, audits); err != nil {
			t.Fatalf("empty failure error=%v", err)
		}
	}
	repository := &executionTransactionalRepositoryEdgeStub{
		executionRepositoryEdgeStub: &executionRepositoryEdgeStub{}, beginErr: backendErr,
	}
	if err := NewActionExecutionRuntime(repository).Fail(t.Context(), claim, result, failure, audits); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("begin failed transaction error=%v", err)
	}
	transaction := &executionTransactionEdgeStub{commitErr: backendErr}
	repository.beginErr, repository.transaction = nil, transaction
	if err := NewActionExecutionRuntime(repository).Fail(t.Context(), claim, result, failure, audits); apperror.CodeOf(err) != "backend.internal" || !transaction.rollback {
		t.Fatalf("commit failed transaction error=%v rollback=%v", err, transaction.rollback)
	}
	transaction.commitErr, transaction.rollback = nil, false
	if err := NewActionExecutionRuntime(repository).Fail(t.Context(), claim, result, failure, audits); err != nil {
		t.Fatal(err)
	}
	if err := NewActionExecutionRuntime(repository).Fail(t.Context(), claim, result, failure, nil); err != nil {
		t.Fatalf("transactional repository without audits error=%v", err)
	}
	if err := NewActionExecutionRuntime(repository).Fail(t.Context(), claim, actionmodel.ActionInvocationResult{
		Status: "failed", ErrorCode: "business.failed", Output: map[string]any{"bad": make(chan int)},
	}, failure, nil); err == nil {
		t.Fatal("unencodable failure result accepted")
	}
	nonTransactional := &executionRepositoryEdgeStub{completeErr: backendErr}
	if err := NewActionExecutionRuntime(nonTransactional).Fail(t.Context(), claim, result, failure, nil); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("complete failed execution error=%v", err)
	}
}

func TestActionExecutionRuntimeValueHelpers(t *testing.T) {
	var nilService *ActionExecutionRuntime
	if nilService.Available() || NewActionExecutionRuntime(nil).Available() || !NewActionExecutionRuntime(&executionRepositoryEdgeStub{}).Available() {
		t.Fatal("runtime availability changed")
	}
	if actionRetryAfter("invalid") != "1" || actionRetryAfter(time.Now().Add(-time.Minute).Format(time.RFC3339Nano)) != "1" {
		t.Fatal("invalid/past retry normalization changed")
	}
	future := actionRetryAfter(time.Now().Add(1500 * time.Millisecond).Format(time.RFC3339Nano))
	seconds, err := strconv.Atoi(future)
	if err != nil || seconds < 1 || seconds > 2 {
		t.Fatalf("future retry=%q error=%v", future, err)
	}
	if _, err := encodeExecutionResult("scalar"); err == nil {
		t.Fatal("scalar result accepted")
	}
	if err := decodeExecutionResult(map[string]any{"value": 1}, make(chan int)); err == nil {
		t.Fatal("invalid decode target accepted")
	}
	if actionWorkspaceID(principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: " workspace "}}) != "workspace" || actionWorkspaceID(principalmodel.Principal{}) != "default" {
		t.Fatal("workspace normalization changed")
	}
	if apperror.CodeOf(executionServiceError("operation", backendSentinelError{})) != "backend.internal" {
		t.Fatal("execution service error changed")
	}
	for errorValue, wantCode := range map[error]string{
		context.Canceled:         "backend.action.cancelled",
		context.DeadlineExceeded: "backend.action.timeout",
		mutation.PolicyConflict("business.conflict", "record", "one", "status"):                    "business.conflict",
		mutation.MutationConflict("record", "one", mutation.MutationConflictUnique, nil):           "backend.mutation.unique_conflict",
		mutation.MutationConflict("record", "one", mutation.MutationConflictOptimistic, nil):       "backend.record.version_conflict",
		mutation.TransactionTransient("record", "one", mutation.TransactionTransientDeadlock, nil): "backend.transaction.deadlock",
		mutation.TransactionCommitUnknown("record", "one", backendSentinelError{}):                 mutation.TransactionCommitUnknownCode,
	} {
		if got := apperror.CodeOf(executionServiceError("operation", errorValue)); got != wantCode {
			t.Fatalf("error=%v code=%q want=%q", errorValue, got, wantCode)
		}
	}
	for kind, want := range map[apperror.ErrorKind]int{
		apperror.KindBadRequest: 400, apperror.KindForbidden: 403, apperror.KindNotFound: 404,
		apperror.KindConflict: 409, apperror.KindRateLimited: 429, apperror.KindUnavailable: 503,
		apperror.KindInternal: 500,
	} {
		if got := actionFailureResponseStatus(kind); got != want {
			t.Fatalf("failure response status kind=%q got=%d want=%d", kind, got, want)
		}
		if !actionFailureKindValid(kind) {
			t.Fatalf("valid failure kind rejected: %q", kind)
		}
	}
	if actionFailureKindValid("") || actionFailureKindValid("unknown") {
		t.Fatal("invalid failure kind accepted")
	}
}

type backendSentinelError struct{}

func (backendSentinelError) Error() string { return "backend" }
