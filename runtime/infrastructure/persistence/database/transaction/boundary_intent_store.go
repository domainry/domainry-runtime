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

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
)

type BoundaryIntentStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

var _ transactionmodel.BoundaryIntentRepository = BoundaryIntentStore{}

func NewBoundaryIntentStore(store *database.RuntimeStore) BoundaryIntentStore {
	return BoundaryIntentStore{store: store, db: store.DB()}
}

func (r BoundaryIntentStore) CreateBoundaryIntent(ctx context.Context, intent transactionmodel.BoundaryIntent) (transactionmodel.BoundaryIntent, bool, error) {
	if err := ctx.Err(); err != nil {
		return transactionmodel.BoundaryIntent{}, false, err
	}
	intent.WorkspaceID = valueOrDefault(intent.WorkspaceID, "default")
	intent.Owner, intent.Operation, intent.IdempotencyKey = strings.TrimSpace(intent.Owner), strings.TrimSpace(intent.Operation), strings.TrimSpace(intent.IdempotencyKey)
	if intent.Owner == "" || intent.Operation == "" || intent.IdempotencyKey == "" {
		return transactionmodel.BoundaryIntent{}, false, fmt.Errorf("boundary intent identity is required")
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
		return transactionmodel.BoundaryIntent{}, false, fmt.Errorf("encode boundary intent payload: %w", err)
	}
	compensation, err := json.Marshal(nonNilMap(intent.CompensationPayload))
	if err != nil {
		return transactionmodel.BoundaryIntent{}, false, fmt.Errorf("encode boundary intent compensation: %w", err)
	}
	columns := []string{"id", "workspace_id", "owner", "operation", "resource_id", "idempotency_key", "status", "payload_json", "compensation_payload_json", "attempt_count", "next_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "last_error", "created_at", "updated_at"}
	values := []any{intent.ID, intent.WorkspaceID, intent.Owner, intent.Operation, intent.ResourceID, intent.IdempotencyKey, intent.Status, string(payload), string(compensation), intent.AttemptCount, intent.NextAttemptAt, intent.LeaseOwner, intent.LeaseExpiresAt, intent.FencingToken, intent.LastError, intent.CreatedAt, intent.UpdatedAt}
	if _, err := r.db.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("transaction_boundary_intents")+" ("+joinIdentifiers(r.store, columns)+") VALUES ("+joinPlaceholders(r.store, len(values))+")", values...); err != nil {
		existing, found, readErr := r.findByIdentity(ctx, intent.WorkspaceID, intent.Owner, intent.Operation, intent.IdempotencyKey)
		if readErr == nil && found {
			return existing, true, nil
		}
		return transactionmodel.BoundaryIntent{}, false, fmt.Errorf("insert boundary intent: %w", err)
	}
	return intent, false, nil
}

func (r BoundaryIntentStore) ClaimBoundaryIntent(ctx context.Context, id, owner, nowText string) (transactionmodel.BoundaryIntent, bool, error) {
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
	query := "UPDATE " + r.store.TableIdentifier("transaction_boundary_intents") + " SET " + r.store.Identifier("status") + " = 'executing', " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("lease_expires_at") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("fencing_token") + " = " + r.store.Identifier("fencing_token") + " + 1, " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(3) + " WHERE " + r.store.Identifier("id") + " = " + r.store.Placeholder(4) + " AND ((" + r.store.Identifier("status") + " IN ('pending','reconciliation_required') AND (" + r.store.Identifier("next_attempt_at") + " = '' OR " + r.store.Identifier("next_attempt_at") + " <= " + r.store.Placeholder(5) + ")) OR (" + r.store.Identifier("status") + " = 'executing' AND " + r.store.Identifier("lease_expires_at") + " <= " + r.store.Placeholder(6) + "))"
	result, err := r.db.ExecContext(ctx, query, owner, expires, nowText, id, nowText, nowText)
	if err != nil {
		return transactionmodel.BoundaryIntent{}, false, fmt.Errorf("claim boundary intent: %w", err)
	}
	intent, found, err := r.GetBoundaryIntent(ctx, id)
	if err != nil || !found {
		return intent, false, err
	}
	affected, _ := result.RowsAffected()
	return intent, affected == 1, nil
}

