package publicationhandoff

import (
	"context"
	"database/sql"
	"fmt"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	publicationrepository "github.com/domainry/domainry-runtime/runtime/domain/publication/repository"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-foundation/requestcontext"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	"github.com/domainry/domainry-orm/query"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type WorkerStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewWorkerStore(store *database.RuntimeStore) WorkerStore {
	return WorkerStore{store: store, db: store.DB()}
}

func (s WorkerStore) ListDueOutbox(ctx context.Context, scope principalmodel.SystemScope, limit int, now string) ([]publicationmodel.Message, error) {
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
	workspaces, err := s.store.WorkerQueueScopePage(ctx, s.db, "runtime_publication_outbox", min(256, max(32, limit*2)))
	if err != nil {
		return nil, err
	}
	values := []publicationmodel.Message{}
	for _, workspaceID := range workspaces {
		workspaceCtx := publicationWorkerContext(ctx, workspaceID, "runtime-publication-worker")
		queryValue, args, err := query.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "_publication_outbox", workspaceID).Columns(publicationColumns...).Where(publicationDuePredicate(now)).OrderBy(query.Ascending("created_at"), query.Ascending("id")).Limit(limit).Build()
		if err != nil {
			return nil, fmt.Errorf("build due Runtime publications for workspace %s: %w", workspaceID, err)
		}
		rows, err := s.db.QueryContext(workspaceCtx, queryValue, args...)
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
	return workerplatform.FairOrder(values, limit, func(value publicationmodel.Message) string { return value.WorkspaceID }), nil
}
func (s WorkerStore) ClaimOutbox(ctx context.Context, workspaceID, messageID, owner, now string) (publicationmodel.Message, bool, error) {
	workspaceID, err := publicationWorkspaceID(workspaceID)
	if err != nil {
		return publicationmodel.Message{}, false, err
	}
	messageID, owner, now = strings.TrimSpace(messageID), strings.TrimSpace(owner), strings.TrimSpace(now)
	if messageID == "" || owner == "" {
		return publicationmodel.Message{}, false, fmt.Errorf("Runtime publication claim identity is required")
	}
	if now == "" {
		now = time.Now().UTC().Format(time.RFC3339)
	}
	ctx = publicationWorkerContext(ctx, workspaceID, owner)
	queryValue, args, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "_publication_outbox", workspaceID).
		Set("status", "sending").Set("next_attempt_at", "").Set("last_attempt_at", now).Set("lease_owner", owner).Set("lease_expires_at", publicationLeaseExpiry(now)).
		SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).Set("updated_at", now).
		Where(query.And(query.Equal("id", messageID), publicationDuePredicate(now))).Build()
	if err != nil {
		return publicationmodel.Message{}, false, err
	}
	result, err := s.db.ExecContext(ctx, queryValue, args...)
	if err != nil {
		return publicationmodel.Message{}, false, fmt.Errorf("claim Runtime publication: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return publicationmodel.Message{}, false, err
	}
	reader := PublicationStore{store: s.store, db: s.db}
	if count == 0 {
		value, _, readErr := reader.GetOutbox(ctx, workspaceID, messageID)
		return value, false, readErr
	}
	value, found, err := reader.GetOutbox(ctx, workspaceID, messageID)
	if err != nil || !found {
		if err != nil {
			return publicationmodel.Message{}, false, err
		}
		return publicationmodel.Message{}, false, fmt.Errorf("Runtime publication not found")
	}
	return value, true, nil
}
func (s WorkerStore) HeartbeatOutbox(ctx context.Context, workspaceID, messageID, leaseOwner string, fencingToken int64, now string) (publicationmodel.Message, error) {
	workspaceID, err := publicationWorkspaceID(workspaceID)
	if err != nil {
		return publicationmodel.Message{}, err
	}
	if now = strings.TrimSpace(now); now == "" {
		now = time.Now().UTC().Format(time.RFC3339)
	}
	ctx = publicationWorkerContext(ctx, workspaceID, leaseOwner)
	queryValue, args, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "_publication_outbox", workspaceID).
		Set("lease_expires_at", publicationLeaseExpiry(now)).Set("updated_at", now).
		Where(publicationPredicate(publicationLeasePredicate(messageID, leaseOwner, fencingToken))).Build()
	if err != nil {
		return publicationmodel.Message{}, err
	}
	result, err := s.db.ExecContext(ctx, queryValue, args...)
	if err != nil {
		return publicationmodel.Message{}, err
	}
	if count, rowsErr := result.RowsAffected(); rowsErr != nil || count != 1 {
		return publicationmodel.Message{}, mutation.MutationConflict("runtime_publication", messageID, mutation.MutationConflictLeaseLost, rowsErr)
	}
	value, _, err := (PublicationStore{store: s.store, db: s.db}).GetOutbox(ctx, workspaceID, messageID)
	return value, err
}
func (s WorkerStore) UpdateOutboxStatus(ctx context.Context, workspaceID, messageID, leaseOwner string, fencingToken int64, status, responseRef, errorText, now string) (publicationmodel.Message, error) {
	workspaceID, err := publicationWorkspaceID(workspaceID)
	if err != nil {
		return publicationmodel.Message{}, err
	}
	messageID, now = strings.TrimSpace(messageID), strings.TrimSpace(now)
	if messageID == "" || now == "" {
		return publicationmodel.Message{}, fmt.Errorf("Runtime publication update identity is required")
	}
	ctx = publicationWorkerContext(ctx, workspaceID, leaseOwner)
	queryValue, args, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "_publication_outbox", workspaceID).
		Set("status", strings.TrimSpace(status)).Set("response_ref", strings.TrimSpace(responseRef)).Set("error", strings.TrimSpace(errorText)).Set("next_attempt_at", "").Set("lease_owner", "").Set("lease_expires_at", "").Set("updated_at", now).
		Where(publicationPredicate(publicationLeasePredicate(messageID, leaseOwner, fencingToken))).Build()
	if err != nil {
		return publicationmodel.Message{}, err
	}
	result, err := s.db.ExecContext(ctx, queryValue, args...)
	if err != nil {
		return publicationmodel.Message{}, err
	}
	if count, rowsErr := result.RowsAffected(); rowsErr != nil || count == 0 {
		return publicationmodel.Message{}, mutation.MutationConflict("runtime_publication", messageID, mutation.MutationConflictLeaseLost, rowsErr)
	}
	value, found, err := (PublicationStore{store: s.store, db: s.db}).GetOutbox(ctx, workspaceID, messageID)
	if err != nil || !found {
		if err != nil {
			return publicationmodel.Message{}, err
		}
		return publicationmodel.Message{}, fmt.Errorf("Runtime publication not found")
	}
	return value, nil
}
func (s WorkerStore) ScheduleOutboxRetry(ctx context.Context, workspaceID, messageID, leaseOwner string, fencingToken int64, delaySeconds int, errorText, now string) (publicationmodel.Message, error) {
	workspaceID, err := publicationWorkspaceID(workspaceID)
	if err != nil {
		return publicationmodel.Message{}, err
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return publicationmodel.Message{}, fmt.Errorf("Runtime publication id is required")
	}
	if delaySeconds < 0 {
		delaySeconds = 60
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(now))
	if err != nil {
		return publicationmodel.Message{}, fmt.Errorf("Runtime publication retry time is invalid: %w", err)
	}
	now = parsed.UTC().Format(time.RFC3339)
	ctx = publicationWorkerContext(ctx, workspaceID, leaseOwner)
	next := parsed.Add(time.Duration(delaySeconds) * time.Second).Format(time.RFC3339)
	queryValue, args, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "_publication_outbox", workspaceID).
		Set("status", "queued").Set("error", strings.TrimSpace(errorText)).SetExpression("attempt_count", query.Add(query.Column("attempt_count"), query.Value(1))).Set("next_attempt_at", next).Set("last_attempt_at", now).Set("lease_owner", "").Set("lease_expires_at", "").Set("updated_at", now).
		Where(publicationPredicate(publicationLeasePredicate(messageID, leaseOwner, fencingToken))).Build()
	if err != nil {
		return publicationmodel.Message{}, err
	}
	result, err := s.db.ExecContext(ctx, queryValue, args...)
	if err != nil {
		return publicationmodel.Message{}, err
	}
	if count, rowsErr := result.RowsAffected(); rowsErr != nil || count == 0 {
		return publicationmodel.Message{}, mutation.MutationConflict("runtime_publication", messageID, mutation.MutationConflictLeaseLost, rowsErr)
	}
	value, found, err := (PublicationStore{store: s.store, db: s.db}).GetOutbox(ctx, workspaceID, messageID)
	if err != nil || !found {
		if err != nil {
			return publicationmodel.Message{}, err
		}
		return publicationmodel.Message{}, fmt.Errorf("Runtime publication not found")
	}
	return value, nil
}

