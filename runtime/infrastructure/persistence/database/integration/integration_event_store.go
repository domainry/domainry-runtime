// Integration event persistence.
package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/query"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

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
	insertColumns, insertValues, err := workspaceInsertValues(workspaceID, columns, args)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, false, err
	}
	statement, insertArgs, err := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "integration_events", workspaceID).Columns(insertColumns...).Values(insertValues...).Build()
	if err != nil {
		return integrationmodel.IntegrationEvent{}, false, fmt.Errorf("build integration event insert: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, statement, insertArgs...); err != nil {
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
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "integration_events", workspaceID).
		Set("status", status).Set("error", errorText).Set("next_retry_at", "").Set("updated_at", now).Where(ormbuilder.Equal("id", eventID)).Build()
	if err != nil {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("build integration event status update: %w", err)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
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
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "integration_events", workspaceID).
		Set("status", "failed").Set("error", errorText).
		SetExpression("attempt_count", ormbuilder.Add(ormbuilder.Column("attempt_count"), ormbuilder.Value(1))).
		Set("next_retry_at", next).Set("last_attempt_at", nowText).Set("updated_at", nowText).Where(ormbuilder.Equal("id", eventID)).Build()
	if err != nil {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("build integration event retry: %w", err)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
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
	lookup, lookupArgs, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "integration_webhook_nonces", workspaceID).Columns("nonce").Where(ormbuilder.And(ormbuilder.Equal("connector_key", connectorKey), ormbuilder.Equal("nonce", nonce))).Build()
	if buildErr != nil {
		return false, fmt.Errorf("build integration webhook nonce lookup: %w", buildErr)
	}
	err = r.db.QueryRowContext(ctx, lookup, lookupArgs...).Scan(&existing)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("read integration webhook nonce: %w", err)
	}
	columns := []string{"id", "workspace_id", "connector_key", "nonce", "request_timestamp", "created_at", "expires_at"}
	args := []any{"integration_webhook_nonce:" + workspaceID + ":" + connectorKey + ":" + nonce, workspaceID, connectorKey, nonce, strings.TrimSpace(timestamp), now, strings.TrimSpace(expiresAt)}
	insertColumns, insertValues, buildErr := workspaceInsertValues(workspaceID, columns, args)
	if buildErr != nil {
		return false, buildErr
	}
	statement, insertArgs, buildErr := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "integration_webhook_nonces", workspaceID).Columns(insertColumns...).Values(insertValues...).Build()
	if buildErr != nil {
		return false, fmt.Errorf("build integration webhook nonce insert: %w", buildErr)
	}
	if _, err := r.db.ExecContext(ctx, statement, insertArgs...); err != nil {
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
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "integration_events", workspaceID).Columns(integrationEventColumns...).Where(ormbuilder.Equal("id", strings.TrimSpace(eventID))).Build()
	if err != nil {
		return integrationmodel.IntegrationEvent{}, false, fmt.Errorf("build integration event lookup: %w", err)
	}
	row := r.db.QueryRowContext(ctx, query, args...)
	value, err := scanIntegrationEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return integrationmodel.IntegrationEvent{}, false, nil
	}
	return value, err == nil, err
}

func (r IntegrationEventStore) findByExternalID(ctx context.Context, workspaceID, provider, externalID string) (integrationmodel.IntegrationEvent, bool, error) {
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "integration_events", workspaceID).Columns(integrationEventColumns...).Where(ormbuilder.And(ormbuilder.Equal("provider", strings.TrimSpace(provider)), ormbuilder.Equal("external_id", strings.TrimSpace(externalID)))).Build()
	if err != nil {
		return integrationmodel.IntegrationEvent{}, false, fmt.Errorf("build integration external event lookup: %w", err)
	}
	row := r.db.QueryRowContext(ctx, query, args...)
	value, err := scanIntegrationEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return integrationmodel.IntegrationEvent{}, false, nil
	}
	return value, err == nil, err
}
