package dispatch

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	pathpkg "path"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-orm/query"
	dispatchcontract "github.com/domainry/domainry-runtime/runtime/domain/dispatch/contract"
	dispatchmodel "github.com/domainry/domainry-runtime/runtime/domain/dispatch/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	runtimeschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
)

var _ dispatchcontract.CallbackReceiptStore = (*CallbackReceiptStore)(nil)

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
	columns, values := callbackReceiptColumns(), callbackReceiptValues(receipt)
	statement, arguments, buildErr := query.NewWorkspaceInsertBuilder(s.store.SQLRenderer, runtimeschema.DispatchCallbackReceiptTable, receipt.WorkspaceID).
		Columns(append(columns[:1], columns[2:]...)...).
		Values(append(values[:1], values[2:]...)...).
		Build()
	if buildErr != nil {
		return dispatchmodel.CallbackClaimResult{}, fmt.Errorf("build dispatch callback receipt insert: %w", buildErr)
	}
	if _, insertErr := s.store.DB().ExecContext(ctx, statement, arguments...); insertErr == nil {
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
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, runtimeschema.DispatchCallbackReceiptTable, requested.WorkspaceID).
		Set("status", string(idempotency.StatusProcessing)).
		Set("downstream_id", "").
		Set("downstream_owner", "").
		Set("downstream_status", "").
		Set("lease_owner", requested.LeaseOwner).
		Set("lease_expires_at", leaseExpiresAt).
		SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).
		Set("updated_at", updatedAt).
		Set("expires_at", "").
		Where(query.And(
			callbackScopePredicate(requested),
			query.Equal("request_fingerprint", requested.BodySHA256),
			query.Or(
				query.Equal("status", string(idempotency.StatusFailedRetryable)),
				query.And(query.Equal("status", string(idempotency.StatusProcessing)), query.LessThanOrEqual("lease_expires_at", updatedAt)),
			),
		)).Build()
	if err != nil {
		return dispatchmodel.CallbackClaimResult{}, fmt.Errorf("build dispatch callback reclaim: %w", err)
	}
	result, err := s.store.DB().ExecContext(ctx, statement, arguments...)
	if err != nil {
		return dispatchmodel.CallbackClaimResult{}, err
	}
	rows, err := result.RowsAffected()
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
	if rows == 1 {
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
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, runtimeschema.DispatchCallbackReceiptTable, strings.TrimSpace(heartbeat.WorkspaceID)).
		Set("lease_expires_at", now.Add(heartbeat.LeaseTTL).Format(time.RFC3339Nano)).
		Set("updated_at", nowText).
		Where(query.And(
			query.Equal("id", strings.TrimSpace(heartbeat.ReceiptID)),
			query.Equal("status", string(idempotency.StatusProcessing)),
			query.Equal("lease_owner", strings.TrimSpace(heartbeat.LeaseOwner)),
			query.Equal("fencing_token", heartbeat.FencingToken),
			query.GreaterThan("lease_expires_at", nowText),
		)).Build()
	if err != nil {
		return false, fmt.Errorf("build dispatch callback heartbeat: %w", err)
	}
	result, err := s.store.DB().ExecContext(ctx, statement, arguments...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (s *CallbackReceiptStore) CompleteCallback(ctx context.Context, completion dispatchmodel.CallbackCompletion) error {
	if s == nil || s.store == nil || s.store.DB() == nil {
		return fmt.Errorf("dispatch callback receipt store is unavailable")
	}
	now := normalizedCallbackTime(completion.Now)
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, runtimeschema.DispatchCallbackReceiptTable, strings.TrimSpace(completion.WorkspaceID)).
		Set("status", string(idempotency.StatusSucceeded)).
		Set("downstream_id", strings.TrimSpace(completion.DownstreamID)).
		Set("downstream_owner", strings.TrimSpace(completion.DownstreamOwner)).
		Set("downstream_status", strings.TrimSpace(completion.DownstreamStatus)).
		Set("lease_owner", "").
		Set("lease_expires_at", "").
		Set("updated_at", now.Format(time.RFC3339Nano)).
		Set("expires_at", completion.ExpiresAt.UTC().Format(time.RFC3339Nano)).
		Where(callbackFencePredicate(completion.ReceiptID, completion.LeaseOwner, completion.FencingToken)).Build()
	if err != nil {
		return fmt.Errorf("build dispatch callback completion: %w", err)
	}
	return s.requireFencedWrite(ctx, completion.WorkspaceID, completion.ReceiptID, statement, arguments)
}

func (s *CallbackReceiptStore) FailCallbackRetryable(ctx context.Context, failure dispatchmodel.CallbackFailure) error {
	if s == nil || s.store == nil || s.store.DB() == nil {
		return fmt.Errorf("dispatch callback receipt store is unavailable")
	}
	now := normalizedCallbackTime(failure.Now)
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, runtimeschema.DispatchCallbackReceiptTable, strings.TrimSpace(failure.WorkspaceID)).
		Set("status", string(idempotency.StatusFailedRetryable)).
		Set("lease_owner", "").
		Set("lease_expires_at", "").
		Set("updated_at", now.Format(time.RFC3339Nano)).
		Set("expires_at", failure.ExpiresAt.UTC().Format(time.RFC3339Nano)).
		Where(callbackFencePredicate(failure.ReceiptID, failure.LeaseOwner, failure.FencingToken)).Build()
	if err != nil {
		return fmt.Errorf("build dispatch callback retryable failure: %w", err)
	}
	return s.requireFencedWrite(ctx, failure.WorkspaceID, failure.ReceiptID, statement, arguments)
}

