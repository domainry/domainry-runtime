package runtime

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

type mutationExecutionStoreStub struct {
	claim            recordmodel.RecordMutationClaimResult
	beginErr         error
	commitErr        error
	completeErr      error
	beginRequests    []recordmodel.RecordMutationClaimRequest
	commitValue      transactionmodel.RecordMutationCommit
	commitCompletion recordmodel.RecordMutationCompletion
	completion       recordmodel.RecordMutationCompletion
}

type mutationExecutionLookupStore struct {
	*mutationExecutionStoreStub
	execution recordmodel.RecordMutationExecution
	found     bool
	err       error
	scope     recordmodel.RecordMutationExecution
}

func (s *mutationExecutionLookupStore) FindRecordMutationExecution(_ context.Context, scope recordmodel.RecordMutationExecution) (recordmodel.RecordMutationExecution, bool, error) {
	s.scope = scope
	return s.execution, s.found, s.err
}

func (s *mutationExecutionStoreStub) TryBeginRecordMutation(_ context.Context, request recordmodel.RecordMutationClaimRequest) (recordmodel.RecordMutationClaimResult, error) {
	s.beginRequests = append(s.beginRequests, request)
	return s.claim, s.beginErr
}

func (s *mutationExecutionStoreStub) CommitRecordMutationExecution(_ context.Context, commit transactionmodel.RecordMutationCommit, completion recordmodel.RecordMutationCompletion) (recordmodel.RecordMutationExecution, error) {
	s.commitValue, s.commitCompletion = commit, completion
	return completionExecution(completion), s.commitErr
}

func (s *mutationExecutionStoreStub) CompleteRecordMutationExecution(_ context.Context, completion recordmodel.RecordMutationCompletion) (recordmodel.RecordMutationExecution, error) {
	s.completion = completion
	return completionExecution(completion), s.completeErr
}

func completionExecution(completion recordmodel.RecordMutationCompletion) recordmodel.RecordMutationExecution {
	return recordmodel.RecordMutationExecution{ID: completion.ExecutionID, WorkspaceID: completion.WorkspaceID}
}

func TestBeginCreateValidatesDependenciesFingerprintAndClaimFailure(t *testing.T) {
	var nilRuntime *RecordMutationExecutionRuntime
	_, _, _, err := nilRuntime.BeginCreate(t.Context(), "customer", "key", nil, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil)

	runtime := NewRecordMutationExecutionRuntime(nil)
	_, _, _, err = runtime.BeginCreate(t.Context(), "customer", "key", nil, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil)
	_, _, _, err = runtime.BeginCreate(t.Context(), "customer", " ", nil, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, map[string]string{"use_case": "record.create"})

	store := &mutationExecutionStoreStub{}
	runtime = NewRecordMutationExecutionRuntime(store)
	_, _, _, err = runtime.BeginCreate(t.Context(), "customer", "key", map[string]any{"invalid": make(chan int)}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "fingerprint record create"})

	claimErr := errors.New("claim failed")
	store.beginErr = claimErr
	_, _, _, err = runtime.BeginCreate(t.Context(), " customer ", " key ", map[string]any{"name": "Alice"}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "claim record create"})
	if !errors.Is(err, claimErr) {
		t.Fatalf("claim error was not preserved: %v", err)
	}
	request := store.beginRequests[len(store.beginRequests)-1]
	if request.Execution.WorkspaceID != "default" || request.Execution.ObjectKey != "customer" || request.Execution.IdempotencyKey != "key" || !strings.HasPrefix(request.LeaseOwner, "req_") || request.LeaseTTL != recordMutationLeaseTTL || request.RequestFingerprint == "" {
		t.Fatalf("default claim request=%+v", request)
	}
}