func (r BoundaryIntentStore) TransitionBoundaryIntent(ctx context.Context, id, expectedLeaseOwner string, expectedFencingToken int64, next transactionmodel.BoundaryIntentStatus, errorText, nextAttemptAt string) (transactionmodel.BoundaryIntent, error) {
	current, found, err := r.GetBoundaryIntent(ctx, id)
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
	query := "UPDATE " + r.store.TableIdentifier("transaction_boundary_intents") + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("last_error") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("next_attempt_at") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("attempt_count") + " = " + r.store.Identifier("attempt_count") + " + " + r.store.Placeholder(4) + ", " + r.store.Identifier("lease_owner") + " = '', " + r.store.Identifier("lease_expires_at") + " = '', " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(5) + " WHERE " + r.store.Identifier("id") + " = " + r.store.Placeholder(6) + " AND " + r.store.Identifier("status") + " = " + r.store.Placeholder(7) + " AND " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(8) + " AND " + r.store.Identifier("fencing_token") + " = " + r.store.Placeholder(9)
	result, err := r.db.ExecContext(ctx, query, next, strings.TrimSpace(errorText), strings.TrimSpace(nextAttemptAt), attemptIncrement, now, id, current.Status, strings.TrimSpace(expectedLeaseOwner), expectedFencingToken)
	if err != nil {
		return transactionmodel.BoundaryIntent{}, fmt.Errorf("transition boundary intent: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return transactionmodel.BoundaryIntent{}, mutation.MutationConflict("transaction_boundary_intent", id, mutation.MutationConflictLeaseLost, nil)
	}
	updated, _, err := r.GetBoundaryIntent(ctx, id)
	return updated, err
}

func (r BoundaryIntentStore) GetBoundaryIntent(ctx context.Context, id string) (transactionmodel.BoundaryIntent, bool, error) {
	row := r.db.QueryRowContext(ctx, "SELECT "+boundaryIntentColumns(r.store)+" FROM "+r.store.TableIdentifier("transaction_boundary_intents")+" WHERE "+r.store.Identifier("id")+" = "+r.store.Placeholder(1), strings.TrimSpace(id))
	intent, err := scanBoundaryIntent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return transactionmodel.BoundaryIntent{}, false, nil
	}
	return intent, err == nil, err
}

func (r BoundaryIntentStore) findByIdentity(ctx context.Context, workspaceID, owner, operation, key string) (transactionmodel.BoundaryIntent, bool, error) {
	row := r.db.QueryRowContext(ctx, "SELECT "+boundaryIntentColumns(r.store)+" FROM "+r.store.TableIdentifier("transaction_boundary_intents")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("owner")+" = "+r.store.Placeholder(2)+" AND "+r.store.Identifier("operation")+" = "+r.store.Placeholder(3)+" AND "+r.store.Identifier("idempotency_key")+" = "+r.store.Placeholder(4), workspaceID, owner, operation, key)
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

func boundaryIntentColumns(store *database.RuntimeStore) string {
	return joinIdentifiers(store, []string{"id", "workspace_id", "owner", "operation", "resource_id", "idempotency_key", "status", "payload_json", "compensation_payload_json", "attempt_count", "next_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "last_error", "created_at", "updated_at"})
}

func joinIdentifiers(store *database.RuntimeStore, columns []string) string {
	return strings.Join(database.QuotedColumns(store, columns), ", ")
}

func joinPlaceholders(store *database.RuntimeStore, count int) string {
	values := make([]string, 0, count)
	for index := 1; index <= count; index++ {
		values = append(values, store.Placeholder(index))
	}
	return strings.Join(values, ", ")
}

func nonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func valueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}
