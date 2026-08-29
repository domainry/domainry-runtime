// Integration delivery persistence.
package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	ormbuilder "github.com/domainry/domainry-orm/builder"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-foundation/telemetry"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type IntegrationDeliveryStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewIntegrationDeliveryStore(store *database.RuntimeStore) IntegrationDeliveryStore {
	return IntegrationDeliveryStore{store: store, db: store.DB()}
}

func (r IntegrationDeliveryStore) GetOutbox(ctx context.Context, workspaceID, id string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	row := r.db.QueryRowContext(ctx, "SELECT "+integrationOutboxColumnsSQL(r.store)+" FROM "+r.store.TableIdentifier("integration_outbox_messages")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(2), workspaceID, strings.TrimSpace(id))
	message, err := scanIntegrationOutboxMessage(row)
	if err == sql.ErrNoRows {
		return integrationmodel.IntegrationOutboxMessage{}, false, nil
	}
	return message, err == nil, err
}

func (r IntegrationDeliveryStore) ListInvocations(ctx context.Context, workspaceID, connectorKey, recordID, executionID, status string, limit int) ([]integrationmodel.IntegrationInvocation, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	predicates := []ormbuilder.Predicate{}
	for _, filter := range []struct{ column, value string }{{"connector_key", connectorKey}, {"record_id", recordID}, {"workflow_execution_id", executionID}, {"status", status}} {
		if filter.value = strings.TrimSpace(filter.value); filter.value != "" {
			predicates = append(predicates, ormbuilder.Equal(filter.column, filter.value))
		}
	}
	builder := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "integration_invocations", workspaceID).
		Columns(integrationInvocationColumns...).OrderBy(ormbuilder.Descending("created_at")).Limit(limit)
	if len(predicates) != 0 {
		builder.Where(ormbuilder.And(predicates...))
	}
	query, args, buildErr := builder.Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build integration invocation query: %w", buildErr)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list integration invocations: %w", err)
	}
	defer rows.Close()
	out := []integrationmodel.IntegrationInvocation{}
	for rows.Next() {
		value, err := scanIntegrationInvocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list integration invocations: %w", err)
	}
	return out, nil
}

func (r IntegrationDeliveryStore) InsertInvocation(ctx context.Context, workspaceID string, value integrationmodel.IntegrationInvocation) (integrationmodel.IntegrationInvocation, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	workspaceID, err := requireIntegrationMutationWorkspaceID(workspaceID, value.WorkspaceID)
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, err
	}
	value.WorkspaceID = workspaceID
	if strings.TrimSpace(value.ID) == "" {
		value.ID = integrationInvocationID(value.WorkspaceID, value.ConnectorKey, value.Operation)
	}
	if strings.TrimSpace(value.Status) == "" {
		value.Status = "queued"
	}
	value.CreatedAt, value.UpdatedAt = now, now
	metadata, err := json.Marshal(nonNilMap(value.Metadata))
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, fmt.Errorf("encode integration invocation metadata: %w", err)
	}
	columns := []string{"id", "workspace_id", "connector_key", "provider_key", "connection_key", "operation", "status", "duration_ms", "request_ref", "response_ref", "error", "event_id", "object_key", "record_id", "workflow_execution_id", "metadata_json", "created_at", "updated_at"}
	args := []any{value.ID, value.WorkspaceID, value.ConnectorKey, value.ProviderKey, value.ConnectionKey, value.Operation, value.Status, value.DurationMS, value.RequestRef, value.ResponseRef, value.Error, value.EventID, value.ObjectKey, value.RecordID, value.WorkflowExecutionID, string(metadata), value.CreatedAt, value.UpdatedAt}
	if _, err := r.db.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("integration_invocations")+" ("+stringsJoinIdentifiers(r.store, columns...)+") VALUES ("+stringsJoinPlaceholders(r.store, len(columns))+")", args...); err != nil {
		return integrationmodel.IntegrationInvocation{}, fmt.Errorf("insert integration invocation: %w", err)
	}
	return value, nil
}

