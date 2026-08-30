package transaction

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/mutation"
	ormbuilder "github.com/domainry/domainry-orm/query"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type BoundaryIntentStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

type BoundaryIntentTransaction interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

var _ transactionmodel.BoundaryIntentRepository = BoundaryIntentStore{}

func NewBoundaryIntentStore(store *database.RuntimeStore) BoundaryIntentStore {
	return BoundaryIntentStore{store: store, db: store.DB()}
}

func (r BoundaryIntentStore) CreateBoundaryIntent(ctx context.Context, intent transactionmodel.BoundaryIntent) (transactionmodel.BoundaryIntent, bool, error) {
	if err := ctx.Err(); err != nil {
		return transactionmodel.BoundaryIntent{}, false, err
	}
	intent, statement, args, err := r.boundaryIntentInsert(intent)
	if err != nil {
		return transactionmodel.BoundaryIntent{}, false, err
	}
	if _, err := r.db.ExecContext(ctx, statement, args...); err != nil {
		existing, found, readErr := r.findByIdentity(ctx, intent.WorkspaceID, intent.Owner, intent.Operation, intent.IdempotencyKey)
		if readErr == nil && found {
			return existing, true, nil
		}
		return transactionmodel.BoundaryIntent{}, false, fmt.Errorf("insert boundary intent: %w", err)
	}
	return intent, false, nil
}

// InsertBoundaryIntentTx lets another infrastructure owner enlist a boundary
// intent in an existing local transaction without duplicating its physical
// schema, defaults or parameterized insert contract.
func (r BoundaryIntentStore) InsertBoundaryIntentTx(ctx context.Context, tx BoundaryIntentTransaction, intent transactionmodel.BoundaryIntent) (transactionmodel.BoundaryIntent, error) {
	if tx == nil {
		return transactionmodel.BoundaryIntent{}, fmt.Errorf("boundary intent transaction is required")
	}
	if err := ctx.Err(); err != nil {
		return transactionmodel.BoundaryIntent{}, err
	}
	intent, statement, args, err := r.boundaryIntentInsert(intent)
	if err != nil {
		return transactionmodel.BoundaryIntent{}, err
	}
	if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
		return transactionmodel.BoundaryIntent{}, fmt.Errorf("insert boundary intent: %w", err)
	}
	return intent, nil
}

