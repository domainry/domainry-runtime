package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	pathpkg "path"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	dispatchcontract "github.com/domainry/domainry-runtime/runtime/domain/dispatch/contract"
	dispatchmodel "github.com/domainry/domainry-runtime/runtime/domain/dispatch/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

var _ dispatchcontract.CallbackReceiptStore = (*CallbackReceiptStore)(nil)

const (
	dispatchCallbackOwner     = "dispatch"
	dispatchCallbackKind      = "dispatch.callback"
	dispatchCallbackActionKey = "runtime.dispatch.callback"
)

type CallbackReceiptStore struct {
	store *database.RuntimeStore
}

func NewCallbackReceiptStore(store *database.RuntimeStore) *CallbackReceiptStore {
	return &CallbackReceiptStore{store: store}
}

func (s *CallbackReceiptStore) TryBeginCallback(ctx context.Context, request dispatchmodel.CallbackClaimRequest) (dispatchmodel.CallbackClaimResult, error) {
	if s == nil || s.store == nil || s.store.DB() == nil {
		return dispatchmodel.CallbackClaimResult{}, fmt.Errorf("dispatch callback receipt store is unavailable")
	}
	for attempt := 0; attempt < 50; attempt++ {
		claim, err := s.tryBeginCallbackOnce(ctx, request)
		if err == nil || !s.store.IsTransientError(err) {
			return claim, err
		}
		if err := callbackClaimBackoff(ctx, attempt); err != nil {
			return dispatchmodel.CallbackClaimResult{}, err
		}
	}
	return dispatchmodel.CallbackClaimResult{}, fmt.Errorf("claim dispatch callback: database remained busy after retry")
}

func (s *CallbackReceiptStore) tryBeginCallbackOnce(ctx context.Context, request dispatchmodel.CallbackClaimRequest) (dispatchmodel.CallbackClaimResult, error) {
	receipt, now, leaseTTL, err := normalizeCallbackClaim(request)
	if err != nil {
		return dispatchmodel.CallbackClaimResult{}, err
	}
	receipt.ID = callbackReceiptID(receipt)
	receipt.Status = string(idempotency.StatusProcessing)
	receipt.LeaseExpiresAt = now.Add(leaseTTL).Format(time.RFC3339Nano)
	receipt.FencingToken = 1
	receipt.CreatedAt, receipt.UpdatedAt = now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)
	ledger := sharedoperation.NewSQLStore(s.store.DB(), s.store.SQLRenderer)
	if inserted, insertErr := ledger.InsertRecord(ctx, callbackRecord(receipt)); insertErr == nil && inserted {
		s.store.ObserveIdempotency(ctx, receipt.WorkspaceID, dispatchmodel.CallbackExecutionUseCase, idempotency.OutcomeAcquired)
		return dispatchmodel.CallbackClaimResult{Decision: idempotency.DecisionAcquired, Receipt: receipt}, nil
	} else {
		current, found, findErr := s.findCallbackByScope(ctx, receipt)
		if findErr != nil {
			return dispatchmodel.CallbackClaimResult{}, findErr
		}
		if !found {
			return dispatchmodel.CallbackClaimResult{}, database.MutationConstraintError(insertErr, "dispatch_callback_receipt", receipt.ID, mutation.MutationConflictIdempotency)
		}
		decision := idempotency.Classify(idempotency.ReceiptState{
			Status: idempotency.Status(current.Status), Fingerprint: current.BodySHA256, Lease: callbackReceiptLease(current),
		}, receipt.BodySHA256, now)
		if decision != idempotency.DecisionAcquired {
			s.store.ObserveIdempotency(ctx, receipt.WorkspaceID, dispatchmodel.CallbackExecutionUseCase, idempotency.OutcomeForDecision(decision, false))
			return dispatchmodel.CallbackClaimResult{Decision: decision, Receipt: current}, nil
		}
		return s.reclaimCallback(ctx, receipt, now, leaseTTL)
	}
}