func (r IntegrationDeliveryStore) UpdateInvocationStatus(ctx context.Context, workspaceID, id, status string, duration int64, responseRef, errorText string) (integrationmodel.IntegrationInvocation, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return integrationmodel.IntegrationInvocation{}, fmt.Errorf("integration invocation id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := r.db.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("integration_invocations")+" SET "+r.store.Identifier("status")+" = "+r.store.Placeholder(1)+", "+r.store.Identifier("duration_ms")+" = "+r.store.Placeholder(2)+", "+r.store.Identifier("response_ref")+" = "+r.store.Placeholder(3)+", "+r.store.Identifier("error")+" = "+r.store.Placeholder(4)+", "+r.store.Identifier("updated_at")+" = "+r.store.Placeholder(5)+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(6)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(7), status, duration, responseRef, errorText, now, workspaceID, id)
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, fmt.Errorf("update integration invocation status: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, fmt.Errorf("read updated integration invocation count: %w", err)
	}
	if count == 0 {
		return integrationmodel.IntegrationInvocation{}, fmt.Errorf("integration invocation not found")
	}
	value, found, err := r.findInvocation(ctx, workspaceID, id)
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, err
	}
	if !found {
		return integrationmodel.IntegrationInvocation{}, fmt.Errorf("integration invocation not found")
	}
	return value, nil
}

func (r IntegrationDeliveryStore) CompleteInvocation(ctx context.Context, workspaceID, id, status string, duration int64, responseRef, errorText string, metadata map[string]any) (integrationmodel.IntegrationInvocation, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, err
	}
	id = strings.TrimSpace(id)
	encoded, err := json.Marshal(nonNilMap(metadata))
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, fmt.Errorf("encode integration invocation outcome: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	query := "UPDATE " + r.store.TableIdentifier("integration_invocations") + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("duration_ms") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("response_ref") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("error") + " = " + r.store.Placeholder(4) + ", " + r.store.Identifier("metadata_json") + " = " + r.store.Placeholder(5) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(6) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(7) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(8) + " AND " + r.store.Identifier("status") + " = 'prepared'"
	result, err := r.db.ExecContext(ctx, query, status, duration, responseRef, errorText, string(encoded), now, workspaceID, id)
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, fmt.Errorf("complete integration invocation: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, fmt.Errorf("read completed integration invocation count: %w", err)
	}
	if count != 1 {
		return integrationmodel.IntegrationInvocation{}, fmt.Errorf("prepared integration invocation not found")
	}
	value, found, err := r.findInvocation(ctx, workspaceID, id)
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, err
	}
	if !found {
		return integrationmodel.IntegrationInvocation{}, fmt.Errorf("integration invocation not found")
	}
	return value, nil
}

func (r IntegrationDeliveryStore) ListPreparedInvocationsForReconciliation(ctx context.Context, scope principalmodel.SystemScope, limit int, staleBefore string) ([]integrationmodel.IntegrationInvocation, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	staleBefore = strings.TrimSpace(staleBefore)
	if staleBefore == "" {
		return nil, fmt.Errorf("integration invocation reconciliation cutoff is required")
	}
	query := "SELECT " + integrationInvocationColumnsSQL(r.store) + " FROM " + r.store.TableIdentifier("integration_invocations") +
		" WHERE " + r.store.Identifier("status") + " = 'prepared' AND " + r.store.Identifier("response_ref") + " = '' AND " +
		r.store.Identifier("created_at") + " <= " + r.store.Placeholder(1) + " ORDER BY " + r.store.Identifier("created_at") + " ASC LIMIT " + r.store.Placeholder(2)
	rows, err := r.db.QueryContext(ctx, query, staleBefore, limit)
	if err != nil {
		return nil, fmt.Errorf("list integration invocations for reconciliation: %w", err)
	}
	defer rows.Close()
	values := []integrationmodel.IntegrationInvocation{}
	for rows.Next() {
		value, scanErr := scanIntegrationInvocation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list integration invocations for reconciliation: %w", err)
	}
	return values, nil
}