func TestBeginCreateClaimDecisionMatrixAndOwnerPrecedence(t *testing.T) {
	replayRecord := recordmodel.Record{ID: "customer-1"}
	for _, test := range []struct {
		name       string
		decision   idempotency.Decision
		wantReplay bool
		wantCode   string
	}{
		{name: "acquired", decision: idempotency.DecisionAcquired},
		{name: "replay", decision: idempotency.DecisionReplay, wantReplay: true},
		{name: "fingerprint conflict", decision: idempotency.DecisionFingerprintConflict, wantCode: idempotency.ErrorCodeKeyReused},
		{name: "in progress", decision: idempotency.DecisionInProgress, wantCode: idempotency.ErrorCodeInProgress},
		{name: "unknown", decision: idempotency.Decision("unknown"), wantCode: idempotency.ErrorCodeReceiptUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &mutationExecutionStoreStub{claim: recordmodel.RecordMutationClaimResult{Decision: test.decision, Execution: recordmodel.RecordMutationExecution{Result: replayRecord, LeaseExpiresAt: "invalid"}}}
			runtime := NewRecordMutationExecutionRuntime(store)
			result, claim, replay, err := runtime.BeginCreate(requestcontext.WithRequestID(t.Context(), "context-owner"), "customer", "key", map[string]any{"name": "Alice"}, principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace", UserID: "user"}, RequestID: "principal-owner"})
			if claim.Decision != test.decision || replay != test.wantReplay || errorCodeFromRecordRuntime(err) != test.wantCode {
				t.Fatalf("result=%#v claim=%#v replay=%v err=%v", result, claim, replay, err)
			}
			if test.wantReplay && result.ID != replayRecord.ID {
				t.Fatalf("replay result=%#v", result)
			}
			request := store.beginRequests[0]
			if request.LeaseOwner != "principal-owner" || request.Execution.WorkspaceID != "workspace" || request.Execution.ActorID != "user" {
				t.Fatalf("principal claim request=%+v", request)
			}
		})
	}

	store := &mutationExecutionStoreStub{claim: recordmodel.RecordMutationClaimResult{Decision: idempotency.DecisionAcquired}}
	_, _, _, err := NewRecordMutationExecutionRuntime(store).BeginCreate(requestcontext.WithRequestID(t.Context(), "context-owner"), "customer", "key", nil, principalmodel.Principal{})
	if err != nil || store.beginRequests[0].LeaseOwner != "context-owner" {
		t.Fatalf("context owner request=%+v err=%v", store.beginRequests[0], err)
	}
}

func TestBeginUpdateScopesReceiptToTargetAndReplaysResult(t *testing.T) {
	replayRecord := recordmodel.Record{ID: "member-1", Data: map[string]any{"status": "blacklisted"}}
	for _, test := range []struct {
		name       string
		decision   idempotency.Decision
		wantReplay bool
		wantCode   string
	}{
		{name: "acquired", decision: idempotency.DecisionAcquired},
		{name: "replay", decision: idempotency.DecisionReplay, wantReplay: true},
		{name: "fingerprint conflict", decision: idempotency.DecisionFingerprintConflict, wantCode: idempotency.ErrorCodeKeyReused},
		{name: "in progress", decision: idempotency.DecisionInProgress, wantCode: idempotency.ErrorCodeInProgress},
		{name: "unknown", decision: idempotency.Decision("unknown"), wantCode: idempotency.ErrorCodeReceiptUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &mutationExecutionStoreStub{claim: recordmodel.RecordMutationClaimResult{
				Decision: test.decision,
				Execution: recordmodel.RecordMutationExecution{
					Result:         replayRecord,
					LeaseExpiresAt: time.Now().Add(time.Second).Format(time.RFC3339Nano),
				},
			}}
			result, claim, replay, err := NewRecordMutationExecutionRuntime(store).BeginUpdate(
				t.Context(), " member_profile ", " member-1 ", " blacklist-1 ",
				map[string]any{"status": "blacklisted"},
				principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace", UserID: "operator"}, RequestID: "request"},
			)
			if claim.Decision != test.decision || replay != test.wantReplay || errorCodeFromRecordRuntime(err) != test.wantCode {
				t.Fatalf("result=%#v claim=%#v replay=%v err=%v", result, claim, replay, err)
			}
			if test.wantReplay && result.ID != replayRecord.ID {
				t.Fatalf("replay result=%#v", result)
			}
			request := store.beginRequests[0]
			if request.Execution.Operation != "update" ||
				request.Execution.ObjectKey != "member_profile" ||
				request.Execution.TargetID != "member-1" ||
				request.Execution.IdempotencyKey != "blacklist-1" ||
				request.Execution.ActorID != "operator" ||
				request.RequestFingerprint == "" {
				t.Fatalf("update claim request=%+v", request)
			}
		})
	}
}