func (s *CallbackReceiptStore) reclaimCallback(ctx context.Context, requested dispatchmodel.CallbackReceipt, now time.Time, leaseTTL time.Duration) (dispatchmodel.CallbackClaimResult, error) {
	updatedAt, leaseExpiresAt := now.Format(time.RFC3339Nano), now.Add(leaseTTL).Format(time.RFC3339Nano)
	ledger := sharedoperation.NewSQLStore(s.store.DB(), s.store.SQLRenderer)
	status, resultJSON, empty := string(idempotency.StatusProcessing), json.RawMessage(`{}`), ""
	changes := sharedoperation.RecordChanges{
		Status: &status, ResultJSON: &resultJSON, ErrorCode: &empty, FailureClass: &empty,
		LeaseOwner: &requested.LeaseOwner, LeaseExpiresAt: &leaseExpiresAt, IncrementFencingToken: true,
		UpdatedAt: &updatedAt, ExpiresAt: &empty,
	}
	filter := callbackRecordFilter(requested)
	filter.RequestFingerprint = requested.BodySHA256
	filter.LeaseExpiresAtOrBefore = updatedAt
	filter.ReclaimableStatus = string(idempotency.StatusFailedRetryable)
	filter.ExpiredLeaseStatus = string(idempotency.StatusProcessing)
	changed, err := ledger.PatchRecord(ctx, filter, changes)
	if err != nil {
		return dispatchmodel.CallbackClaimResult{}, err
	}
	current, found, err := s.findCallbackByScope(ctx, requested)
	if err != nil {
		return dispatchmodel.CallbackClaimResult{}, err
	}
	if !found {
		return dispatchmodel.CallbackClaimResult{}, fmt.Errorf("dispatch callback receipt disappeared during reclaim")
	}
	if changed {
		s.store.ObserveIdempotency(ctx, requested.WorkspaceID, dispatchmodel.CallbackExecutionUseCase, idempotency.OutcomeReclaimed)
		return dispatchmodel.CallbackClaimResult{Decision: idempotency.DecisionAcquired, Receipt: current}, nil
	}
	s.store.ObserveIdempotency(ctx, requested.WorkspaceID, dispatchmodel.CallbackExecutionUseCase, idempotency.OutcomeInProgress)
	return dispatchmodel.CallbackClaimResult{Decision: idempotency.DecisionInProgress, Receipt: current}, nil
}

func (s *CallbackReceiptStore) HeartbeatCallback(ctx context.Context, heartbeat dispatchmodel.CallbackHeartbeat) (bool, error) {
	if s == nil || s.store == nil || s.store.DB() == nil {
		return false, fmt.Errorf("dispatch callback receipt store is unavailable")
	}
	now := heartbeat.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if heartbeat.LeaseTTL <= 0 {
		return false, fmt.Errorf("dispatch callback heartbeat lease TTL is required")
	}
	nowText := now.Format(time.RFC3339Nano)
	leaseExpiresAt := now.Add(heartbeat.LeaseTTL).Format(time.RFC3339Nano)
	token := heartbeat.FencingToken
	return sharedoperation.NewSQLStore(s.store.DB(), s.store.SQLRenderer).PatchRecord(ctx, sharedoperation.RecordFilter{
		WorkspaceID: strings.TrimSpace(heartbeat.WorkspaceID), ID: strings.TrimSpace(heartbeat.ReceiptID),
		Owner: dispatchCallbackOwner, Kind: dispatchCallbackKind, Status: string(idempotency.StatusProcessing),
		LeaseOwner: strings.TrimSpace(heartbeat.LeaseOwner), FencingToken: &token, LeaseExpiresAfter: nowText,
	}, sharedoperation.RecordChanges{LeaseExpiresAt: &leaseExpiresAt, UpdatedAt: &nowText})
}