func (r IntegrationDeliveryStore) MarkInvocationReconciliationRequired(ctx context.Context, workspaceID, invocationID, detectedAt string) (integrationmodel.IntegrationInvocation, bool, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, false, err
	}
	invocationID = strings.TrimSpace(invocationID)
	detectedAt = strings.TrimSpace(detectedAt)
	if invocationID == "" || detectedAt == "" {
		return integrationmodel.IntegrationInvocation{}, false, fmt.Errorf("integration invocation reconciliation identity is required")
	}
	query := "UPDATE " + r.store.TableIdentifier("integration_invocations") + " SET " + r.store.Identifier("status") + " = 'reconciliation_required', " +
		r.store.Identifier("error") + " = 'backend.integration.invocation.external_receipt_missing', " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(1) +
		" WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(2) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(3) +
		" AND " + r.store.Identifier("status") + " = 'prepared' AND " + r.store.Identifier("response_ref") + " = ''"
	result, err := r.db.ExecContext(ctx, query, detectedAt, workspaceID, invocationID)
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, false, fmt.Errorf("mark integration invocation for reconciliation: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, false, fmt.Errorf("read integration invocation reconciliation result: %w", err)
	}
	if count == 0 {
		return integrationmodel.IntegrationInvocation{}, false, nil
	}
	value, found, err := r.findInvocation(ctx, workspaceID, invocationID)
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, false, err
	}
	if !found {
		return integrationmodel.IntegrationInvocation{}, false, fmt.Errorf("reconciliation integration invocation not found")
	}
	return value, true, nil
}

func (r IntegrationDeliveryStore) ListOverdueOutboxAcknowledgements(ctx context.Context, scope principalmodel.SystemScope, limit int, now string) ([]integrationmodel.IntegrationOutboxMessage, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	now = strings.TrimSpace(now)
	if now == "" {
		return nil, fmt.Errorf("integration outbox acknowledgement cutoff is required")
	}
	query := "SELECT " + integrationOutboxColumnsSQL(r.store) + " FROM " + r.store.TableIdentifier("integration_outbox_messages") +
		" WHERE " + r.store.Identifier("status") + " = 'sent' AND " + r.store.Identifier("ack_deadline_at") + " <> '' AND " +
		r.store.Identifier("ack_deadline_at") + " <= " + r.store.Placeholder(1) + " ORDER BY " + r.store.Identifier("ack_deadline_at") + " ASC LIMIT " + r.store.Placeholder(2)
	rows, err := r.db.QueryContext(ctx, query, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list overdue integration outbox acknowledgements: %w", err)
	}
	defer rows.Close()
	values := []integrationmodel.IntegrationOutboxMessage{}
	for rows.Next() {
		value, scanErr := scanIntegrationOutboxMessage(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list overdue integration outbox acknowledgements: %w", err)
	}
	return values, nil
}

func (r IntegrationDeliveryStore) MarkOutboxAcknowledgementReconciliationRequired(ctx context.Context, workspaceID, messageID, detectedAt string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	messageID = strings.TrimSpace(messageID)
	detectedAt = strings.TrimSpace(detectedAt)
	if messageID == "" || detectedAt == "" {
		return integrationmodel.IntegrationOutboxMessage{}, false, fmt.Errorf("integration outbox acknowledgement reconciliation identity is required")
	}
	query := "UPDATE " + r.store.TableIdentifier("integration_outbox_messages") + " SET " + r.store.Identifier("status") + " = 'quarantined', " +
		r.store.Identifier("error") + " = 'backend.integration.outbox.ack_timeout', " + r.store.Identifier("ack_deadline_at") + " = '', " +
		r.store.Identifier("next_attempt_at") + " = '', " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(1) +
		" WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(2) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(3) +
		" AND " + r.store.Identifier("status") + " = 'sent' AND " + r.store.Identifier("ack_deadline_at") + " <> '' AND " +
		r.store.Identifier("ack_deadline_at") + " <= " + r.store.Placeholder(4)
	result, err := r.db.ExecContext(ctx, query, detectedAt, workspaceID, messageID, detectedAt)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, fmt.Errorf("mark integration outbox acknowledgement for reconciliation: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, fmt.Errorf("read integration outbox acknowledgement reconciliation result: %w", err)
	}
	if count == 0 {
		return integrationmodel.IntegrationOutboxMessage{}, false, nil
	}
	value, found, err := r.findOutbox(ctx, workspaceID, messageID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	if !found {
		return integrationmodel.IntegrationOutboxMessage{}, false, fmt.Errorf("reconciliation integration outbox message not found")
	}
	return value, true, nil
}

func (r IntegrationDeliveryStore) ListOutbox(ctx context.Context, workspaceID, connectorKey, status string, limit int) ([]integrationmodel.IntegrationOutboxMessage, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	predicates := []ormbuilder.Predicate{}
	for _, filter := range []struct{ column, value string }{{"connector_key", connectorKey}, {"status", status}} {
		if filter.value = strings.TrimSpace(filter.value); filter.value != "" {
			predicates = append(predicates, ormbuilder.Equal(filter.column, filter.value))
		}
	}
	builder := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "integration_outbox_messages", workspaceID).
		Columns(integrationOutboxColumns...).OrderBy(ormbuilder.Descending("created_at")).Limit(limit)
	if len(predicates) != 0 {
		builder.Where(ormbuilder.And(predicates...))
	}
	query, args, buildErr := builder.Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build integration outbox query: %w", buildErr)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list integration outbox messages: %w", err)
	}
	defer rows.Close()
	out := []integrationmodel.IntegrationOutboxMessage{}
	for rows.Next() {
		value, err := scanIntegrationOutboxMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list integration outbox messages: %w", err)
	}
	return out, nil
}