func (r BoundaryIntentStore) boundaryIntentInsert(intent transactionmodel.BoundaryIntent) (transactionmodel.BoundaryIntent, string, []any, error) {
	workspaceID, err := requireBoundaryIntentWorkspaceID(intent.WorkspaceID)
	if err != nil {
		return transactionmodel.BoundaryIntent{}, "", nil, err
	}
	intent.WorkspaceID = workspaceID
	intent.Owner, intent.Operation, intent.IdempotencyKey = strings.TrimSpace(intent.Owner), strings.TrimSpace(intent.Operation), strings.TrimSpace(intent.IdempotencyKey)
	if intent.Owner == "" || intent.Operation == "" || intent.IdempotencyKey == "" {
		return transactionmodel.BoundaryIntent{}, "", nil, fmt.Errorf("boundary intent identity is required")
	}
	if intent.ID == "" {
		sum := sha256.Sum256([]byte(intent.WorkspaceID + "\x00" + intent.Owner + "\x00" + intent.Operation + "\x00" + intent.IdempotencyKey))
		intent.ID = "boundary_intent:" + hex.EncodeToString(sum[:])[:24]
	}
	if intent.Status == "" {
		intent.Status = transactionmodel.BoundaryIntentPending
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	intent.CreatedAt, intent.UpdatedAt = now, now
	payload, err := json.Marshal(nonNilMap(intent.Payload))
	if err != nil {
		return transactionmodel.BoundaryIntent{}, "", nil, fmt.Errorf("encode boundary intent payload: %w", err)
	}
	compensation, err := json.Marshal(nonNilMap(intent.CompensationPayload))
	if err != nil {
		return transactionmodel.BoundaryIntent{}, "", nil, fmt.Errorf("encode boundary intent compensation: %w", err)
	}
	columns := []string{"id", "owner", "operation", "resource_id", "idempotency_key", "status", "payload_json", "compensation_payload_json", "attempt_count", "next_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "last_error", "created_at", "updated_at"}
	values := []any{intent.ID, intent.Owner, intent.Operation, intent.ResourceID, intent.IdempotencyKey, intent.Status, string(payload), string(compensation), intent.AttemptCount, intent.NextAttemptAt, intent.LeaseOwner, intent.LeaseExpiresAt, intent.FencingToken, intent.LastError, intent.CreatedAt, intent.UpdatedAt}
	statement, args, buildErr := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "_transaction_boundary_intents", intent.WorkspaceID).Columns(columns...).Values(values...).Build()
	if buildErr != nil {
		return transactionmodel.BoundaryIntent{}, "", nil, fmt.Errorf("build boundary intent insert: %w", buildErr)
	}
	return intent, statement, args, nil
}

func (r BoundaryIntentStore) ClaimBoundaryIntent(ctx context.Context, workspaceID, id, owner, nowText string) (transactionmodel.BoundaryIntent, bool, error) {
	workspaceID, err := requireBoundaryIntentWorkspaceID(workspaceID)
	if err != nil {
		return transactionmodel.BoundaryIntent{}, false, err
	}
	id, owner = strings.TrimSpace(id), strings.TrimSpace(owner)
	if id == "" || owner == "" {
		return transactionmodel.BoundaryIntent{}, false, fmt.Errorf("boundary intent claim identity is required")
	}
	now, err := time.Parse(time.RFC3339, nowText)
	if err != nil {
		now, err = time.Parse(time.RFC3339Nano, nowText)
	}
	if err != nil {
		return transactionmodel.BoundaryIntent{}, false, fmt.Errorf("parse boundary intent claim time: %w", err)
	}
	expires := now.Add(90 * time.Second).Format(time.RFC3339Nano)
	statement, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "_transaction_boundary_intents", workspaceID).
		Set("status", transactionmodel.BoundaryIntentExecuting).Set("lease_owner", owner).Set("lease_expires_at", expires).
		SetExpression("fencing_token", ormbuilder.Add(ormbuilder.Column("fencing_token"), ormbuilder.Value(1))).Set("updated_at", nowText).
		Where(ormbuilder.And(ormbuilder.Equal("id", id), ormbuilder.Or(
			ormbuilder.And(ormbuilder.In("status", transactionmodel.BoundaryIntentPending, transactionmodel.BoundaryIntentReconciliationRequired), ormbuilder.Or(ormbuilder.Equal("next_attempt_at", ""), ormbuilder.LessThanOrEqual("next_attempt_at", nowText))),
			ormbuilder.And(ormbuilder.Equal("status", transactionmodel.BoundaryIntentExecuting), ormbuilder.LessThanOrEqual("lease_expires_at", nowText)),
		))).Build()
	if buildErr != nil {
		return transactionmodel.BoundaryIntent{}, false, fmt.Errorf("build boundary intent claim: %w", buildErr)
	}
	result, err := r.db.ExecContext(ctx, statement, args...)
	if err != nil {
		return transactionmodel.BoundaryIntent{}, false, fmt.Errorf("claim boundary intent: %w", err)
	}
	intent, found, err := r.GetBoundaryIntent(ctx, workspaceID, id)
	if err != nil || !found {
		return intent, false, err
	}
	affected, _ := result.RowsAffected()
	return intent, affected == 1, nil
}