var _ publicationrepository.WorkerRepository = WorkerStore{}

func publicationWorkerContext(ctx context.Context, workspaceID, actorID string) context.Context {
	ctx = requestcontext.WithWorkspaceID(ctx, strings.TrimSpace(workspaceID))
	if actorID = strings.TrimSpace(actorID); actorID != "" {
		ctx = requestcontext.WithActorID(ctx, actorID)
	}
	return ctx
}

func publicationDuePredicate(now string) query.Predicate {
	return publicationPredicate(query.Or(
		query.And(query.Equal("status", "queued"), query.Or(query.Equal("next_attempt_at", ""), query.LessThanOrEqual("next_attempt_at", now))),
		query.And(query.Equal("status", "sending"), query.LessThanOrEqual("lease_expires_at", now)),
	))
}

func publicationLeasePredicate(messageID, owner string, token int64) query.Predicate {
	return query.And(query.Equal("id", strings.TrimSpace(messageID)), query.Equal("status", "sending"), query.Equal("lease_owner", strings.TrimSpace(owner)), query.Equal("fencing_token", token))
}

func publicationLeaseExpiry(now string) string {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(now))
	if err != nil {
		parsed = time.Now().UTC()
	}
	return parsed.Add(5 * time.Minute).Format(time.RFC3339)
}