func (r IntegrationDeliveryStore) InsertOutbox(ctx context.Context, workspaceID string, value integrationmodel.IntegrationOutboxMessage) (integrationmodel.IntegrationOutboxMessage, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	workspaceID, err := requireIntegrationMutationWorkspaceID(workspaceID, value.WorkspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	value.WorkspaceID = workspaceID
	ctx = integrationWorkerWorkspaceContext(ctx, workspaceID, "integration-outbox-enqueue")
	value.ConnectorKey, value.ConnectionKey, value.Operation = strings.TrimSpace(value.ConnectorKey), strings.TrimSpace(value.ConnectionKey), strings.TrimSpace(value.Operation)
	value.RequestRef, value.DedupKey = strings.TrimSpace(value.RequestRef), strings.TrimSpace(value.DedupKey)
	if value.DedupKey == "" {
		value.DedupKey = value.RequestRef
	}
	if value.DedupKey == "" {
		if value.ID == "" {
			value.ID = integrationOutboxID(value.WorkspaceID, value.ConnectorKey, value.Operation)
		}
		value.DedupKey = "legacy:" + value.ID
	}
	fingerprint, err := idempotency.Fingerprint(idempotency.FingerprintInput{
		UseCase: "integration.outbox.enqueue", ResourceType: "outbox", TargetID: value.ConnectorKey + ":" + value.ConnectionKey + ":" + value.Operation,
		Payload: map[string]any{"payload": telemetry.ApplicationPayload(value.Payload), "event_id": value.EventID, "request_ref": value.RequestRef},
	})
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("fingerprint integration outbox message: %w", err)
	}
	if value.RequestFingerprint == "" {
		value.RequestFingerprint = fingerprint
	}
	if strings.TrimSpace(value.ID) == "" {
		identity, _ := idempotency.Fingerprint(idempotency.FingerprintInput{UseCase: "integration.outbox.identity", ResourceType: "outbox", TargetID: value.WorkspaceID + ":" + value.ConnectorKey + ":" + value.ConnectionKey + ":" + value.Operation, Payload: map[string]any{"dedup_key": value.DedupKey}})
		value.ID = "integration_outbox:" + identity[:24]
	}
	if strings.TrimSpace(value.Status) == "" {
		value.Status = "queued"
	}
	value.CreatedAt, value.UpdatedAt = now, now
	value.Payload = telemetry.EnsureAsyncPayload(ctx, value.Payload)
	payload, _ := json.Marshal(nonNilMap(value.Payload))
	columns := []string{"id", "workspace_id", "connector_key", "connection_key", "operation", "status", "payload_json", "event_id", "request_ref", "dedup_key", "request_fingerprint", "response_ref", "error", "attempt_count", "next_attempt_at", "ack_deadline_at", "last_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "created_by", "created_at", "updated_at"}
	args := []any{value.ID, value.WorkspaceID, value.ConnectorKey, value.ConnectionKey, value.Operation, value.Status, string(payload), value.EventID, value.RequestRef, value.DedupKey, value.RequestFingerprint, value.ResponseRef, value.Error, value.AttemptCount, value.NextAttemptAt, value.AckDeadlineAt, value.LastAttemptAt, value.LeaseOwner, value.LeaseExpiresAt, value.FencingToken, value.CreatedBy, value.CreatedAt, value.UpdatedAt}
	if _, err := r.db.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("integration_outbox_messages")+" ("+stringsJoinIdentifiers(r.store, columns...)+") VALUES ("+stringsJoinPlaceholders(r.store, len(columns))+")", args...); err != nil {
		existing, found, readErr := r.findOutboxByDedup(ctx, value)
		if readErr == nil && found {
			if existing.RequestFingerprint != value.RequestFingerprint {
				return integrationmodel.IntegrationOutboxMessage{}, mutation.MutationConflict("integration_outbox", value.DedupKey, mutation.MutationConflictIdempotency, nil)
			}
			if existing.Status == "queued" || existing.Status == "sending" {
				if registerErr := RegisterOutboxWorkerQueueScope(ctx, r.store, r.db, existing.WorkspaceID, existing.UpdatedAt); registerErr != nil {
					return integrationmodel.IntegrationOutboxMessage{}, registerErr
				}
			}
			return existing, nil
		}
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("insert integration outbox message: %w", err)
	}
	if err := RegisterOutboxWorkerQueueScope(ctx, r.store, r.db, workspaceID, now); err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	return value, nil
}

