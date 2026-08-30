// Integration delivery persistence.
package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	ormbuilder "github.com/domainry/domainry-orm/query"
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
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "runtime_publication_outbox", workspaceID).Columns(integrationOutboxColumns...).Where(integrationPublicationPredicate(ormbuilder.Equal("id", strings.TrimSpace(id)))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	row := r.db.QueryRowContext(ctx, query, args...)
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
	insertColumns, insertValues, err := workspaceInsertValues(workspaceID, columns, args)
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, err
	}
	query, insertArgs, err := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "integration_invocations", workspaceID).Columns(insertColumns...).Values(insertValues...).Build()
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, fmt.Errorf("build integration invocation insert: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, query, insertArgs...); err != nil {
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
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "integration_invocations", workspaceID).Set("status", status).Set("duration_ms", duration).Set("response_ref", responseRef).Set("error", errorText).Set("updated_at", now).Where(ormbuilder.Equal("id", id)).Build()
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, fmt.Errorf("build integration invocation status update: %w", err)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
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
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "integration_invocations", workspaceID).Set("status", status).Set("duration_ms", duration).Set("response_ref", responseRef).Set("error", errorText).Set("metadata_json", string(encoded)).Set("updated_at", now).Where(ormbuilder.And(ormbuilder.Equal("id", id), ormbuilder.Equal("status", "prepared"))).Build()
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, fmt.Errorf("build integration invocation completion: %w", err)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
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
	query, args, err := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, "integration_invocations").Columns(integrationInvocationColumns...).Where(ormbuilder.And(ormbuilder.Equal("status", "prepared"), ormbuilder.Equal("response_ref", ""), ormbuilder.LessThanOrEqual("created_at", staleBefore))).OrderBy(ormbuilder.Ascending("created_at"), ormbuilder.Ascending("workspace_id"), ormbuilder.Ascending("id")).Limit(limit).Build()
	if err != nil {
		return nil, fmt.Errorf("build integration invocation reconciliation list: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
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
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "integration_invocations", workspaceID).Set("status", "reconciliation_required").Set("error", "backend.integration.invocation.external_receipt_missing").Set("updated_at", detectedAt).Where(ormbuilder.And(ormbuilder.Equal("id", invocationID), ormbuilder.Equal("status", "prepared"), ormbuilder.Equal("response_ref", ""))).Build()
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, false, fmt.Errorf("build integration invocation reconciliation update: %w", err)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
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
	query, args, err := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, "runtime_publication_outbox").Columns(integrationOutboxColumns...).Where(integrationPublicationPredicate(ormbuilder.And(ormbuilder.Equal("status", "sent"), ormbuilder.NotEqual("ack_deadline_at", ""), ormbuilder.LessThanOrEqual("ack_deadline_at", now)))).OrderBy(ormbuilder.Ascending("ack_deadline_at"), ormbuilder.Ascending("workspace_id"), ormbuilder.Ascending("id")).Limit(limit).Build()
	if err != nil {
		return nil, fmt.Errorf("build overdue integration outbox acknowledgement list: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
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
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "runtime_publication_outbox", workspaceID).Set("status", "quarantined").Set("error", "backend.integration.outbox.ack_timeout").Set("ack_deadline_at", "").Set("next_attempt_at", "").Set("updated_at", detectedAt).Where(integrationPublicationPredicate(ormbuilder.And(ormbuilder.Equal("id", messageID), ormbuilder.Equal("status", "sent"), ormbuilder.NotEqual("ack_deadline_at", ""), ormbuilder.LessThanOrEqual("ack_deadline_at", detectedAt)))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, fmt.Errorf("build integration outbox acknowledgement reconciliation: %w", err)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
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
	predicates := []ormbuilder.Predicate{ormbuilder.Equal("publication_type", "integration.connector")}
	for _, filter := range []struct{ column, value string }{{"connector_key", connectorKey}, {"status", status}} {
		if filter.value = strings.TrimSpace(filter.value); filter.value != "" {
			predicates = append(predicates, ormbuilder.Equal(filter.column, filter.value))
		}
	}
	builder := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "runtime_publication_outbox", workspaceID).
		Columns(integrationOutboxColumns...).OrderBy(ormbuilder.Descending("created_at")).Limit(limit)
	builder.Where(ormbuilder.And(predicates...))
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
	columns := []string{"id", "workspace_id", "publication_type", "connector_key", "connection_key", "operation", "status", "payload_json", "event_id", "request_ref", "dedup_key", "request_fingerprint", "response_ref", "error", "attempt_count", "next_attempt_at", "ack_deadline_at", "last_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "created_by", "created_at", "updated_at"}
	args := []any{value.ID, value.WorkspaceID, "integration.connector", value.ConnectorKey, value.ConnectionKey, value.Operation, value.Status, string(payload), value.EventID, value.RequestRef, value.DedupKey, value.RequestFingerprint, value.ResponseRef, value.Error, value.AttemptCount, value.NextAttemptAt, value.AckDeadlineAt, value.LastAttemptAt, value.LeaseOwner, value.LeaseExpiresAt, value.FencingToken, value.CreatedBy, value.CreatedAt, value.UpdatedAt}
	insertColumns, insertValues, err := workspaceInsertValues(workspaceID, columns, args)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	query, insertArgs, err := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "runtime_publication_outbox", workspaceID).Columns(insertColumns...).Values(insertValues...).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("build integration outbox insert: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, query, insertArgs...); err != nil {
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
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "runtime_publication_outbox", value.WorkspaceID).Columns(integrationOutboxColumns...).Where(integrationPublicationPredicate(ormbuilder.And(ormbuilder.Equal("connector_key", value.ConnectorKey), ormbuilder.Equal("connection_key", value.ConnectionKey), ormbuilder.Equal("operation", value.Operation), ormbuilder.Equal("dedup_key", value.DedupKey)))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	row := r.db.QueryRowContext(ctx, query, args...)
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
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "runtime_publication_outbox", workspaceID).Set("status", status).Set("response_ref", responseRef).Set("error", errorText).Set("next_attempt_at", "").Set("updated_at", now).Where(integrationPublicationPredicate(ormbuilder.Equal("id", id))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("build integration outbox status update: %w", err)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
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
		query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "runtime_publication_outbox", workspaceID).Columns(integrationOutboxColumns...).Where(integrationPublicationPredicate(ormbuilder.And(ormbuilder.Equal("connection_key", connectionKey), ormbuilder.Equal("response_ref", responseRef)))).OrderBy(ormbuilder.Descending("created_at"), ormbuilder.Descending("id")).Limit(1).Build()
		if buildErr != nil {
			return integrationmodel.IntegrationOutboxMessage{}, false, buildErr
		}
		row := r.db.QueryRowContext(ctx, query, args...)
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
		query, args, buildErr = ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "runtime_publication_outbox", workspaceID).Set("status", strings.TrimSpace(status)).Set("error", strings.TrimSpace(errorText)).Set("next_attempt_at", "").Set("ack_deadline_at", "").Set("updated_at", now).Where(integrationPublicationPredicate(ormbuilder.And(ormbuilder.Equal("id", existing.ID), ormbuilder.Equal("status", existing.Status)))).Build()
		if buildErr != nil {
			return integrationmodel.IntegrationOutboxMessage{}, true, buildErr
		}
		result, updateErr := r.db.ExecContext(ctx, query, args...)
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
	builder := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "runtime_publication_outbox", workspaceID).Set("status", "queued").SetExpression("attempt_count", ormbuilder.Add(ormbuilder.Column("attempt_count"), ormbuilder.Value(1))).Set("next_attempt_at", next).Set("last_attempt_at", nowText).Set("updated_at", nowText)
	if strings.TrimSpace(errorText) != "" {
		builder.Set("error", errorText)
	}
	query, args, err := builder.Where(integrationPublicationPredicate(ormbuilder.Equal("id", id))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("build integration outbox retry: %w", err)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
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
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "integration_invocations", workspaceID).Columns(integrationInvocationColumns...).Where(ormbuilder.Equal("id", strings.TrimSpace(id))).Build()
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, false, err
	}
	row := r.db.QueryRowContext(ctx, query, args...)
	value, err := scanIntegrationInvocation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return integrationmodel.IntegrationInvocation{}, false, nil
	}
	return value, err == nil, err
}
func (r IntegrationDeliveryStore) findOutbox(ctx context.Context, workspaceID, id string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "runtime_publication_outbox", workspaceID).Columns(integrationOutboxColumns...).Where(integrationPublicationPredicate(ormbuilder.Equal("id", strings.TrimSpace(id)))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	row := r.db.QueryRowContext(ctx, query, args...)
	value, err := scanIntegrationOutboxMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return integrationmodel.IntegrationOutboxMessage{}, false, nil
	}
	return value, err == nil, err
}
