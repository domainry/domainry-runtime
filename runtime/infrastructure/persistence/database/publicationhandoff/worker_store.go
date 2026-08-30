package publicationhandoff

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-foundation/requestcontext"
	ormbuilder "github.com/domainry/domainry-orm/query"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/capacity"
)

// WorkerStore owns leasing and retry state for the Runtime handoff only.
type WorkerStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewWorkerStore(store *database.RuntimeStore) WorkerStore {
	return WorkerStore{store: store, db: store.DB()}
}

func (s WorkerStore) ListDueOutbox(ctx context.Context, scope principalmodel.SystemScope, limit int, now string) ([]integrationmodel.IntegrationOutboxMessage, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil || scope.Kind != principalmodel.SystemScopeRuntimeGlobal {
		if err == nil {
			err = principalmodel.ErrSystemScopeRequired
		}
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if now = strings.TrimSpace(now); now == "" {
		now = time.Now().UTC().Format(time.RFC3339)
	}
	workspaces, err := s.store.WorkerQueueScopePage(ctx, s.db, "integration_outbox", min(256, max(32, limit*2)))
	if err != nil {
		return nil, err
	}
	values := []integrationmodel.IntegrationOutboxMessage{}
	for _, workspaceID := range workspaces {
		workspaceCtx := publicationWorkerContext(ctx, workspaceID, "runtime-publication-worker")
		query, args, err := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "runtime_publication_outbox", workspaceID).Columns(publicationColumns...).Where(publicationDuePredicate(now)).OrderBy(ormbuilder.Ascending("created_at"), ormbuilder.Ascending("id")).Limit(limit).Build()
		if err != nil {
			return nil, fmt.Errorf("build due Runtime publications for workspace %s: %w", workspaceID, err)
		}
		rows, err := s.db.QueryContext(workspaceCtx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("list due Runtime publications for workspace %s: %w", workspaceID, err)
		}
		for rows.Next() {
			value, scanErr := scanPublication(rows)
			if scanErr != nil {
				_ = rows.Close()
				return nil, scanErr
			}
			values = append(values, value)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return capacity.FairOrder(values, limit, func(value integrationmodel.IntegrationOutboxMessage) string { return value.WorkspaceID }), nil
}
func (s WorkerStore) ClaimOutbox(ctx context.Context, workspaceID, messageID, owner, now string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	workspaceID, err := publicationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	messageID, owner, now = strings.TrimSpace(messageID), strings.TrimSpace(owner), strings.TrimSpace(now)
	if messageID == "" || owner == "" {
		return integrationmodel.IntegrationOutboxMessage{}, false, fmt.Errorf("Runtime publication claim identity is required")
	}
	if now == "" {
		now = time.Now().UTC().Format(time.RFC3339)
	}
	ctx = publicationWorkerContext(ctx, workspaceID, owner)
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "runtime_publication_outbox", workspaceID).
		Set("status", "sending").Set("next_attempt_at", "").Set("last_attempt_at", now).Set("lease_owner", owner).Set("lease_expires_at", publicationLeaseExpiry(now)).
		SetExpression("fencing_token", ormbuilder.Add(ormbuilder.Column("fencing_token"), ormbuilder.Value(1))).Set("updated_at", now).
		Where(ormbuilder.And(ormbuilder.Equal("id", messageID), publicationDuePredicate(now))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, fmt.Errorf("claim Runtime publication: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	reader := PublicationStore{store: s.store, db: s.db}
	if count == 0 {
		value, _, readErr := reader.GetOutbox(ctx, workspaceID, messageID)
		return value, false, readErr
	}
	value, found, err := reader.GetOutbox(ctx, workspaceID, messageID)
	if err != nil || !found {
		if err != nil {
			return integrationmodel.IntegrationOutboxMessage{}, false, err
		}
		return integrationmodel.IntegrationOutboxMessage{}, false, fmt.Errorf("Runtime publication not found")
	}
	return value, true, nil
}
func (s WorkerStore) HeartbeatOutbox(ctx context.Context, workspaceID, messageID, leaseOwner string, fencingToken int64, now string) (integrationmodel.IntegrationOutboxMessage, error) {
	workspaceID, err := publicationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	if now = strings.TrimSpace(now); now == "" {
		now = time.Now().UTC().Format(time.RFC3339)
	}
	ctx = publicationWorkerContext(ctx, workspaceID, leaseOwner)
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "runtime_publication_outbox", workspaceID).
		Set("lease_expires_at", publicationLeaseExpiry(now)).Set("updated_at", now).
		Where(publicationPredicate(publicationLeasePredicate(messageID, leaseOwner, fencingToken))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	if count, rowsErr := result.RowsAffected(); rowsErr != nil || count != 1 {
		return integrationmodel.IntegrationOutboxMessage{}, mutation.MutationConflict("runtime_publication", messageID, mutation.MutationConflictLeaseLost, rowsErr)
	}
	value, _, err := (PublicationStore{store: s.store, db: s.db}).GetOutbox(ctx, workspaceID, messageID)
	return value, err
}
func (s WorkerStore) UpdateOutboxStatus(ctx context.Context, workspaceID, messageID, leaseOwner string, fencingToken int64, status, responseRef, errorText, ackDeadlineAt, now string) (integrationmodel.IntegrationOutboxMessage, error) {
	workspaceID, err := publicationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	messageID, now = strings.TrimSpace(messageID), strings.TrimSpace(now)
	if messageID == "" || now == "" {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("Runtime publication update identity is required")
	}
	ctx = publicationWorkerContext(ctx, workspaceID, leaseOwner)
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "runtime_publication_outbox", workspaceID).
		Set("status", strings.TrimSpace(status)).Set("response_ref", strings.TrimSpace(responseRef)).Set("error", strings.TrimSpace(errorText)).Set("next_attempt_at", "").Set("ack_deadline_at", strings.TrimSpace(ackDeadlineAt)).Set("lease_owner", "").Set("lease_expires_at", "").Set("updated_at", now).
		Where(publicationPredicate(publicationLeasePredicate(messageID, leaseOwner, fencingToken))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	if count, rowsErr := result.RowsAffected(); rowsErr != nil || count == 0 {
		return integrationmodel.IntegrationOutboxMessage{}, mutation.MutationConflict("runtime_publication", messageID, mutation.MutationConflictLeaseLost, rowsErr)
	}
	value, found, err := (PublicationStore{store: s.store, db: s.db}).GetOutbox(ctx, workspaceID, messageID)
	if err != nil || !found {
		if err != nil {
			return integrationmodel.IntegrationOutboxMessage{}, err
		}
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("Runtime publication not found")
	}
	return value, nil
}
func (s WorkerStore) ScheduleOutboxRetry(ctx context.Context, workspaceID, messageID, leaseOwner string, fencingToken int64, delaySeconds int, errorText, now string) (integrationmodel.IntegrationOutboxMessage, error) {
	workspaceID, err := publicationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("Runtime publication id is required")
	}
	if delaySeconds < 0 {
		delaySeconds = 60
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(now))
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("Runtime publication retry time is invalid: %w", err)
	}
	now = parsed.UTC().Format(time.RFC3339)
	ctx = publicationWorkerContext(ctx, workspaceID, leaseOwner)
	next := parsed.Add(time.Duration(delaySeconds) * time.Second).Format(time.RFC3339)
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "runtime_publication_outbox", workspaceID).
		Set("status", "queued").Set("error", strings.TrimSpace(errorText)).SetExpression("attempt_count", ormbuilder.Add(ormbuilder.Column("attempt_count"), ormbuilder.Value(1))).Set("next_attempt_at", next).Set("last_attempt_at", now).Set("lease_owner", "").Set("lease_expires_at", "").Set("updated_at", now).
		Where(publicationPredicate(publicationLeasePredicate(messageID, leaseOwner, fencingToken))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	if count, rowsErr := result.RowsAffected(); rowsErr != nil || count == 0 {
		return integrationmodel.IntegrationOutboxMessage{}, mutation.MutationConflict("runtime_publication", messageID, mutation.MutationConflictLeaseLost, rowsErr)
	}
	value, found, err := (PublicationStore{store: s.store, db: s.db}).GetOutbox(ctx, workspaceID, messageID)
	if err != nil || !found {
		if err != nil {
			return integrationmodel.IntegrationOutboxMessage{}, err
		}
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("Runtime publication not found")
	}
	return value, nil
}

var _ integrationrepository.RuntimePublicationWorkerRepository = WorkerStore{}

func publicationWorkerContext(ctx context.Context, workspaceID, actorID string) context.Context {
	ctx = requestcontext.WithWorkspaceID(ctx, strings.TrimSpace(workspaceID))
	if actorID = strings.TrimSpace(actorID); actorID != "" {
		ctx = requestcontext.WithActorID(ctx, actorID)
	}
	return ctx
}

func publicationDuePredicate(now string) ormbuilder.Predicate {
	return publicationPredicate(ormbuilder.Or(
		ormbuilder.And(ormbuilder.Equal("status", "queued"), ormbuilder.Or(ormbuilder.Equal("next_attempt_at", ""), ormbuilder.LessThanOrEqual("next_attempt_at", now))),
		ormbuilder.And(ormbuilder.Equal("status", "sending"), ormbuilder.LessThanOrEqual("lease_expires_at", now)),
	))
}

func publicationLeasePredicate(messageID, owner string, token int64) ormbuilder.Predicate {
	return ormbuilder.And(ormbuilder.Equal("id", strings.TrimSpace(messageID)), ormbuilder.Equal("status", "sending"), ormbuilder.Equal("lease_owner", strings.TrimSpace(owner)), ormbuilder.Equal("fencing_token", token))
}

func publicationLeaseExpiry(now string) string {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(now))
	if err != nil {
		parsed = time.Now().UTC()
	}
	return parsed.Add(5 * time.Minute).Format(time.RFC3339)
}