func (r IntegrationDeliveryStore) findOutboxByDedup(ctx context.Context, value integrationmodel.IntegrationOutboxMessage) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	row := r.db.QueryRowContext(ctx, "SELECT "+integrationOutboxColumnsSQL(r.store)+" FROM "+r.store.TableIdentifier("integration_outbox_messages")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("connector_key")+" = "+r.store.Placeholder(2)+" AND "+r.store.Identifier("connection_key")+" = "+r.store.Placeholder(3)+" AND "+r.store.Identifier("operation")+" = "+r.store.Placeholder(4)+" AND "+r.store.Identifier("dedup_key")+" = "+r.store.Placeholder(5), value.WorkspaceID, value.ConnectorKey, value.ConnectionKey, value.Operation, value.DedupKey)
	existing, err := scanIntegrationOutboxMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return integrationmodel.IntegrationOutboxMessage{}, false, nil
	}
	return existing, err == nil, err
}

func (r IntegrationDeliveryStore) UpdateOutboxStatus(ctx context.Context, workspaceID, id, status, responseRef, errorText string) (integrationmodel.IntegrationOutboxMessage, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("integration outbox id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := r.db.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("integration_outbox_messages")+" SET "+r.store.Identifier("status")+" = "+r.store.Placeholder(1)+", "+r.store.Identifier("response_ref")+" = "+r.store.Placeholder(2)+", "+r.store.Identifier("error")+" = "+r.store.Placeholder(3)+", "+r.store.Identifier("next_attempt_at")+" = "+r.store.Placeholder(4)+", "+r.store.Identifier("updated_at")+" = "+r.store.Placeholder(5)+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(6)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(7), status, responseRef, errorText, "", now, workspaceID, id)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("update integration outbox status: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("read updated integration outbox count: %w", err)
	}
	if count == 0 {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("integration outbox message not found")
	}
	value, found, err := r.findOutbox(ctx, workspaceID, id)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	if !found {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("integration outbox message not found")
	}
	return value, nil
}