func (s *CallbackReceiptStore) CompleteCallback(ctx context.Context, completion dispatchmodel.CallbackCompletion) error {
	if s == nil || s.store == nil || s.store.DB() == nil {
		return fmt.Errorf("dispatch callback receipt store is unavailable")
	}
	now := normalizedCallbackTime(completion.Now)
	resultJSON, err := json.Marshal(callbackOperationResult{DownstreamID: strings.TrimSpace(completion.DownstreamID), DownstreamOwner: strings.TrimSpace(completion.DownstreamOwner), DownstreamStatus: strings.TrimSpace(completion.DownstreamStatus)})
	if err != nil {
		return fmt.Errorf("encode dispatch callback completion: %w", err)
	}
	status, empty := string(idempotency.StatusSucceeded), ""
	rawResult := json.RawMessage(resultJSON)
	updatedAt, expiresAt := now.Format(time.RFC3339Nano), completion.ExpiresAt.UTC().Format(time.RFC3339Nano)
	token := completion.FencingToken
	changed, err := sharedoperation.NewSQLStore(s.store.DB(), s.store.SQLRenderer).PatchRecord(ctx, sharedoperation.RecordFilter{
		WorkspaceID: strings.TrimSpace(completion.WorkspaceID), ID: strings.TrimSpace(completion.ReceiptID),
		Owner: dispatchCallbackOwner, Kind: dispatchCallbackKind, Status: string(idempotency.StatusProcessing),
		LeaseOwner: strings.TrimSpace(completion.LeaseOwner), FencingToken: &token,
	}, sharedoperation.RecordChanges{
		Status: &status, ResultJSON: &rawResult, LeaseOwner: &empty, LeaseExpiresAt: &empty,
		UpdatedAt: &updatedAt, ExpiresAt: &expiresAt,
	})
	return s.requireFencedWrite(ctx, completion.WorkspaceID, completion.ReceiptID, changed, err)
}

func (s *CallbackReceiptStore) FailCallbackRetryable(ctx context.Context, failure dispatchmodel.CallbackFailure) error {
	if s == nil || s.store == nil || s.store.DB() == nil {
		return fmt.Errorf("dispatch callback receipt store is unavailable")
	}
	now := normalizedCallbackTime(failure.Now)
	status, errorCode, failureClass, empty := string(idempotency.StatusFailedRetryable), "dispatch.callback_retryable", "retryable", ""
	updatedAt, expiresAt, token := now.Format(time.RFC3339Nano), failure.ExpiresAt.UTC().Format(time.RFC3339Nano), failure.FencingToken
	changed, err := sharedoperation.NewSQLStore(s.store.DB(), s.store.SQLRenderer).PatchRecord(ctx, sharedoperation.RecordFilter{
		WorkspaceID: strings.TrimSpace(failure.WorkspaceID), ID: strings.TrimSpace(failure.ReceiptID),
		Owner: dispatchCallbackOwner, Kind: dispatchCallbackKind, Status: string(idempotency.StatusProcessing),
		LeaseOwner: strings.TrimSpace(failure.LeaseOwner), FencingToken: &token,
	}, sharedoperation.RecordChanges{
		Status: &status, ErrorCode: &errorCode, FailureClass: &failureClass, LeaseOwner: &empty, LeaseExpiresAt: &empty,
		UpdatedAt: &updatedAt, ExpiresAt: &expiresAt,
	})
	return s.requireFencedWrite(ctx, failure.WorkspaceID, failure.ReceiptID, changed, err)
}

func (s *CallbackReceiptStore) requireFencedWrite(ctx context.Context, workspaceID, receiptID string, changed bool, err error) error {
	if err != nil {
		return err
	}
	if !changed {
		s.store.ObserveIdempotency(ctx, strings.TrimSpace(workspaceID), dispatchmodel.CallbackExecutionUseCase, idempotency.OutcomeLeaseLost)
		return mutation.MutationConflict("dispatch_callback_receipt", strings.TrimSpace(receiptID), mutation.MutationConflictLeaseLost, nil)
	}
	return nil
}

func (s *CallbackReceiptStore) findCallbackByScope(ctx context.Context, scope dispatchmodel.CallbackReceipt) (dispatchmodel.CallbackReceipt, bool, error) {
	record, found, err := sharedoperation.NewSQLStore(s.store.DB(), s.store.SQLRenderer).GetRecord(ctx, callbackRecordFilter(scope))
	if err != nil || !found {
		return dispatchmodel.CallbackReceipt{}, found, err
	}
	receipt, err := callbackReceipt(record)
	return receipt, err == nil, err
}