func TestBeginImportDecisionReplayAndDecodeMatrix(t *testing.T) {
	var nilRuntime *RecordMutationExecutionRuntime
	_, _, _, err := nilRuntime.BeginImport(t.Context(), "customer", "key", []byte("name\nAlice"), principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil)
	_, _, _, err = NewRecordMutationExecutionRuntime(nil).BeginImport(t.Context(), "customer", "key", nil, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil)
	_, _, _, err = NewRecordMutationExecutionRuntime(&mutationExecutionStoreStub{}).BeginImport(t.Context(), "customer", " ", nil, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, map[string]string{"use_case": "record.import"})

	for _, test := range []struct {
		decision idempotency.Decision
		code     string
		replay   bool
	}{
		{decision: idempotency.DecisionAcquired},
		{decision: idempotency.DecisionFingerprintConflict, code: idempotency.ErrorCodeKeyReused},
		{decision: idempotency.DecisionInProgress, code: idempotency.ErrorCodeInProgress},
		{decision: idempotency.Decision("unknown"), code: idempotency.ErrorCodeReceiptUnavailable},
	} {
		store := &mutationExecutionStoreStub{claim: recordmodel.RecordMutationClaimResult{Decision: test.decision, Execution: recordmodel.RecordMutationExecution{LeaseExpiresAt: time.Now().Add(time.Second).Format(time.RFC3339Nano)}}}
		_, _, replay, err := NewRecordMutationExecutionRuntime(store).BeginImport(t.Context(), "customer", "key", []byte("csv"), principalmodel.Principal{})
		if replay != test.replay || errorCodeFromRecordRuntime(err) != test.code {
			t.Fatalf("decision=%q replay=%v err=%v", test.decision, replay, err)
		}
	}
	ownerStore := &mutationExecutionStoreStub{claim: recordmodel.RecordMutationClaimResult{Decision: idempotency.DecisionAcquired}}
	_, _, _, err = NewRecordMutationExecutionRuntime(ownerStore).BeginImport(requestcontext.WithRequestID(t.Context(), "context-owner"), "customer", "key", []byte("csv"), principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}, RequestID: "principal-owner"})
	if err != nil || ownerStore.beginRequests[0].LeaseOwner != "principal-owner" || ownerStore.beginRequests[0].Execution.WorkspaceID != "workspace" {
		t.Fatalf("principal import owner request=%+v err=%v", ownerStore.beginRequests[0], err)
	}
	contextOwnerStore := &mutationExecutionStoreStub{claim: recordmodel.RecordMutationClaimResult{Decision: idempotency.DecisionAcquired}}
	_, _, _, err = NewRecordMutationExecutionRuntime(contextOwnerStore).BeginImport(requestcontext.WithRequestID(t.Context(), "context-owner"), "customer", "key", []byte("csv"), principalmodel.Principal{})
	if err != nil || contextOwnerStore.beginRequests[0].LeaseOwner != "context-owner" {
		t.Fatalf("context import owner request=%+v err=%v", contextOwnerStore.beginRequests[0], err)
	}

	claimErr := errors.New("claim failed")
	store := &mutationExecutionStoreStub{beginErr: claimErr}
	_, _, _, err = NewRecordMutationExecutionRuntime(store).BeginImport(t.Context(), "customer", "key", []byte("csv"), principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "claim record operation"})

	runtime := NewRecordMutationExecutionRuntime(&mutationExecutionStoreStub{})
	_, _, err = runtime.beginOperation(t.Context(), "import", "customer", "key", idempotency.FingerprintInput{Payload: make(chan int)}, principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "fingerprint record operation"})

	for _, test := range []struct {
		name      string
		operation map[string]any
		want      recordmodel.RecordImportApplyResult
		wantError bool
	}{
		{name: "success", operation: map[string]any{"object_key": "customer", "created": 2.0}, want: recordmodel.RecordImportApplyResult{ObjectKey: "customer", Created: 2}},
		{name: "marshal", operation: map[string]any{"invalid": make(chan int)}, wantError: true},
		{name: "unmarshal", operation: map[string]any{"created": "invalid"}, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &mutationExecutionStoreStub{claim: recordmodel.RecordMutationClaimResult{Decision: idempotency.DecisionReplay, Execution: recordmodel.RecordMutationExecution{OperationResult: test.operation}}}
			result, _, replay, err := NewRecordMutationExecutionRuntime(store).BeginImport(t.Context(), "customer", "key", []byte("csv"), principalmodel.Principal{})
			if (err != nil) != test.wantError || replay != !test.wantError || result.ObjectKey != test.want.ObjectKey || result.Created != test.want.Created {
				t.Fatalf("result=%#v replay=%v err=%v", result, replay, err)
			}
		})
	}
}