func (r IntegrationDeliveryStore) UpdateOutboxStatusByResponseRef(ctx context.Context, workspaceID, connectionKey, responseRef, status, errorText string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	connectionKey, responseRef = strings.TrimSpace(connectionKey), strings.TrimSpace(responseRef)
	if connectionKey == "" || responseRef == "" {
		return integrationmodel.IntegrationOutboxMessage{}, false, nil
	}
	for attempt := 0; attempt < 4; attempt++ {
		row := r.db.QueryRowContext(ctx, "SELECT "+integrationOutboxColumnsSQL(r.store)+" FROM "+r.store.TableIdentifier("integration_outbox_messages")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("connection_key")+" = "+r.store.Placeholder(2)+" AND "+r.store.Identifier("response_ref")+" = "+r.store.Placeholder(3)+" ORDER BY "+r.store.Identifier("created_at")+" DESC", workspaceID, connectionKey, responseRef)
		existing, scanErr := scanIntegrationOutboxMessage(row)
		if errors.Is(scanErr, sql.ErrNoRows) {
			return integrationmodel.IntegrationOutboxMessage{}, false, nil
		}
		if scanErr != nil {
			return integrationmodel.IntegrationOutboxMessage{}, false, scanErr
		}
		if !integrationpolicy.IntegrationDeliveryStatusAdvances(existing.Status, status) {
			return existing, true, nil
		}
		now := time.Now().UTC().Format(time.RFC3339)
		query := "UPDATE " + r.store.TableIdentifier("integration_outbox_messages") + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("error") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("next_attempt_at") + " = '', " + r.store.Identifier("ack_deadline_at") + " = '', " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(3) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(5) + " AND " + r.store.Identifier("status") + " = " + r.store.Placeholder(6)
		result, updateErr := r.db.ExecContext(ctx, query, strings.TrimSpace(status), strings.TrimSpace(errorText), now, workspaceID, existing.ID, existing.Status)
		if updateErr != nil {
			return integrationmodel.IntegrationOutboxMessage{}, true, fmt.Errorf("advance integration outbox delivery status: %w", updateErr)
		}
		changed, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return integrationmodel.IntegrationOutboxMessage{}, true, fmt.Errorf("read integration outbox delivery advance count: %w", rowsErr)
		}
		if changed == 1 {
			updated, found, readErr := r.findOutbox(ctx, workspaceID, existing.ID)
			if readErr != nil {
				return integrationmodel.IntegrationOutboxMessage{}, true, readErr
			}
			if !found {
				return integrationmodel.IntegrationOutboxMessage{}, true, fmt.Errorf("integration outbox message not found")
			}
			return updated, true, nil
		}
	}
	return integrationmodel.IntegrationOutboxMessage{}, true, fmt.Errorf("integration outbox delivery status contention")
}

func (r IntegrationDeliveryStore) ScheduleOutboxRetry(ctx context.Context, workspaceID, id string, delay int, errorText string) (integrationmodel.IntegrationOutboxMessage, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("integration outbox id is required")
	}
	if delay < 0 {
		delay = 60
	}
	now := time.Now().UTC()
	nowText, next := now.Format(time.RFC3339), now.Add(time.Duration(delay)*time.Second).Format(time.RFC3339)
	result, err := r.db.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("integration_outbox_messages")+" SET "+r.store.Identifier("status")+" = "+r.store.Placeholder(1)+", "+r.store.Identifier("error")+" = COALESCE(NULLIF(TRIM("+r.store.Placeholder(2)+"), ''), "+r.store.Identifier("error")+"), "+r.store.Identifier("attempt_count")+" = "+r.store.Identifier("attempt_count")+" + 1, "+r.store.Identifier("next_attempt_at")+" = "+r.store.Placeholder(3)+", "+r.store.Identifier("last_attempt_at")+" = "+r.store.Placeholder(4)+", "+r.store.Identifier("updated_at")+" = "+r.store.Placeholder(5)+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(6)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(7), "queued", errorText, next, nowText, nowText, workspaceID, id)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("schedule integration outbox retry: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("read scheduled integration outbox count: %w", err)
	}
	if count == 0 {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("integration outbox message not found")
	}
	value, found, err := r.findOutbox(ctx, workspaceID, id)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	if !found {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("integration outbox message not found")
	}
	return value, nil
}

func (r IntegrationDeliveryStore) findInvocation(ctx context.Context, workspaceID, id string) (integrationmodel.IntegrationInvocation, bool, error) {
	row := r.db.QueryRowContext(ctx, "SELECT "+integrationInvocationColumnsSQL(r.store)+" FROM "+r.store.TableIdentifier("integration_invocations")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(2), workspaceID, strings.TrimSpace(id))
	value, err := scanIntegrationInvocation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return integrationmodel.IntegrationInvocation{}, false, nil
	}
	return value, err == nil, err
}
func (r IntegrationDeliveryStore) findOutbox(ctx context.Context, workspaceID, id string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	row := r.db.QueryRowContext(ctx, "SELECT "+integrationOutboxColumnsSQL(r.store)+" FROM "+r.store.TableIdentifier("integration_outbox_messages")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(2), workspaceID, strings.TrimSpace(id))
	value, err := scanIntegrationOutboxMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return integrationmodel.IntegrationOutboxMessage{}, false, nil
	}
	return value, err == nil, err
}