func normalizeCallbackClaim(request dispatchmodel.CallbackClaimRequest) (dispatchmodel.CallbackReceipt, time.Time, time.Duration, error) {
	receipt := request.Receipt
	values := []*string{&receipt.WorkspaceID, &receipt.RuntimeID, &receipt.Method, &receipt.Path, &receipt.IdempotencyKey, &receipt.BodySHA256, &receipt.ExecutionID}
	for _, value := range values {
		if strings.TrimSpace(*value) == "" || strings.TrimSpace(*value) != *value {
			return dispatchmodel.CallbackReceipt{}, time.Time{}, 0, fmt.Errorf("dispatch callback receipt identity is incomplete")
		}
	}
	if len(receipt.IdempotencyKey) > 191 || len(receipt.BodySHA256) != sha256.Size*2 {
		return dispatchmodel.CallbackReceipt{}, time.Time{}, 0, fmt.Errorf("dispatch callback receipt identity is invalid")
	}
	if _, err := principalmodel.NewWorkspaceID(receipt.WorkspaceID); err != nil {
		return dispatchmodel.CallbackReceipt{}, time.Time{}, 0, fmt.Errorf("dispatch callback workspace is invalid: %w", err)
	}
	if receipt.Method != strings.ToUpper(receipt.Method) || !strings.HasPrefix(receipt.Path, "/") || strings.ContainsAny(receipt.Path, "?#") || pathpkg.Clean(receipt.Path) != receipt.Path {
		return dispatchmodel.CallbackReceipt{}, time.Time{}, 0, fmt.Errorf("dispatch callback request scope is invalid")
	}
	if _, err := hex.DecodeString(receipt.BodySHA256); err != nil {
		return dispatchmodel.CallbackReceipt{}, time.Time{}, 0, fmt.Errorf("dispatch callback body fingerprint is invalid")
	}
	receipt.LeaseOwner = strings.TrimSpace(request.LeaseOwner)
	if receipt.LeaseOwner == "" {
		return dispatchmodel.CallbackReceipt{}, time.Time{}, 0, fmt.Errorf("dispatch callback lease owner is required")
	}
	now := normalizedCallbackTime(request.Now)
	if request.LeaseTTL <= 0 {
		return dispatchmodel.CallbackReceipt{}, time.Time{}, 0, fmt.Errorf("dispatch callback lease TTL is required")
	}
	return receipt, now, request.LeaseTTL, nil
}

func callbackReceiptLease(receipt dispatchmodel.CallbackReceipt) idempotency.Lease {
	expiresAt, _ := time.Parse(time.RFC3339Nano, receipt.LeaseExpiresAt)
	return idempotency.Lease{Owner: receipt.LeaseOwner, Token: receipt.FencingToken, ExpiresAt: expiresAt}
}

func callbackReceiptID(receipt dispatchmodel.CallbackReceipt) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{receipt.WorkspaceID, receipt.RuntimeID, receipt.Method, receipt.Path, receipt.IdempotencyKey}, "\x00")))
	return "dispatch_callback:" + hex.EncodeToString(sum[:16])
}

