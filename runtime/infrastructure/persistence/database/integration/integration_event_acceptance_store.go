package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
)

// AcceptEvent persists the inbound event and its durable mapping intent in one
// commit. A caller can therefore never observe an accepted event without the
// dispatch fact needed to reconcile later processing.
func (r IntegrationEventStore) AcceptEvent(ctx context.Context, workspaceID string, event integrationmodel.IntegrationEvent, intent integrationmodel.IntegrationEventMappingIntent) (integrationmodel.IntegrationEvent, bool, error) {
	if err := ctx.Err(); err != nil {
		return integrationmodel.IntegrationEvent{}, false, err
	}
	workspaceID, err := requireIntegrationMutationWorkspaceID(workspaceID, event.WorkspaceID)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, false, err
	}
	event.WorkspaceID = workspaceID
	ctx = integrationWorkerWorkspaceContext(ctx, workspaceID, "integration-event-ingress")
	if strings.TrimSpace(event.Status) == "" {
		event.Status = "received"
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return integrationmodel.IntegrationEvent{}, false, fmt.Errorf("begin integration event acceptance: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, found, err := r.findByExternalIDTx(ctx, tx, event.WorkspaceID, event.Provider, event.ExternalID)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, false, err
	}
	if found {
		// The persisted payload has just been decoded from JSON, so it cannot
		// contain a value that the canonical fingerprint encoder rejects.
		existingFingerprint, _ := integrationpolicy.IntegrationEventContentFingerprint(existing)
		incomingFingerprint, fingerprintErr := integrationpolicy.IntegrationEventContentFingerprint(event)
		if fingerprintErr != nil {
			return integrationmodel.IntegrationEvent{}, false, fmt.Errorf("fingerprint incoming integration event: %w", fingerprintErr)
		}
		if existingFingerprint != incomingFingerprint {
			now := time.Now().UTC().Format(time.RFC3339)
			if _, err := tx.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("integration_events")+" SET "+r.store.Identifier("status")+" = 'quarantined', "+r.store.Identifier("error")+" = 'backend.integration.event.external_id_conflict', "+r.store.Identifier("next_retry_at")+" = '', "+r.store.Identifier("updated_at")+" = "+r.store.Placeholder(1)+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(2)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(3), now, existing.WorkspaceID, existing.ID); err != nil {
				return integrationmodel.IntegrationEvent{}, false, fmt.Errorf("quarantine conflicting integration event: %w", err)
			}
			existing.Status, existing.Error, existing.NextRetryAt, existing.UpdatedAt = "quarantined", "backend.integration.event.external_id_conflict", "", now
			if err := tx.Commit(); err != nil {
				return integrationmodel.IntegrationEvent{}, false, fmt.Errorf("commit conflicting integration event acceptance: %w", err)
			}
			return existing, true, nil
		}
		if err := r.insertMappingIntentTx(ctx, tx, existing, intent); err != nil {
			return integrationmodel.IntegrationEvent{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return integrationmodel.IntegrationEvent{}, false, fmt.Errorf("commit duplicate integration event acceptance: %w", err)
		}
		return existing, true, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	event.ID = integrationEventID(event.WorkspaceID, event.Provider, event.ExternalID)
	event.ReceivedAt, event.UpdatedAt = now, now
	payload, err := json.Marshal(nonNilMap(event.Payload))
	if err != nil {
		return integrationmodel.IntegrationEvent{}, false, fmt.Errorf("encode integration event payload: %w", err)
	}
	columns := []string{"id", "workspace_id", "provider", "event_type", "external_id", "status", "payload_json", "error", "attempt_count", "next_retry_at", "last_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "received_at", "updated_at"}
	args := []any{event.ID, event.WorkspaceID, event.Provider, event.EventType, event.ExternalID, event.Status, string(payload), event.Error, event.AttemptCount, event.NextRetryAt, event.LastAttemptAt, event.LeaseOwner, event.LeaseExpiresAt, event.FencingToken, event.ReceivedAt, event.UpdatedAt}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("integration_events")+" ("+stringsJoinIdentifiers(r.store, columns...)+") VALUES ("+stringsJoinPlaceholders(r.store, len(columns))+")", args...); err != nil {
		return integrationmodel.IntegrationEvent{}, false, fmt.Errorf("insert accepted integration event: %w", err)
	}
	if err := r.insertMappingIntentTx(ctx, tx, event, intent); err != nil {
		return integrationmodel.IntegrationEvent{}, false, err
	}
	if err := RegisterEventWorkerQueueScope(ctx, r.store, tx, workspaceID, event.UpdatedAt); err != nil {
		return integrationmodel.IntegrationEvent{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return integrationmodel.IntegrationEvent{}, false, fmt.Errorf("commit integration event acceptance: %w", err)
	}
	return event, false, nil
}

func (r IntegrationEventStore) insertMappingIntentTx(ctx context.Context, tx *sql.Tx, event integrationmodel.IntegrationEvent, intent integrationmodel.IntegrationEventMappingIntent) error {
	now := time.Now().UTC().Format(time.RFC3339)
	intent.ID = "integration_mapping_intent:" + event.ID
	intent.WorkspaceID = event.WorkspaceID
	intent.EventID = event.ID
	if strings.TrimSpace(intent.TargetType) == "" {
		intent.TargetType = "unmatched"
	}
	if strings.TrimSpace(intent.Status) == "" {
		intent.Status = "pending"
	}
	intent.CreatedAt, intent.UpdatedAt = now, now
	payload, err := json.Marshal(nonNilMap(intent.Payload))
	if err != nil {
		return fmt.Errorf("encode integration event mapping intent: %w", err)
	}
	columns := []string{"id", "workspace_id", "event_id", "mapping_key", "target_type", "status", "payload_json", "created_at", "updated_at"}
	args := []any{intent.ID, intent.WorkspaceID, intent.EventID, intent.MappingKey, intent.TargetType, intent.Status, string(payload), intent.CreatedAt, intent.UpdatedAt}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("integration_event_mapping_intents")+" ("+stringsJoinIdentifiers(r.store, columns...)+") VALUES ("+stringsJoinPlaceholders(r.store, len(columns))+")", args...); err != nil {
		if isUniqueConstraintError(err) {
			return nil
		}
		return fmt.Errorf("insert integration event mapping intent: %w", err)
	}
	return nil
}

func (r IntegrationEventStore) findByExternalIDTx(ctx context.Context, tx *sql.Tx, workspaceID, provider, externalID string) (integrationmodel.IntegrationEvent, bool, error) {
	row := tx.QueryRowContext(ctx, "SELECT "+integrationEventColumnsSQL(r.store)+" FROM "+r.store.TableIdentifier("integration_events")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("provider")+" = "+r.store.Placeholder(2)+" AND "+r.store.Identifier("external_id")+" = "+r.store.Placeholder(3), workspaceID, strings.TrimSpace(provider), strings.TrimSpace(externalID))
	value, err := scanIntegrationEvent(row)
	if err == sql.ErrNoRows {
		return integrationmodel.IntegrationEvent{}, false, nil
	}
	return value, err == nil, err
}

func isUniqueConstraintError(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "unique") || strings.Contains(text, "duplicate")
}
