package publicationhandoff

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

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-foundation/telemetry"
	"github.com/domainry/domainry-orm/query"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type PublicationStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewPublicationStore(store *database.RuntimeStore) PublicationStore {
	return PublicationStore{store: store, db: store.DB()}
}

func (s PublicationStore) GetOutbox(ctx context.Context, workspaceID, id string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	workspaceID, err := publicationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	queryValue, args, err := query.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "_publication_outbox", workspaceID).Columns(publicationColumns...).Where(publicationPredicate(query.Equal("id", strings.TrimSpace(id)))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	message, err := scanPublication(s.db.QueryRowContext(ctx, queryValue, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return integrationmodel.IntegrationOutboxMessage{}, false, nil
	}
	return message, err == nil, err
}
func (s PublicationStore) ListOutbox(ctx context.Context, workspaceID, connectorKey, status string, limit int) ([]integrationmodel.IntegrationOutboxMessage, error) {
	workspaceID, err := publicationWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	predicates := []query.Predicate{query.Equal("publication_type", "integration.connector")}
	if connectorKey = strings.TrimSpace(connectorKey); connectorKey != "" {
		predicates = append(predicates, query.Equal("connector_key", connectorKey))
	}
	if status = strings.TrimSpace(status); status != "" {
		predicates = append(predicates, query.Equal("status", status))
	}
	queryValue, args, err := query.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "_publication_outbox", workspaceID).Columns(publicationColumns...).Where(query.And(predicates...)).OrderBy(query.Descending("created_at")).Limit(limit).Build()
	if err != nil {
		return nil, fmt.Errorf("build Runtime publication query: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, queryValue, args...)
	if err != nil {
		return nil, fmt.Errorf("list Runtime publications: %w", err)
	}
	defer rows.Close()
	values := []integrationmodel.IntegrationOutboxMessage{}
	for rows.Next() {
		value, scanErr := scanPublication(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
func (s PublicationStore) InsertOutbox(ctx context.Context, workspaceID string, value integrationmodel.IntegrationOutboxMessage) (integrationmodel.IntegrationOutboxMessage, error) {
	workspaceID, err := publicationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	if embedded := strings.TrimSpace(value.WorkspaceID); embedded != "" && embedded != workspaceID {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("Runtime publication workspace mismatch")
	}
	value.WorkspaceID = workspaceID
	value.ConnectorKey, value.ConnectionKey, value.Operation = strings.TrimSpace(value.ConnectorKey), strings.TrimSpace(value.ConnectionKey), strings.TrimSpace(value.Operation)
	value.RequestRef, value.DedupKey = strings.TrimSpace(value.RequestRef), strings.TrimSpace(value.DedupKey)
	if value.DedupKey == "" {
		value.DedupKey = value.RequestRef
	}
	if value.DedupKey == "" {
		if strings.TrimSpace(value.ID) == "" {
			value.ID = publicationIdentity(workspaceID, value.ConnectorKey, value.ConnectionKey, value.Operation, fmt.Sprint(time.Now().UTC().UnixNano()))
		}
		value.DedupKey = "legacy:" + value.ID
	}
	fingerprint, err := idempotency.Fingerprint(idempotency.FingerprintInput{
		UseCase: "integration.outbox.enqueue", ResourceType: "outbox", TargetID: value.ConnectorKey + ":" + value.ConnectionKey + ":" + value.Operation,
		Payload: map[string]any{"payload": telemetry.ApplicationPayload(value.Payload), "event_id": value.EventID, "request_ref": value.RequestRef},
	})
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("fingerprint Runtime publication: %w", err)
	}
	if value.RequestFingerprint == "" {
		value.RequestFingerprint = fingerprint
	}
	if strings.TrimSpace(value.ID) == "" {
		value.ID = publicationIdentity(workspaceID, value.ConnectorKey, value.ConnectionKey, value.Operation, value.DedupKey)
	}
	if strings.TrimSpace(value.Status) == "" {
		value.Status = "queued"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	value.CreatedAt, value.UpdatedAt = now, now
	value.Payload = telemetry.EnsureAsyncPayload(ctx, value.Payload)
	if value.Payload == nil {
		value.Payload = map[string]any{}
	}
	payload, err := json.Marshal(value.Payload)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("encode Runtime publication payload: %w", err)
	}
	columns := []string{"id", "publication_type", "connector_key", "connection_key", "operation", "status", "payload_json", "event_id", "request_ref", "dedup_key", "request_fingerprint", "response_ref", "error", "attempt_count", "next_attempt_at", "ack_deadline_at", "last_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "created_by", "created_at", "updated_at"}
	values := []any{value.ID, "integration.connector", value.ConnectorKey, value.ConnectionKey, value.Operation, value.Status, string(payload), value.EventID, value.RequestRef, value.DedupKey, value.RequestFingerprint, value.ResponseRef, value.Error, value.AttemptCount, value.NextAttemptAt, value.AckDeadlineAt, value.LastAttemptAt, value.LeaseOwner, value.LeaseExpiresAt, value.FencingToken, value.CreatedBy, value.CreatedAt, value.UpdatedAt}
	queryValue, args, err := query.NewWorkspaceInsertBuilder(s.store.SQLRenderer, "_publication_outbox", workspaceID).Columns(columns...).Values(values...).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("build Runtime publication insert: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, queryValue, args...); err != nil {
		existing, found, readErr := s.findByDedup(ctx, value)
		if readErr == nil && found {
			if existing.RequestFingerprint != value.RequestFingerprint {
				return integrationmodel.IntegrationOutboxMessage{}, mutation.MutationConflict("runtime_publication", value.DedupKey, mutation.MutationConflictIdempotency, nil)
			}
			if existing.Status == "queued" || existing.Status == "sending" {
				if registerErr := registerPublicationWorkerScope(ctx, s.store, s.db, existing.WorkspaceID, existing.UpdatedAt); registerErr != nil {
					return integrationmodel.IntegrationOutboxMessage{}, registerErr
				}
			}
			return existing, nil
		}
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("insert Runtime publication: %w", err)
	}
	if err := registerPublicationWorkerScope(ctx, s.store, s.db, workspaceID, now); err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	return value, nil
}
func (s PublicationStore) UpdateOutboxStatus(ctx context.Context, workspaceID, id, status, responseRef, errorText string) (integrationmodel.IntegrationOutboxMessage, error) {
	workspaceID, err := publicationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("Runtime publication id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	queryValue, args, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "_publication_outbox", workspaceID).
		Set("status", strings.TrimSpace(status)).Set("response_ref", strings.TrimSpace(responseRef)).Set("error", strings.TrimSpace(errorText)).Set("next_attempt_at", "").Set("updated_at", now).
		Where(publicationPredicate(query.Equal("id", id))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("build Runtime publication status update: %w", err)
	}
	result, err := s.db.ExecContext(ctx, queryValue, args...)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("update Runtime publication status: %w", err)
	}
	if count, countErr := result.RowsAffected(); countErr != nil || count == 0 {
		if countErr != nil {
			return integrationmodel.IntegrationOutboxMessage{}, countErr
		}
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("Runtime publication not found")
	}
	value, found, err := s.GetOutbox(ctx, workspaceID, id)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	if !found {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("Runtime publication not found")
	}
	return value, nil
}
func (s PublicationStore) UpdateOutboxStatusByResponseRef(ctx context.Context, workspaceID, connectionKey, responseRef, status, errorText string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	workspaceID, err := publicationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	connectionKey, responseRef = strings.TrimSpace(connectionKey), strings.TrimSpace(responseRef)
	if connectionKey == "" || responseRef == "" {
		return integrationmodel.IntegrationOutboxMessage{}, false, nil
	}
	for attempt := 0; attempt < 4; attempt++ {
		queryValue, args, buildErr := query.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "_publication_outbox", workspaceID).Columns(publicationColumns...).Where(publicationPredicate(query.And(query.Equal("connection_key", connectionKey), query.Equal("response_ref", responseRef)))).OrderBy(query.Descending("created_at"), query.Descending("id")).Limit(1).Build()
		if buildErr != nil {
			return integrationmodel.IntegrationOutboxMessage{}, false, buildErr
		}
		existing, scanErr := scanPublication(s.db.QueryRowContext(ctx, queryValue, args...))
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
		queryValue, args, buildErr = query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "_publication_outbox", workspaceID).
			Set("status", strings.TrimSpace(status)).Set("error", strings.TrimSpace(errorText)).Set("next_attempt_at", "").Set("ack_deadline_at", "").Set("updated_at", now).
			Where(publicationPredicate(query.And(query.Equal("id", existing.ID), query.Equal("status", existing.Status)))).Build()
		if buildErr != nil {
			return integrationmodel.IntegrationOutboxMessage{}, true, buildErr
		}
		result, updateErr := s.db.ExecContext(ctx, queryValue, args...)
		if updateErr != nil {
			return integrationmodel.IntegrationOutboxMessage{}, true, fmt.Errorf("advance Runtime publication delivery status: %w", updateErr)
		}
		changed, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return integrationmodel.IntegrationOutboxMessage{}, true, rowsErr
		}
		if changed == 1 {
			updated, found, readErr := s.GetOutbox(ctx, workspaceID, existing.ID)
			if readErr != nil || !found {
				if readErr != nil {
					return integrationmodel.IntegrationOutboxMessage{}, true, readErr
				}
				return integrationmodel.IntegrationOutboxMessage{}, true, fmt.Errorf("Runtime publication not found")
			}
			return updated, true, nil
		}
	}
	return integrationmodel.IntegrationOutboxMessage{}, true, fmt.Errorf("Runtime publication delivery status contention")
}
func (s PublicationStore) ScheduleOutboxRetry(ctx context.Context, workspaceID, id string, delay int, errorText string) (integrationmodel.IntegrationOutboxMessage, error) {
	workspaceID, err := publicationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("Runtime publication id is required")
	}
	if delay < 0 {
		delay = 60
	}
	now := time.Now().UTC()
	nowText, next := now.Format(time.RFC3339), now.Add(time.Duration(delay)*time.Second).Format(time.RFC3339)
	builder := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "_publication_outbox", workspaceID).
		Set("status", "queued").SetExpression("attempt_count", query.Add(query.Column("attempt_count"), query.Value(1))).Set("next_attempt_at", next).Set("last_attempt_at", nowText).Set("updated_at", nowText)
	if errorText = strings.TrimSpace(errorText); errorText != "" {
		builder.Set("error", errorText)
	}
	queryValue, args, err := builder.Where(publicationPredicate(query.Equal("id", id))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("build Runtime publication retry: %w", err)
	}
	result, err := s.db.ExecContext(ctx, queryValue, args...)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("schedule Runtime publication retry: %w", err)
	}
	if count, countErr := result.RowsAffected(); countErr != nil || count == 0 {
		if countErr != nil {
			return integrationmodel.IntegrationOutboxMessage{}, countErr
		}
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("Runtime publication not found")
	}
	value, found, err := s.GetOutbox(ctx, workspaceID, id)
	if err != nil || !found {
		if err != nil {
			return integrationmodel.IntegrationOutboxMessage{}, err
		}
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("Runtime publication not found")
	}
	return value, nil
}
func (s PublicationStore) ListOverdueOutboxAcknowledgements(ctx context.Context, scope principalmodel.SystemScope, limit int, now string) ([]integrationmodel.IntegrationOutboxMessage, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if now = strings.TrimSpace(now); now == "" {
		return nil, fmt.Errorf("Runtime publication acknowledgement cutoff is required")
	}
	queryValue, args, err := query.NewSelectBuilder(s.store.SQLRenderer, "_publication_outbox").Columns(publicationColumns...).Where(publicationPredicate(query.And(query.Equal("status", "sent"), query.NotEqual("ack_deadline_at", ""), query.LessThanOrEqual("ack_deadline_at", now)))).OrderBy(query.Ascending("ack_deadline_at"), query.Ascending("workspace_id"), query.Ascending("id")).Limit(limit).Build()
	if err != nil {
		return nil, fmt.Errorf("build overdue Runtime publication acknowledgement list: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, queryValue, args...)
	if err != nil {
		return nil, fmt.Errorf("list overdue Runtime publication acknowledgements: %w", err)
	}
	defer rows.Close()
	values := []integrationmodel.IntegrationOutboxMessage{}
	for rows.Next() {
		value, scanErr := scanPublication(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
func (s PublicationStore) MarkOutboxAcknowledgementReconciliationRequired(ctx context.Context, workspaceID, messageID, detectedAt string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	workspaceID, err := publicationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	messageID, detectedAt = strings.TrimSpace(messageID), strings.TrimSpace(detectedAt)
	if messageID == "" || detectedAt == "" {
		return integrationmodel.IntegrationOutboxMessage{}, false, fmt.Errorf("Runtime publication acknowledgement identity is required")
	}
	queryValue, args, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "_publication_outbox", workspaceID).
		Set("status", "quarantined").Set("error", "backend.integration.outbox.ack_timeout").Set("ack_deadline_at", "").Set("next_attempt_at", "").Set("updated_at", detectedAt).
		Where(publicationPredicate(query.And(query.Equal("id", messageID), query.Equal("status", "sent"), query.NotEqual("ack_deadline_at", ""), query.LessThanOrEqual("ack_deadline_at", detectedAt)))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, fmt.Errorf("build Runtime publication acknowledgement reconciliation: %w", err)
	}
	result, err := s.db.ExecContext(ctx, queryValue, args...)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, fmt.Errorf("mark Runtime publication acknowledgement: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	if count == 0 {
		return integrationmodel.IntegrationOutboxMessage{}, false, nil
	}
	value, found, err := s.GetOutbox(ctx, workspaceID, messageID)
	if err != nil || !found {
		if err != nil {
			return integrationmodel.IntegrationOutboxMessage{}, false, err
		}
		return integrationmodel.IntegrationOutboxMessage{}, false, fmt.Errorf("Runtime publication not found")
	}
	return value, true, nil
}

var _ integrationrepository.RuntimePublicationRepository = PublicationStore{}
var _ integrationrepository.IntegrationOutboxReader = PublicationStore{}
var _ integrationrepository.IntegrationAcknowledgementReconciliationRepository = PublicationStore{}

var publicationColumns = []string{
	"id", "workspace_id", "connector_key", "connection_key", "operation", "status", "payload_json", "event_id", "request_ref", "dedup_key", "request_fingerprint", "response_ref", "error", "attempt_count", "next_attempt_at", "ack_deadline_at", "last_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "created_by", "created_at", "updated_at",
}

type publicationScanner interface{ Scan(...any) error }

func scanPublication(row publicationScanner) (integrationmodel.IntegrationOutboxMessage, error) {
	var message integrationmodel.IntegrationOutboxMessage
	var payloadJSON string
	var connectionKey, eventID, requestRef, responseRef, errorText sql.NullString
	if err := row.Scan(&message.ID, &message.WorkspaceID, &message.ConnectorKey, &connectionKey, &message.Operation, &message.Status, &payloadJSON, &eventID, &requestRef, &message.DedupKey, &message.RequestFingerprint, &responseRef, &errorText, &message.AttemptCount, &message.NextAttemptAt, &message.AckDeadlineAt, &message.LastAttemptAt, &message.LeaseOwner, &message.LeaseExpiresAt, &message.FencingToken, &message.CreatedBy, &message.CreatedAt, &message.UpdatedAt); err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	message.ConnectionKey, message.EventID, message.RequestRef = connectionKey.String, eventID.String, requestRef.String
	message.ResponseRef, message.Error = responseRef.String, errorText.String
	_ = json.Unmarshal([]byte(payloadJSON), &message.Payload)
	if message.Payload == nil {
		message.Payload = map[string]any{}
	}
	return message, nil
}

func publicationWorkspaceID(value string) (string, error) {
	workspaceID, err := principalmodel.NewWorkspaceID(value)
	if err != nil {
		return "", fmt.Errorf("Runtime publication workspace: %w", err)
	}
	return workspaceID.String(), nil
}

func publicationPredicate(predicate query.Predicate) query.Predicate {
	return query.And(query.Equal("publication_type", "integration.connector"), predicate)
}

func publicationIdentity(workspaceID, connectorKey, connectionKey, operation, dedupKey string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{workspaceID, connectorKey, connectionKey, operation, dedupKey}, "\x00")))
	return "integration_outbox:" + hex.EncodeToString(digest[:])[:24]
}

func WorkspaceID(value string) string { return strings.TrimSpace(value) }

func OutboxDedupID(workspaceID, connectorKey, connectionKey, operation, dedupKey string) string {
	return publicationIdentity(strings.TrimSpace(workspaceID), strings.TrimSpace(connectorKey), strings.TrimSpace(connectionKey), strings.TrimSpace(operation), strings.TrimSpace(dedupKey))
}

func (s PublicationStore) findByDedup(ctx context.Context, value integrationmodel.IntegrationOutboxMessage) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	queryValue, args, err := query.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "_publication_outbox", value.WorkspaceID).Columns(publicationColumns...).Where(publicationPredicate(query.And(query.Equal("connector_key", value.ConnectorKey), query.Equal("connection_key", value.ConnectionKey), query.Equal("operation", value.Operation), query.Equal("dedup_key", value.DedupKey)))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	existing, err := scanPublication(s.db.QueryRowContext(ctx, queryValue, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return integrationmodel.IntegrationOutboxMessage{}, false, nil
	}
	return existing, err == nil, err
}

type publicationScopeExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func registerPublicationWorkerScope(ctx context.Context, store *database.RuntimeStore, executor publicationScopeExecutor, workspaceID, updatedAt string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if store == nil || executor == nil || workspaceID == "" {
		return fmt.Errorf("Runtime publication worker scope is required")
	}
	if updatedAt = strings.TrimSpace(updatedAt); updatedAt == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	digest := sha256.Sum256([]byte("integration_outbox\x00" + workspaceID))
	id := "worker_scope:" + hex.EncodeToString(digest[:12])
	update, updateArgs, err := query.NewUpdateBuilder(store.SQLRenderer, "_worker_queue_scopes").Set("updated_at", updatedAt).Where(query.Equal("id", id)).Build()
	if err != nil {
		return fmt.Errorf("build Runtime publication worker scope refresh: %w", err)
	}
	updated, err := executor.ExecContext(ctx, update, updateArgs...)
	if err != nil {
		return fmt.Errorf("refresh Runtime publication worker scope: %w", err)
	}
	if affected, rowsErr := updated.RowsAffected(); rowsErr != nil {
		return rowsErr
	} else if affected > 0 {
		return nil
	}
	insert, insertArgs, err := query.NewInsertBuilder(store.SQLRenderer, "_worker_queue_scopes").Columns("id", "queue_kind", "scope_key", "updated_at").Values(id, "integration_outbox", workspaceID, updatedAt).Build()
	if err != nil {
		return fmt.Errorf("build Runtime publication worker scope registration: %w", err)
	}
	if _, err := executor.ExecContext(ctx, insert, insertArgs...); err != nil {
		retried, retryErr := executor.ExecContext(ctx, update, updateArgs...)
		if retryErr == nil {
			if affected, rowsErr := retried.RowsAffected(); rowsErr == nil && affected > 0 {
				return nil
			}
		}
		return fmt.Errorf("register Runtime publication worker scope: %w", err)
	}
	return nil
}

func RegisterWorkerScope(ctx context.Context, store *database.RuntimeStore, executor publicationScopeExecutor, workspaceID, updatedAt string) error {
	return registerPublicationWorkerScope(ctx, store, executor, workspaceID, updatedAt)
}