func (s *CallbackReceiptStore) requireFencedWrite(ctx context.Context, workspaceID, receiptID, statement string, arguments []any) error {
	result, err := s.store.DB().ExecContext(ctx, statement, arguments...)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		s.store.ObserveIdempotency(ctx, strings.TrimSpace(workspaceID), dispatchmodel.CallbackExecutionUseCase, idempotency.OutcomeLeaseLost)
		return mutation.MutationConflict("dispatch_callback_receipt", strings.TrimSpace(receiptID), mutation.MutationConflictLeaseLost, nil)
	}
	return nil
}

func (s *CallbackReceiptStore) findCallbackByScope(ctx context.Context, scope dispatchmodel.CallbackReceipt) (dispatchmodel.CallbackReceipt, bool, error) {
	statement, arguments, err := query.NewWorkspaceSelectBuilder(s.store.SQLRenderer, runtimeschema.DispatchCallbackReceiptTable, strings.TrimSpace(scope.WorkspaceID)).
		Columns(callbackReceiptColumns()...).
		Where(callbackScopePredicate(scope)).
		Limit(1).
		Build()
	if err != nil {
		return dispatchmodel.CallbackReceipt{}, false, fmt.Errorf("build dispatch callback receipt lookup: %w", err)
	}
	var receipt dispatchmodel.CallbackReceipt
	err = s.store.DB().QueryRowContext(ctx, statement, arguments...).Scan(callbackReceiptScanTargets(&receipt)...)
	if errors.Is(err, sql.ErrNoRows) {
		return dispatchmodel.CallbackReceipt{}, false, nil
	}
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

func callbackScopePredicate(receipt dispatchmodel.CallbackReceipt) query.Predicate {
	return query.And(
		query.Equal("runtime_id", receipt.RuntimeID),
		query.Equal("method", receipt.Method),
		query.Equal("path", receipt.Path),
		query.Equal("idempotency_key", receipt.IdempotencyKey),
	)
}

func callbackFencePredicate(receiptID, leaseOwner string, fencingToken int64) query.Predicate {
	return query.And(
		query.Equal("id", strings.TrimSpace(receiptID)),
		query.Equal("status", string(idempotency.StatusProcessing)),
		query.Equal("lease_owner", strings.TrimSpace(leaseOwner)),
		query.Equal("fencing_token", fencingToken),
	)
}

func callbackReceiptLease(receipt dispatchmodel.CallbackReceipt) idempotency.Lease {
	expiresAt, _ := time.Parse(time.RFC3339Nano, receipt.LeaseExpiresAt)
	return idempotency.Lease{Owner: receipt.LeaseOwner, Token: receipt.FencingToken, ExpiresAt: expiresAt}
}

func callbackReceiptID(receipt dispatchmodel.CallbackReceipt) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{receipt.WorkspaceID, receipt.RuntimeID, receipt.Method, receipt.Path, receipt.IdempotencyKey}, "\x00")))
	return "dispatch_callback:" + hex.EncodeToString(sum[:16])
}

func callbackReceiptColumns() []string {
	return []string{"id", "workspace_id", "runtime_id", "method", "path", "idempotency_key", "request_fingerprint", "status", "execution_id", "downstream_id", "downstream_owner", "downstream_status", "lease_owner", "lease_expires_at", "fencing_token", "created_at", "updated_at", "expires_at"}
}

func callbackReceiptValues(receipt dispatchmodel.CallbackReceipt) []any {
	return []any{receipt.ID, receipt.WorkspaceID, receipt.RuntimeID, receipt.Method, receipt.Path, receipt.IdempotencyKey, receipt.BodySHA256, receipt.Status, receipt.ExecutionID, receipt.DownstreamID, receipt.DownstreamOwner, receipt.DownstreamStatus, receipt.LeaseOwner, receipt.LeaseExpiresAt, receipt.FencingToken, receipt.CreatedAt, receipt.UpdatedAt, receipt.ExpiresAt}
}

func callbackReceiptScanTargets(receipt *dispatchmodel.CallbackReceipt) []any {
	return []any{&receipt.ID, &receipt.WorkspaceID, &receipt.RuntimeID, &receipt.Method, &receipt.Path, &receipt.IdempotencyKey, &receipt.BodySHA256, &receipt.Status, &receipt.ExecutionID, &receipt.DownstreamID, &receipt.DownstreamOwner, &receipt.DownstreamStatus, &receipt.LeaseOwner, &receipt.LeaseExpiresAt, &receipt.FencingToken, &receipt.CreatedAt, &receipt.UpdatedAt, &receipt.ExpiresAt}
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