func TestReplayImportLookupAndDecodeMatrix(t *testing.T) {
	var nilRuntime *RecordMutationExecutionRuntime
	if _, found, err := nilRuntime.ReplayImport(t.Context(), "customer", "key", []byte("csv"), principalmodel.Principal{}); err != nil || found {
		t.Fatalf("nil runtime found=%v err=%v", found, err)
	}
	if _, found, err := NewRecordMutationExecutionRuntime(nil).ReplayImport(t.Context(), "customer", "key", []byte("csv"), principalmodel.Principal{}); err != nil || found {
		t.Fatalf("nil repository found=%v err=%v", found, err)
	}
	if _, found, err := NewRecordMutationExecutionRuntime(&mutationExecutionStoreStub{}).ReplayImport(t.Context(), "customer", "key", []byte("csv"), principalmodel.Principal{}); err != nil || found {
		t.Fatalf("repository without lookup found=%v err=%v", found, err)
	}

	lookupErr := errors.New("lookup failed")
	store := &mutationExecutionLookupStore{mutationExecutionStoreStub: &mutationExecutionStoreStub{}, err: lookupErr}
	_, _, err := NewRecordMutationExecutionRuntime(store).ReplayImport(t.Context(), " customer ", " key ", []byte("csv"), principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "lookup record import replay"})
	if !errors.Is(err, lookupErr) || store.scope.WorkspaceID != "default" || store.scope.ObjectKey != "customer" || store.scope.IdempotencyKey != "key" {
		t.Fatalf("scope=%+v err=%v", store.scope, err)
	}

	for _, test := range []struct {
		name        string
		found       bool
		status      string
		payload     map[string]any
		fingerprint string
		wantCode    string
		wantFound   bool
	}{
		{name: "missing"},
		{name: "processing", found: true, status: string(idempotency.StatusProcessing)},
		{name: "fingerprint conflict", found: true, status: string(idempotency.StatusSucceeded), fingerprint: "different", wantCode: idempotency.ErrorCodeKeyReused},
		{name: "marshal failure", found: true, status: string(idempotency.StatusSucceeded), payload: map[string]any{"invalid": make(chan int)}, wantCode: "backend.internal"},
		{name: "unmarshal failure", found: true, status: string(idempotency.StatusSucceeded), payload: map[string]any{"created": "invalid"}, wantCode: "backend.internal"},
		{name: "success", found: true, status: string(idempotency.StatusSucceeded), payload: map[string]any{"object_key": "customer", "created": 2.0}, wantFound: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fingerprint := test.fingerprint
			if fingerprint == "" {
				fingerprint, _ = idempotency.Fingerprint(idempotency.FingerprintInput{UseCase: "record.import", ResourceType: "record_collection", TargetID: "customer", Payload: []byte("csv")})
			}
			store := &mutationExecutionLookupStore{mutationExecutionStoreStub: &mutationExecutionStoreStub{}, found: test.found, execution: recordmodel.RecordMutationExecution{Status: test.status, RequestFingerprint: fingerprint, OperationResult: test.payload}}
			result, found, err := NewRecordMutationExecutionRuntime(store).ReplayImport(t.Context(), "customer", "key", []byte("csv"), principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}})
			if found != test.wantFound || errorCodeFromRecordRuntime(err) != test.wantCode || result.Created != map[bool]int{true: 2}[test.wantFound] {
				t.Fatalf("result=%+v found=%v err=%v", result, found, err)
			}
		})
	}
}