func callbackRecord(receipt dispatchmodel.CallbackReceipt) sharedoperation.Record {
	metadata, _ := json.Marshal(callbackOperationMetadata{RuntimeID: receipt.RuntimeID, Method: receipt.Method, Path: receipt.Path, IdempotencyKey: receipt.IdempotencyKey, ExecutionID: receipt.ExecutionID})
	result, _ := json.Marshal(callbackOperationResult{DownstreamID: receipt.DownstreamID, DownstreamOwner: receipt.DownstreamOwner, DownstreamStatus: receipt.DownstreamStatus})
	return sharedoperation.Record{
		ID: receipt.ID, WorkspaceID: receipt.WorkspaceID, Owner: dispatchCallbackOwner, Kind: dispatchCallbackKind,
		ActionKey: dispatchCallbackActionKey, ResourceType: "dispatch_callback", ResourceID: receipt.ExecutionID,
		IdempotencyKey: receipt.ID, RequestFingerprint: receipt.BodySHA256, RequestedBy: receipt.RuntimeID,
		Reason: receipt.Method, Reference: receipt.Path, Status: receipt.Status, StatusURL: "/operations/" + receipt.ID,
		ResultJSON: result, MetadataJSON: metadata, RelatedIDsJSON: json.RawMessage(`[]`), Correlation: receipt.ExecutionID,
		EvidenceJSON: json.RawMessage(`[]`), LeaseOwner: receipt.LeaseOwner, LeaseExpiresAt: receipt.LeaseExpiresAt,
		FencingToken: receipt.FencingToken, ExpiresAt: receipt.ExpiresAt, CreatedAt: receipt.CreatedAt, UpdatedAt: receipt.UpdatedAt,
	}
}

func callbackRecordFilter(receipt dispatchmodel.CallbackReceipt) sharedoperation.RecordFilter {
	return sharedoperation.RecordFilter{WorkspaceID: strings.TrimSpace(receipt.WorkspaceID), ID: callbackReceiptID(receipt), Owner: dispatchCallbackOwner, Kind: dispatchCallbackKind}
}

type callbackOperationMetadata struct {
	RuntimeID      string `json:"runtime_id"`
	Method         string `json:"method"`
	Path           string `json:"path"`
	IdempotencyKey string `json:"idempotency_key"`
	ExecutionID    string `json:"execution_id"`
}

type callbackOperationResult struct {
	DownstreamID     string `json:"downstream_id,omitempty"`
	DownstreamOwner  string `json:"downstream_owner,omitempty"`
	DownstreamStatus string `json:"downstream_status,omitempty"`
}

func callbackReceipt(record sharedoperation.Record) (dispatchmodel.CallbackReceipt, error) {
	receipt := dispatchmodel.CallbackReceipt{
		ID: record.ID, WorkspaceID: record.WorkspaceID, BodySHA256: record.RequestFingerprint, Status: record.Status,
		LeaseOwner: record.LeaseOwner, LeaseExpiresAt: record.LeaseExpiresAt, FencingToken: record.FencingToken,
		ExpiresAt: record.ExpiresAt, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
	if record.Owner != dispatchCallbackOwner || record.Kind != dispatchCallbackKind || record.ActionKey != dispatchCallbackActionKey || record.ResourceType != "dispatch_callback" || record.IdempotencyKey != receipt.ID {
		return receipt, fmt.Errorf("dispatch callback operation identity is invalid")
	}
	var metadata callbackOperationMetadata
	if err := json.Unmarshal(record.MetadataJSON, &metadata); err != nil {
		return receipt, fmt.Errorf("decode dispatch callback metadata: %w", err)
	}
	var result callbackOperationResult
	if err := json.Unmarshal(record.ResultJSON, &result); err != nil {
		return receipt, fmt.Errorf("decode dispatch callback result: %w", err)
	}
	receipt.RuntimeID, receipt.Method, receipt.Path, receipt.IdempotencyKey, receipt.ExecutionID = metadata.RuntimeID, metadata.Method, metadata.Path, metadata.IdempotencyKey, metadata.ExecutionID
	receipt.DownstreamID, receipt.DownstreamOwner, receipt.DownstreamStatus = result.DownstreamID, result.DownstreamOwner, result.DownstreamStatus
	if receipt.RuntimeID != record.RequestedBy || receipt.Method != record.Reason || receipt.Path != record.Reference || receipt.ExecutionID != record.ResourceID || receipt.ExecutionID != record.Correlation {
		return receipt, fmt.Errorf("dispatch callback operation metadata is inconsistent")
	}
	return receipt, nil
}

func normalizedCallbackTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func callbackClaimBackoff(ctx context.Context, attempt int) error {
	timer := time.NewTimer(time.Duration(attempt+1) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
