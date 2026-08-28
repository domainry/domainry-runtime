package integration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

const (
	integrationEventWorkerQueueKind  = "integration_event"
	integrationOutboxWorkerQueueKind = "integration_outbox"
)

type integrationWorkerQueueScopeExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// RegisterEventWorkerQueueScope makes one workspace discoverable by the
// Runtime-global integration event worker. The registry itself is deliberately
// not workspace-scoped, while the actual queue read remains protected by RLS.
func RegisterEventWorkerQueueScope(ctx context.Context, store *database.RuntimeStore, executor integrationWorkerQueueScopeExecutor, workspaceID, updatedAt string) error {
	return registerIntegrationWorkerQueueScope(ctx, store, executor, integrationEventWorkerQueueKind, workspaceID, updatedAt)
}

// RegisterOutboxWorkerQueueScope makes one workspace discoverable by the
// Runtime-global integration outbox worker. Callers that already own a business
// transaction pass that transaction so the queue row and discovery scope commit
// together.
func RegisterOutboxWorkerQueueScope(ctx context.Context, store *database.RuntimeStore, executor integrationWorkerQueueScopeExecutor, workspaceID, updatedAt string) error {
	return registerIntegrationWorkerQueueScope(ctx, store, executor, integrationOutboxWorkerQueueKind, workspaceID, updatedAt)
}

func registerIntegrationWorkerQueueScope(ctx context.Context, store *database.RuntimeStore, executor integrationWorkerQueueScopeExecutor, queueKind, workspaceID, updatedAt string) error {
	if store == nil || executor == nil {
		return fmt.Errorf("integration worker queue scope store is required")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return fmt.Errorf("integration worker queue scope workspace is required")
	}
	updatedAt = strings.TrimSpace(updatedAt)
	if updatedAt == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	digest := sha256.Sum256([]byte(queueKind + "\x00" + workspaceID))
	id := "worker_scope:" + hex.EncodeToString(digest[:12])
	update := "UPDATE " + store.TableIdentifier("runtime_worker_queue_scopes") + " SET " + store.Identifier("updated_at") + " = " + store.Placeholder(1) + " WHERE " + store.Identifier("id") + " = " + store.Placeholder(2)
	updated, err := executor.ExecContext(ctx, update, updatedAt, id)
	if err != nil {
		return fmt.Errorf("refresh %s worker queue scope: %w", queueKind, err)
	}
	if affected, rowsErr := updated.RowsAffected(); rowsErr != nil {
		return fmt.Errorf("read refreshed %s worker queue scope count: %w", queueKind, rowsErr)
	} else if affected > 0 {
		return nil
	}
	columns := []string{"id", "queue_kind", "scope_key", "updated_at"}
	insert := "INSERT INTO " + store.TableIdentifier("runtime_worker_queue_scopes") + " (" + stringsJoinIdentifiers(store, columns...) + ") VALUES (" + stringsJoinPlaceholders(store, len(columns)) + ")"
	if _, err := executor.ExecContext(ctx, insert, id, queueKind, workspaceID, updatedAt); err != nil {
		// A concurrent producer may have inserted the deterministic scope after
		// our update. A second update distinguishes that harmless race from a
		// real insert failure without relying on driver-specific error strings.
		retried, retryErr := executor.ExecContext(ctx, update, updatedAt, id)
		if retryErr == nil {
			if affected, rowsErr := retried.RowsAffected(); rowsErr == nil && affected > 0 {
				return nil
			}
		}
		return fmt.Errorf("register %s worker queue scope: %w", queueKind, err)
	}
	return nil
}

func (r IntegrationWorkerStore) integrationWorkerQueueScopes(ctx context.Context, queueKind string) ([]string, error) {
	query := "SELECT " + r.store.Identifier("scope_key") + " FROM " + r.store.TableIdentifier("runtime_worker_queue_scopes") + " WHERE " + r.store.Identifier("queue_kind") + " = " + r.store.Placeholder(1) + " ORDER BY " + r.store.Identifier("scope_key") + " ASC"
	rows, err := r.db.QueryContext(ctx, query, queueKind)
	if err != nil {
		return nil, fmt.Errorf("list %s worker queue scopes: %w", queueKind, err)
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var workspaceID string
		if err := rows.Scan(&workspaceID); err != nil {
			return nil, fmt.Errorf("scan %s worker queue scope: %w", queueKind, err)
		}
		if workspaceID = strings.TrimSpace(workspaceID); workspaceID != "" {
			values = append(values, workspaceID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s worker queue scopes: %w", queueKind, err)
	}
	return values, nil
}

func integrationWorkerWorkspaceContext(ctx context.Context, workspaceID, actorID string) context.Context {
	ctx = requestcontext.WithWorkspaceID(ctx, strings.TrimSpace(workspaceID))
	if actorID = strings.TrimSpace(actorID); actorID != "" {
		ctx = requestcontext.WithActorID(ctx, actorID)
	}
	return ctx
}
