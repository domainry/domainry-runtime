package publicationhandoff

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	publicationrepository "github.com/domainry/domainry-runtime/runtime/domain/publication/repository"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-foundation/requestcontext"
	"github.com/domainry/domainry-foundation/telemetry"
	"github.com/domainry/domainry-orm/query"
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

func (s PublicationStore) GetOutbox(ctx context.Context, workspaceID, id string) (publicationmodel.Message, bool, error) {
	workspaceID, err := publicationWorkspaceID(workspaceID)
	if err != nil {
		return publicationmodel.Message{}, false, err
	}
	queryValue, args, err := query.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "_publication_outbox", workspaceID).Columns(publicationColumns...).Where(publicationPredicate(query.Equal("id", strings.TrimSpace(id)))).Build()
	if err != nil {
		return publicationmodel.Message{}, false, err
	}
	message, err := scanPublication(s.db.QueryRowContext(ctx, queryValue, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return publicationmodel.Message{}, false, nil
	}
	return message, err == nil, err
}
func (s PublicationStore) ListOutbox(ctx context.Context, workspaceID, connectorKey, status string, limit int) ([]publicationmodel.Message, error) {
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
	values := []publicationmodel.Message{}
	for rows.Next() {
		value, scanErr := scanPublication(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
func (s PublicationStore) InsertOutbox(ctx context.Context, workspaceID string, value publicationmodel.Message) (publicationmodel.Message, error) {
	workspaceID, err := publicationWorkspaceID(workspaceID)
	if err != nil {
		return publicationmodel.Message{}, err
	}
	if embedded := strings.TrimSpace(value.WorkspaceID); embedded != "" && embedded != workspaceID {
		return publicationmodel.Message{}, fmt.Errorf("Runtime publication workspace mismatch")
	}
	value.WorkspaceID = workspaceID
	value.ConnectorKey, value.ConnectionKey, value.Operation = strings.TrimSpace(value.ConnectorKey), strings.TrimSpace(value.ConnectionKey), strings.TrimSpace(value.Operation)
	value.RequestRef, value.DedupKey = strings.TrimSpace(value.RequestRef), strings.TrimSpace(value.DedupKey)
	if value.DedupKey == "" {
		value.DedupKey = value.RequestRef
	}
	if value.DedupKey == "" {
		return publicationmodel.Message{}, fmt.Errorf("Runtime publication deduplication identity is required")
	}
	fingerprint, err := idempotency.Fingerprint(idempotency.FingerprintInput{
		UseCase: "runtime.publication.enqueue", ResourceType: "outbox", TargetID: value.ConnectorKey + ":" + value.ConnectionKey + ":" + value.Operation,
		Payload: map[string]any{"payload": telemetry.ApplicationPayload(value.Payload), "event_id": value.EventID, "request_ref": value.RequestRef},
	})
	if err != nil {
		return publicationmodel.Message{}, fmt.Errorf("fingerprint Runtime publication: %w", err)
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
		return publicationmodel.Message{}, fmt.Errorf("encode Runtime publication payload: %w", err)
	}
	columns := []string{"id", "publication_type", "operation_id", "connector_key", "connection_key", "operation", "status", "payload_json", "event_id", "request_ref", "dedup_key", "request_fingerprint", "response_ref", "error", "attempt_count", "next_attempt_at", "last_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "created_by", "created_at", "updated_at"}
	values := []any{value.ID, "integration.connector", value.OperationID, value.ConnectorKey, value.ConnectionKey, value.Operation, value.Status, string(payload), value.EventID, value.RequestRef, value.DedupKey, value.RequestFingerprint, value.ResponseRef, value.Error, value.AttemptCount, value.NextAttemptAt, value.LastAttemptAt, value.LeaseOwner, value.LeaseExpiresAt, value.FencingToken, value.CreatedBy, value.CreatedAt, value.UpdatedAt}
	if err := s.store.GuardSubjectEvidenceWrite(ctx, s.db, workspaceID, "_publication_outbox", columns, values); err != nil {
		return publicationmodel.Message{}, err
	}
	queryValue, args, err := query.NewWorkspaceInsertBuilder(s.store.SQLRenderer, "_publication_outbox", workspaceID).Columns(columns...).Values(values...).Build()
	if err != nil {
		return publicationmodel.Message{}, fmt.Errorf("build Runtime publication insert: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, queryValue, args...); err != nil {
		existing, found, readErr := s.findByDedup(ctx, value)
		if readErr == nil && found {
			if existing.RequestFingerprint != value.RequestFingerprint {
				return publicationmodel.Message{}, mutation.MutationConflict("runtime_publication", value.DedupKey, mutation.MutationConflictIdempotency, nil)
			}
			if existing.Status == "queued" || existing.Status == "sending" {
				if registerErr := registerPublicationWorkerScope(ctx, s.store, s.db, existing.WorkspaceID, existing.UpdatedAt); registerErr != nil {
					return publicationmodel.Message{}, registerErr
				}
			}
			return existing, nil
		}
		return publicationmodel.Message{}, fmt.Errorf("insert Runtime publication: %w", err)
	}
	if err := registerPublicationWorkerScope(ctx, s.store, s.db, workspaceID, now); err != nil {
		return publicationmodel.Message{}, err
	}
	return value, nil
}
func (s PublicationStore) UpdateOutboxStatus(ctx context.Context, workspaceID, id, status, responseRef, errorText string) (publicationmodel.Message, error) {
	workspaceID, err := publicationWorkspaceID(workspaceID)
	if err != nil {
		return publicationmodel.Message{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return publicationmodel.Message{}, fmt.Errorf("Runtime publication id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	builder := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "_publication_outbox", workspaceID)
	if operationID := requestcontext.OwnerExecutionID(ctx); operationID != "" {
		builder.Set("operation_id", operationID)
	}
	queryValue, args, err := builder.
		Set("status", strings.TrimSpace(status)).Set("response_ref", strings.TrimSpace(responseRef)).Set("error", strings.TrimSpace(errorText)).Set("next_attempt_at", "").Set("updated_at", now).
		Where(publicationPredicate(query.And(query.Equal("id", id), s.store.SubjectEvidenceWriteAllowed(workspaceID, "_publication_outbox", id)))).Build()
	if err != nil {
		return publicationmodel.Message{}, fmt.Errorf("build Runtime publication status update: %w", err)
	}
	result, err := s.db.ExecContext(ctx, queryValue, args...)
	if err != nil {
		return publicationmodel.Message{}, fmt.Errorf("update Runtime publication status: %w", err)
	}
	if count, countErr := result.RowsAffected(); countErr != nil || count == 0 {
		if countErr != nil {
			return publicationmodel.Message{}, countErr
		}
		return publicationmodel.Message{}, fmt.Errorf("Runtime publication not found")
	}
	value, found, err := s.GetOutbox(ctx, workspaceID, id)
	if err != nil {
		return publicationmodel.Message{}, err
	}
	if !found {
		return publicationmodel.Message{}, fmt.Errorf("Runtime publication not found")
	}
	return value, nil
}
func (s PublicationStore) ScheduleOutboxRetry(ctx context.Context, workspaceID, id string, delay int, errorText string) (publicationmodel.Message, error) {
	workspaceID, err := publicationWorkspaceID(workspaceID)
	if err != nil {
		return publicationmodel.Message{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return publicationmodel.Message{}, fmt.Errorf("Runtime publication id is required")
	}
	if delay < 0 {
		delay = 60
	}
	now := time.Now().UTC()
	nowText, next := now.Format(time.RFC3339), now.Add(time.Duration(delay)*time.Second).Format(time.RFC3339)
	builder := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "_publication_outbox", workspaceID).
		Set("status", "queued").SetExpression("attempt_count", query.Add(query.Column("attempt_count"), query.Value(1))).Set("next_attempt_at", next).Set("last_attempt_at", nowText).Set("updated_at", nowText)
	if operationID := requestcontext.OwnerExecutionID(ctx); operationID != "" {
		builder.Set("operation_id", operationID)
	}
	if errorText = strings.TrimSpace(errorText); errorText != "" {
		builder.Set("error", errorText)
	}
	queryValue, args, err := builder.Where(publicationPredicate(query.And(query.Equal("id", id), s.store.SubjectEvidenceWriteAllowed(workspaceID, "_publication_outbox", id)))).Build()
	if err != nil {
		return publicationmodel.Message{}, fmt.Errorf("build Runtime publication retry: %w", err)
	}
	result, err := s.db.ExecContext(ctx, queryValue, args...)
	if err != nil {
		return publicationmodel.Message{}, fmt.Errorf("schedule Runtime publication retry: %w", err)
	}
	if count, countErr := result.RowsAffected(); countErr != nil || count == 0 {
		if countErr != nil {
			return publicationmodel.Message{}, countErr
		}
		return publicationmodel.Message{}, fmt.Errorf("Runtime publication not found")
	}
	value, found, err := s.GetOutbox(ctx, workspaceID, id)
	if err != nil || !found {
		if err != nil {
			return publicationmodel.Message{}, err
		}
		return publicationmodel.Message{}, fmt.Errorf("Runtime publication not found")
	}
	return value, nil
}

var _ publicationrepository.Repository = PublicationStore{}
var _ publicationrepository.Reader = PublicationStore{}

var publicationColumns = []string{
	"id", "workspace_id", "operation_id", "connector_key", "connection_key", "operation", "status", "payload_json", "event_id", "request_ref", "dedup_key", "request_fingerprint", "response_ref", "error", "attempt_count", "next_attempt_at", "last_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "created_by", "created_at", "updated_at",
}

type publicationScanner interface{ Scan(...any) error }

func scanPublication(row publicationScanner) (publicationmodel.Message, error) {
	var message publicationmodel.Message
	var payloadJSON string
	var connectionKey, eventID, requestRef, responseRef, errorText sql.NullString
	if err := row.Scan(&message.ID, &message.WorkspaceID, &message.OperationID, &message.ConnectorKey, &connectionKey, &message.Operation, &message.Status, &payloadJSON, &eventID, &requestRef, &message.DedupKey, &message.RequestFingerprint, &responseRef, &errorText, &message.AttemptCount, &message.NextAttemptAt, &message.LastAttemptAt, &message.LeaseOwner, &message.LeaseExpiresAt, &message.FencingToken, &message.CreatedBy, &message.CreatedAt, &message.UpdatedAt); err != nil {
		return publicationmodel.Message{}, err
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
	return "publication_handoff:" + hex.EncodeToString(digest[:])[:24]
}

func WorkspaceID(value string) string { return strings.TrimSpace(value) }

func OutboxDedupID(workspaceID, connectorKey, connectionKey, operation, dedupKey string) string {
	return publicationIdentity(strings.TrimSpace(workspaceID), strings.TrimSpace(connectorKey), strings.TrimSpace(connectionKey), strings.TrimSpace(operation), strings.TrimSpace(dedupKey))
}

func (s PublicationStore) findByDedup(ctx context.Context, value publicationmodel.Message) (publicationmodel.Message, bool, error) {
	queryValue, args, err := query.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "_publication_outbox", value.WorkspaceID).Columns(publicationColumns...).Where(publicationPredicate(query.And(query.Equal("connector_key", value.ConnectorKey), query.Equal("connection_key", value.ConnectionKey), query.Equal("operation", value.Operation), query.Equal("dedup_key", value.DedupKey)))).Build()
	if err != nil {
		return publicationmodel.Message{}, false, err
	}
	existing, err := scanPublication(s.db.QueryRowContext(ctx, queryValue, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return publicationmodel.Message{}, false, nil
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
	return store.RegisterWorkerQueueScope(ctx, executor, database.WorkerScopeOwnerRuntimePublicationOutbox, workspaceID, updatedAt)
}

func RegisterWorkerScope(ctx context.Context, store *database.RuntimeStore, executor publicationScopeExecutor, workspaceID, updatedAt string) error {
	return registerPublicationWorkerScope(ctx, store, executor, workspaceID, updatedAt)
}
