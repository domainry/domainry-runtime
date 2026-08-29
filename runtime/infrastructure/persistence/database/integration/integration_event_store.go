// Integration event persistence.
package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	ormbuilder "github.com/domainry/domainry-orm/builder"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"strings"
	"time"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type IntegrationEventStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewIntegrationEventStore(store *database.RuntimeStore) IntegrationEventStore {
	return IntegrationEventStore{store: store, db: store.DB()}
}

func (r IntegrationEventStore) ListEvents(ctx context.Context, workspaceID, provider, status string, limit int) ([]integrationmodel.IntegrationEvent, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	predicates := []ormbuilder.Predicate{}
	if provider = strings.TrimSpace(provider); provider != "" {
		predicates = append(predicates, ormbuilder.Equal("provider", provider))
	}
	if status = strings.TrimSpace(status); status != "" {
		predicates = append(predicates, ormbuilder.Equal("status", status))
	}
	builder := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "integration_events", workspaceID).
		Columns(integrationEventColumns...).OrderBy(ormbuilder.Descending("received_at")).Limit(limit)
	if len(predicates) != 0 {
		builder.Where(ormbuilder.And(predicates...))
	}
	query, args, buildErr := builder.Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build integration event query: %w", buildErr)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list integration events: %w", err)
	}
	defer rows.Close()
	out := []integrationmodel.IntegrationEvent{}
	for rows.Next() {
		value, err := scanIntegrationEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list integration events: %w", err)
	}
	return out, nil
}

func (r IntegrationEventStore) GetEvent(ctx context.Context, workspaceID, eventID string) (integrationmodel.IntegrationEvent, bool, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, false, err
	}
	return r.findByID(ctx, workspaceID, eventID)
}

func (r IntegrationEventStore) UpsertEvent(ctx context.Context, workspaceID string, value integrationmodel.IntegrationEvent) (integrationmodel.IntegrationEvent, bool, error) {
	workspaceID, err := requireIntegrationMutationWorkspaceID(workspaceID, value.WorkspaceID)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, false, err
	}
	value.WorkspaceID = workspaceID
	ctx = integrationWorkerWorkspaceContext(ctx, workspaceID, "integration-event-ingress")
	now := time.Now().UTC().Format(time.RFC3339)
	if strings.TrimSpace(value.Status) == "" {
		value.Status = "received"
	}
	existing, found, err := r.findByExternalID(ctx, value.WorkspaceID, value.Provider, value.ExternalID)
	if err != nil || found {
		return existing, found, err
	}
	value.ID, value.ReceivedAt, value.UpdatedAt = integrationEventID(value.WorkspaceID, value.Provider, value.ExternalID), now, now
	payload, err := json.Marshal(nonNilMap(value.Payload))
	if err != nil {
		return integrationmodel.IntegrationEvent{}, false, fmt.Errorf("encode integration event payload: %w", err)
	}
	columns := []string{"id", "workspace_id", "provider", "event_type", "external_id", "status", "payload_json", "error", "attempt_count", "next_retry_at", "last_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "received_at", "updated_at"}
	args := []any{value.ID, value.WorkspaceID, value.Provider, value.EventType, value.ExternalID, value.Status, string(payload), value.Error, value.AttemptCount, value.NextRetryAt, value.LastAttemptAt, value.LeaseOwner, value.LeaseExpiresAt, value.FencingToken, value.ReceivedAt, value.UpdatedAt}
	if _, err := r.db.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("integration_events")+" ("+stringsJoinIdentifiers(r.store, columns...)+") VALUES ("+stringsJoinPlaceholders(r.store, len(columns))+")", args...); err != nil {
		if existing, found, readErr := r.findByExternalID(ctx, value.WorkspaceID, value.Provider, value.ExternalID); readErr == nil && found {
			return existing, true, nil
		}
		return integrationmodel.IntegrationEvent{}, false, fmt.Errorf("insert integration event: %w", err)
	}
	if err := RegisterEventWorkerQueueScope(ctx, r.store, r.db, workspaceID, now); err != nil {
		return integrationmodel.IntegrationEvent{}, false, err
	}
	return value, false, nil
}

func (r IntegrationEventStore) UpdateEventStatus(ctx context.Context, workspaceID, eventID, status, errorText string) (integrationmodel.IntegrationEvent, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("integration event id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := r.db.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("integration_events")+" SET "+r.store.Identifier("status")+" = "+r.store.Placeholder(1)+", "+r.store.Identifier("error")+" = "+r.store.Placeholder(2)+", "+r.store.Identifier("next_retry_at")+" = "+r.store.Placeholder(3)+", "+r.store.Identifier("updated_at")+" = "+r.store.Placeholder(4)+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(5)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(6), status, errorText, "", now, workspaceID, eventID)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("update integration event status: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("read updated integration event count: %w", err)
	}
	if count == 0 {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("integration event not found")
	}
	value, found, err := r.findByID(ctx, workspaceID, eventID)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	if !found {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("integration event not found")
	}
	return value, nil
}