func (r BoundaryIntentStore) TransitionBoundaryIntent(ctx context.Context, workspaceID, id, expectedLeaseOwner string, expectedFencingToken int64, next transactionmodel.BoundaryIntentStatus, errorText, nextAttemptAt string) (transactionmodel.BoundaryIntent, error) {
	workspaceID, err := requireBoundaryIntentWorkspaceID(workspaceID)
	if err != nil {
		return transactionmodel.BoundaryIntent{}, err
	}
	current, found, err := r.GetBoundaryIntent(ctx, workspaceID, id)
	if err != nil {
		return transactionmodel.BoundaryIntent{}, err
	}
	if !found {
		return transactionmodel.BoundaryIntent{}, fmt.Errorf("boundary intent not found")
	}
	if !transactionmodel.BoundaryIntentTransitionAllowed(current.Status, next) {
		return transactionmodel.BoundaryIntent{}, fmt.Errorf("boundary intent transition %s -> %s is not allowed", current.Status, next)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	attemptIncrement := 0
	if next == transactionmodel.BoundaryIntentReconciliationRequired || next == transactionmodel.BoundaryIntentManualReview {
		attemptIncrement = 1
	}
	statement, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "_transaction_boundary_intents", workspaceID).
		Set("status", next).Set("last_error", strings.TrimSpace(errorText)).Set("next_attempt_at", strings.TrimSpace(nextAttemptAt)).
		SetExpression("attempt_count", ormbuilder.Add(ormbuilder.Column("attempt_count"), ormbuilder.Value(attemptIncrement))).
		Set("lease_owner", "").Set("lease_expires_at", "").Set("updated_at", now).
		Where(ormbuilder.And(ormbuilder.Equal("id", id), ormbuilder.Equal("status", current.Status), ormbuilder.Equal("lease_owner", strings.TrimSpace(expectedLeaseOwner)), ormbuilder.Equal("fencing_token", expectedFencingToken))).Build()
	if buildErr != nil {
		return transactionmodel.BoundaryIntent{}, fmt.Errorf("build boundary intent transition: %w", buildErr)
	}
	result, err := r.db.ExecContext(ctx, statement, args...)
	if err != nil {
		return transactionmodel.BoundaryIntent{}, fmt.Errorf("transition boundary intent: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return transactionmodel.BoundaryIntent{}, mutation.MutationConflict("transaction_boundary_intent", id, mutation.MutationConflictLeaseLost, nil)
	}
	updated, _, err := r.GetBoundaryIntent(ctx, workspaceID, id)
	return updated, err
}

func requireBoundaryIntentWorkspaceID(workspaceID string) (string, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return "", fmt.Errorf("boundary intent workspace is required")
	}
	return workspaceID, nil
}

func (r BoundaryIntentStore) GetBoundaryIntent(ctx context.Context, workspaceID, id string) (transactionmodel.BoundaryIntent, bool, error) {
	statement, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "_transaction_boundary_intents", workspaceID).
		Columns(boundaryIntentColumnNames...).Where(ormbuilder.Equal("id", strings.TrimSpace(id))).Limit(1).Build()
	if buildErr != nil {
		return transactionmodel.BoundaryIntent{}, false, fmt.Errorf("build boundary intent lookup: %w", buildErr)
	}
	row := r.db.QueryRowContext(ctx, statement, args...)
	intent, err := scanBoundaryIntent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return transactionmodel.BoundaryIntent{}, false, nil
	}
	return intent, err == nil, err
}

func (r BoundaryIntentStore) findByIdentity(ctx context.Context, workspaceID, owner, operation, key string) (transactionmodel.BoundaryIntent, bool, error) {
	statement, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "_transaction_boundary_intents", workspaceID).
		Columns(boundaryIntentColumnNames...).Where(ormbuilder.And(ormbuilder.Equal("owner", owner), ormbuilder.Equal("operation", operation), ormbuilder.Equal("idempotency_key", key))).Limit(1).Build()
	if buildErr != nil {
		return transactionmodel.BoundaryIntent{}, false, fmt.Errorf("build boundary intent identity lookup: %w", buildErr)
	}
	row := r.db.QueryRowContext(ctx, statement, args...)
	intent, err := scanBoundaryIntent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return transactionmodel.BoundaryIntent{}, false, nil
	}
	return intent, err == nil, err
}

type boundaryIntentScanner interface{ Scan(...any) error }

func scanBoundaryIntent(scanner boundaryIntentScanner) (transactionmodel.BoundaryIntent, error) {
	var intent transactionmodel.BoundaryIntent
	var status, payloadJSON, compensationJSON string
	err := scanner.Scan(&intent.ID, &intent.WorkspaceID, &intent.Owner, &intent.Operation, &intent.ResourceID, &intent.IdempotencyKey, &status, &payloadJSON, &compensationJSON, &intent.AttemptCount, &intent.NextAttemptAt, &intent.LeaseOwner, &intent.LeaseExpiresAt, &intent.FencingToken, &intent.LastError, &intent.CreatedAt, &intent.UpdatedAt)
	if err != nil {
		return intent, err
	}
	intent.Status = transactionmodel.BoundaryIntentStatus(status)
	_ = json.Unmarshal([]byte(payloadJSON), &intent.Payload)
	_ = json.Unmarshal([]byte(compensationJSON), &intent.CompensationPayload)
	return intent, nil
}

var boundaryIntentColumnNames = []string{"id", "workspace_id", "owner", "operation", "resource_id", "idempotency_key", "status", "payload_json", "compensation_payload_json", "attempt_count", "next_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "last_error", "created_at", "updated_at"}

func nonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}