func TestCommitAndCompleteOperationValidateAndForwardCompletion(t *testing.T) {
	claim := recordmodel.RecordMutationClaimResult{Execution: recordmodel.RecordMutationExecution{ID: "execution-1", WorkspaceID: "workspace", LeaseOwner: "owner", FencingToken: 7}}
	commit := transactionmodel.RecordMutationCommit{Operation: "create", Record: recordmodel.Record{ID: "record-1"}}
	var nilRuntime *RecordMutationExecutionRuntime
	assertRecordAppError(t, nilRuntime.Commit(t.Context(), claim, commit), apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil)
	assertRecordAppError(t, NewRecordMutationExecutionRuntime(nil).Commit(t.Context(), claim, commit), apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil)
	assertRecordAppError(t, nilRuntime.CompleteOperation(t.Context(), claim, nil), apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil)
	assertRecordAppError(t, NewRecordMutationExecutionRuntime(nil).CompleteOperation(t.Context(), claim, nil), apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil)

	commitErr := errors.New("commit failed")
	store := &mutationExecutionStoreStub{commitErr: commitErr}
	runtime := NewRecordMutationExecutionRuntime(store)
	err := runtime.Commit(t.Context(), claim, commit)
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "commit record mutation execution"})
	if !errors.Is(err, commitErr) {
		t.Fatalf("commit error not preserved: %v", err)
	}
	store.commitErr = nil
	if err := runtime.Commit(t.Context(), claim, commit); err != nil {
		t.Fatal(err)
	}
	if store.commitCompletion.ExecutionID != "execution-1" || store.commitCompletion.LeaseOwner != "owner" || store.commitCompletion.FencingToken != 7 || store.commitCompletion.Result.(recordmodel.Record).ID != "record-1" || !store.commitCompletion.ExpiresAt.After(store.commitCompletion.Now) {
		t.Fatalf("commit completion=%+v", store.commitCompletion)
	}

	completeErr := errors.New("complete failed")
	store.completeErr = completeErr
	err = runtime.CompleteOperation(t.Context(), claim, map[string]any{"created": 1})
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "complete record operation"})
	store.completeErr = nil
	result := map[string]any{"created": 1}
	if err := runtime.CompleteOperation(t.Context(), claim, result); err != nil {
		t.Fatal(err)
	}
	if store.completion.ExecutionID != "execution-1" || store.completion.Result.(map[string]any)["created"] != 1 || !store.completion.ExpiresAt.After(store.completion.Now) {
		t.Fatalf("operation completion=%+v", store.completion)
	}
}

func TestRecordMutationRetryAfterBoundsAndParses(t *testing.T) {
	if got := recordMutationRetryAfter("invalid"); got != "1" {
		t.Fatalf("invalid retry-after=%q", got)
	}
	if got := recordMutationRetryAfter(time.Now().Add(-time.Minute).Format(time.RFC3339Nano)); got != "1" {
		t.Fatalf("expired retry-after=%q", got)
	}
	got := recordMutationRetryAfter(time.Now().Add(3 * time.Second).Format(time.RFC3339Nano))
	seconds, err := strconv.Atoi(got)
	if err != nil || seconds < 1 || seconds > 3 {
		t.Fatalf("future retry-after=%q err=%v", got, err)
	}
}

func errorCodeFromRecordRuntime(err error) string {
	if err == nil {
		return ""
	}
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return appErr.Code
	}
	return ""
}