func (r IntegrationEventStore) ScheduleEventRetry(ctx context.Context, workspaceID, eventID string, delaySeconds int, errorText string) (integrationmodel.IntegrationEvent, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("integration event id is required")
	}
	if delaySeconds < 0 {
		delaySeconds = 60
	}
	if delaySeconds > 86400 {
		delaySeconds = 86400
	}
	now := time.Now().UTC()
	nowText, next := now.Format(time.RFC3339), now.Add(time.Duration(delaySeconds)*time.Second).Format(time.RFC3339)
	result, err := r.db.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("integration_events")+" SET "+r.store.Identifier("status")+" = "+r.store.Placeholder(1)+", "+r.store.Identifier("error")+" = "+r.store.Placeholder(2)+", "+r.store.Identifier("attempt_count")+" = "+r.store.Identifier("attempt_count")+" + 1, "+r.store.Identifier("next_retry_at")+" = "+r.store.Placeholder(3)+", "+r.store.Identifier("last_attempt_at")+" = "+r.store.Placeholder(4)+", "+r.store.Identifier("updated_at")+" = "+r.store.Placeholder(5)+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(6)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(7), "failed", errorText, next, nowText, nowText, workspaceID, eventID)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("schedule integration event retry: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("read scheduled integration event count: %w", err)
	}
	if count == 0 {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("integration event not found")
	}
	value, found, err := r.findByID(ctx, workspaceID, eventID)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	if !found {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("integration event not found")
	}
	return value, nil
}

func (r IntegrationEventStore) RecordWebhookNonce(ctx context.Context, workspaceID, connectorKey, nonce, timestamp, expiresAt string) (bool, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return false, err
	}
	connectorKey, nonce = strings.TrimSpace(connectorKey), strings.TrimSpace(nonce)
	if connectorKey == "" || nonce == "" {
		return false, fmt.Errorf("integration webhook nonce identity is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	deleteStatement, deleteArgs, buildErr := ormbuilder.NewWorkspaceDeleteBuilder(r.store.SQLRenderer, "integration_webhook_nonces", workspaceID).
		Where(ormbuilder.LessThanOrEqual("expires_at", now)).Build()
	if buildErr != nil {
		return false, fmt.Errorf("build expired integration webhook nonce cleanup: %w", buildErr)
	}
	if _, err := r.db.ExecContext(ctx, deleteStatement, deleteArgs...); err != nil {
		return false, fmt.Errorf("delete expired integration webhook nonces: %w", err)
	}
	var existing string
	err = r.db.QueryRowContext(ctx, "SELECT "+r.store.Identifier("nonce")+" FROM "+r.store.TableIdentifier("integration_webhook_nonces")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("connector_key")+" = "+r.store.Placeholder(2)+" AND "+r.store.Identifier("nonce")+" = "+r.store.Placeholder(3), workspaceID, connectorKey, nonce).Scan(&existing)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("read integration webhook nonce: %w", err)
	}
	columns := []string{"id", "workspace_id", "connector_key", "nonce", "request_timestamp", "created_at", "expires_at"}
	args := []any{"integration_webhook_nonce:" + workspaceID + ":" + connectorKey + ":" + nonce, workspaceID, connectorKey, nonce, strings.TrimSpace(timestamp), now, strings.TrimSpace(expiresAt)}
	if _, err := r.db.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("integration_webhook_nonces")+" ("+stringsJoinIdentifiers(r.store, columns...)+") VALUES ("+stringsJoinPlaceholders(r.store, len(columns))+")", args...); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "unique") || strings.Contains(lower, "duplicate") {
			return true, nil
		}
		return false, fmt.Errorf("insert integration webhook nonce: %w", err)
	}
	return false, nil
}

var integrationEventColumns = []string{"id", "workspace_id", "provider", "event_type", "external_id", "status", "payload_json", "error", "attempt_count", "next_retry_at", "last_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "received_at", "updated_at"}

func integrationEventColumnsSQL(store *database.RuntimeStore) string {
	return stringsJoinIdentifiers(store, integrationEventColumns...)
}

func (r IntegrationEventStore) findByID(ctx context.Context, workspaceID, eventID string) (integrationmodel.IntegrationEvent, bool, error) {
	row := r.db.QueryRowContext(ctx, "SELECT "+integrationEventColumnsSQL(r.store)+" FROM "+r.store.TableIdentifier("integration_events")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(2), workspaceID, strings.TrimSpace(eventID))
	value, err := scanIntegrationEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return integrationmodel.IntegrationEvent{}, false, nil
	}
	return value, err == nil, err
}

func (r IntegrationEventStore) findByExternalID(ctx context.Context, workspaceID, provider, externalID string) (integrationmodel.IntegrationEvent, bool, error) {
	row := r.db.QueryRowContext(ctx, "SELECT "+integrationEventColumnsSQL(r.store)+" FROM "+r.store.TableIdentifier("integration_events")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("provider")+" = "+r.store.Placeholder(2)+" AND "+r.store.Identifier("external_id")+" = "+r.store.Placeholder(3), workspaceID, strings.TrimSpace(provider), strings.TrimSpace(externalID))
	value, err := scanIntegrationEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return integrationmodel.IntegrationEvent{}, false, nil
	}
	return value, err == nil, err
}
