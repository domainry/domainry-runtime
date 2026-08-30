// Integration worker persistence.
package integration

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/mutation"
	ormbuilder "github.com/domainry/domainry-orm/builder"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/capacity"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type IntegrationWorkerStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewIntegrationWorkerStore(s *database.RuntimeStore) IntegrationWorkerStore {
	return IntegrationWorkerStore{store: s, db: s.DB()}
}

func (r IntegrationWorkerStore) ListDueEvents(ctx context.Context, scope principalmodel.SystemScope, limit int, now string) ([]integrationmodel.IntegrationEvent, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if now = strings.TrimSpace(now); now == "" {
		now = time.Now().UTC().Format(time.RFC3339)
	}
	out := []integrationmodel.IntegrationEvent{}
	workspaces, err := r.store.WorkerQueueScopePage(ctx, r.db, integrationEventWorkerQueueKind, integrationWorkspaceScanLimit(limit))
	if err != nil {
		return nil, err
	}
	for _, workspaceID := range workspaces {
		workspaceCtx := integrationWorkerWorkspaceContext(ctx, workspaceID, "integration-event-worker")
		predicate := integrationDueEventPredicate(now)
		priority := ormbuilder.CaseWhen(ormbuilder.Equal("status", "failed"), 0).Else(1)
		query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "integration_events", workspaceID).Columns(integrationEventColumns...).Where(predicate).
			OrderBy(ormbuilder.AscendingExpression(priority), ormbuilder.Ascending("next_retry_at"), ormbuilder.Ascending("received_at"), ormbuilder.Ascending("id")).Limit(limit).Build()
		if buildErr != nil {
			return nil, fmt.Errorf("build due integration events for workspace %s: %w", workspaceID, buildErr)
		}
		rows, queryErr := r.db.QueryContext(workspaceCtx, query, args...)
		if queryErr != nil {
			return nil, fmt.Errorf("list due integration events for workspace %s: %w", workspaceID, queryErr)
		}
		for rows.Next() {
			event, scanErr := scanIntegrationEvent(rows)
			if scanErr != nil {
				_ = rows.Close()
				return nil, scanErr
			}
			out = append(out, event)
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate due integration events for workspace %s: %w", workspaceID, rowsErr)
		}
		_ = rows.Close()
	}
	sort.SliceStable(out, func(i, j int) bool {
		leftRank, rightRank := 1, 1
		if out[i].Status == "failed" {
			leftRank = 0
		}
		if out[j].Status == "failed" {
			rightRank = 0
		}
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if out[i].NextRetryAt != out[j].NextRetryAt {
			return out[i].NextRetryAt < out[j].NextRetryAt
		}
		if out[i].ReceivedAt != out[j].ReceivedAt {
			return out[i].ReceivedAt < out[j].ReceivedAt
		}
		return out[i].ID < out[j].ID
	})
	return capacity.FairOrder(out, limit, func(event integrationmodel.IntegrationEvent) string { return event.WorkspaceID }), nil
}

func (r IntegrationWorkerStore) ClaimEvent(ctx context.Context, workspaceID, eventID, owner, now string) (integrationmodel.IntegrationEvent, bool, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, false, err
	}
	eventID = strings.TrimSpace(eventID)
	now = strings.TrimSpace(now)
	if now == "" {
		now = time.Now().UTC().Format(time.RFC3339)
	}
	if eventID == "" {
		return integrationmodel.IntegrationEvent{}, false, fmt.Errorf("integration event id is required")
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return integrationmodel.IntegrationEvent{}, false, fmt.Errorf("integration event worker owner is required")
	}
	ctx = integrationWorkerWorkspaceContext(ctx, workspaceID, owner)
	expires := integrationWorkerLeaseExpiry(now)
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "integration_events", workspaceID).
		Set("status", "processing").Set("error", "").Set("next_retry_at", "").Set("lease_owner", owner).Set("lease_expires_at", expires).
		SetExpression("fencing_token", ormbuilder.Add(ormbuilder.Column("fencing_token"), ormbuilder.Value(1))).Set("updated_at", now).
		Where(ormbuilder.And(ormbuilder.Equal("id", eventID), integrationDueEventPredicate(now))).Build()
	if err != nil {
		return integrationmodel.IntegrationEvent{}, false, fmt.Errorf("build integration event claim: %w", err)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, false, fmt.Errorf("claim integration event: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return integrationmodel.IntegrationEvent{}, false, fmt.Errorf("read claimed integration event count: %w", err)
	}
	if n == 0 {
		event, _, err := r.findEvent(ctx, workspaceID, eventID)
		return event, false, err
	}
	event, ok, err := r.findEvent(ctx, workspaceID, eventID)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("integration event not found")
		}
		return integrationmodel.IntegrationEvent{}, false, err
	}
	return event, true, nil
}

func (r IntegrationWorkerStore) UpdateEventStatus(ctx context.Context, workspaceID, eventID, expectedLeaseOwner string, expectedFencingToken int64, status, errorText, now string) (integrationmodel.IntegrationEvent, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("integration event id is required")
	}
	now = strings.TrimSpace(now)
	if now == "" {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("integration event update time is required")
	}
	ctx = integrationWorkerWorkspaceContext(ctx, workspaceID, expectedLeaseOwner)
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "integration_events", workspaceID).
		Set("status", status).Set("error", errorText).Set("next_retry_at", "").Set("lease_owner", "").Set("lease_expires_at", "").Set("updated_at", now).
		Where(integrationLeasePredicate(eventID, "processing", expectedLeaseOwner, expectedFencingToken)).Build()
	if err != nil {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("build integration event status update: %w", err)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("update integration event status: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("read updated integration event count: %w", err)
	}
	if n == 0 {
		return integrationmodel.IntegrationEvent{}, mutation.MutationConflict("integration_event", eventID, mutation.MutationConflictLeaseLost, nil)
	}
	event, ok, err := r.findEvent(ctx, workspaceID, eventID)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	if !ok {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("integration event not found")
	}
	return event, nil
}

func (r IntegrationWorkerStore) HeartbeatEvent(ctx context.Context, workspaceID, eventID, expectedLeaseOwner string, expectedFencingToken int64, now string) (integrationmodel.IntegrationEvent, error) {
	return r.heartbeatEvent(ctx, workspaceID, eventID, expectedLeaseOwner, expectedFencingToken, now)
}

func (r IntegrationWorkerStore) heartbeatEvent(ctx context.Context, workspaceID, eventID, expectedLeaseOwner string, expectedFencingToken int64, now string) (integrationmodel.IntegrationEvent, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	if strings.TrimSpace(now) == "" {
		now = time.Now().UTC().Format(time.RFC3339)
	}
	ctx = integrationWorkerWorkspaceContext(ctx, workspaceID, expectedLeaseOwner)
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "integration_events", workspaceID).
		Set("lease_expires_at", integrationWorkerLeaseExpiry(now)).Set("updated_at", now).
		Where(integrationLeasePredicate(strings.TrimSpace(eventID), "processing", expectedLeaseOwner, expectedFencingToken)).Build()
	if err != nil {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("build integration event heartbeat: %w", err)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("read heartbeat integration event count: %w", err)
	}
	if n != 1 {
		return integrationmodel.IntegrationEvent{}, mutation.MutationConflict("integration_event", eventID, mutation.MutationConflictLeaseLost, nil)
	}
	event, _, err := r.findEvent(ctx, workspaceID, eventID)
	return event, err
}

func (r IntegrationWorkerStore) ScheduleEventRetry(ctx context.Context, workspaceID, eventID, expectedLeaseOwner string, expectedFencingToken int64, delaySeconds int, errorText, nowText string) (integrationmodel.IntegrationEvent, error) {
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
	now, err := time.Parse(time.RFC3339, strings.TrimSpace(nowText))
	if err != nil {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("integration event retry time is invalid: %w", err)
	}
	nowText = now.UTC().Format(time.RFC3339)
	ctx = integrationWorkerWorkspaceContext(ctx, workspaceID, expectedLeaseOwner)
	next := now.Add(time.Duration(delaySeconds) * time.Second).Format(time.RFC3339)
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "integration_events", workspaceID).
		Set("status", "failed").Set("error", errorText).SetExpression("attempt_count", ormbuilder.Add(ormbuilder.Column("attempt_count"), ormbuilder.Value(1))).
		Set("next_retry_at", next).Set("last_attempt_at", nowText).Set("lease_owner", "").Set("lease_expires_at", "").Set("updated_at", nowText).
		Where(integrationLeasePredicate(eventID, "processing", expectedLeaseOwner, expectedFencingToken)).Build()
	if err != nil {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("build integration event retry: %w", err)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("schedule integration event retry: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("read scheduled integration event count: %w", err)
	}
	if n == 0 {
		return integrationmodel.IntegrationEvent{}, mutation.MutationConflict("integration_event", eventID, mutation.MutationConflictLeaseLost, nil)
	}
	event, ok, err := r.findEvent(ctx, workspaceID, eventID)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	if !ok {
		return integrationmodel.IntegrationEvent{}, fmt.Errorf("integration event not found")
	}
	return event, nil
}

func (r IntegrationWorkerStore) ListDueOutbox(ctx context.Context, scope principalmodel.SystemScope, limit int, now string) ([]integrationmodel.IntegrationOutboxMessage, error) {
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
	out := []integrationmodel.IntegrationOutboxMessage{}
	workspaces, err := r.store.WorkerQueueScopePage(ctx, r.db, integrationOutboxWorkerQueueKind, integrationWorkspaceScanLimit(limit))
	if err != nil {
		return nil, err
	}
	for _, workspaceID := range workspaces {
		workspaceCtx := integrationWorkerWorkspaceContext(ctx, workspaceID, "integration-outbox-worker")
		query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "runtime_publication_outbox", workspaceID).Columns(integrationOutboxColumns...).Where(integrationDueOutboxPredicate(now)).OrderBy(ormbuilder.Ascending("created_at"), ormbuilder.Ascending("id")).Limit(limit).Build()
		if buildErr != nil {
			return nil, fmt.Errorf("build due integration outbox for workspace %s: %w", workspaceID, buildErr)
		}
		rows, queryErr := r.db.QueryContext(workspaceCtx, query, args...)
		if queryErr != nil {
			return nil, fmt.Errorf("list due integration outbox for workspace %s: %w", workspaceID, queryErr)
		}
		for rows.Next() {
			message, scanErr := scanIntegrationOutboxMessage(rows)
			if scanErr != nil {
				_ = rows.Close()
				return nil, scanErr
			}
			out = append(out, message)
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate due integration outbox for workspace %s: %w", workspaceID, rowsErr)
		}
		_ = rows.Close()
	}
	return capacity.FairOrder(out, limit, func(message integrationmodel.IntegrationOutboxMessage) string { return message.WorkspaceID }), nil
}

func integrationWorkspaceScanLimit(itemLimit int) int {
	return min(256, max(32, itemLimit*2))
}

func (r IntegrationWorkerStore) ClaimOutbox(ctx context.Context, workspaceID, messageID, owner, now string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	messageID = strings.TrimSpace(messageID)
	now = strings.TrimSpace(now)
	if now == "" {
		now = time.Now().UTC().Format(time.RFC3339)
	}
	if messageID == "" {
		return integrationmodel.IntegrationOutboxMessage{}, false, fmt.Errorf("integration outbox id is required")
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return integrationmodel.IntegrationOutboxMessage{}, false, fmt.Errorf("integration outbox worker owner is required")
	}
	ctx = integrationWorkerWorkspaceContext(ctx, workspaceID, owner)
	expires := integrationWorkerLeaseExpiry(now)
	// Preserve the previous attempt's error while the retry is claimed. The
	// adapter-resolution safety policy uses that evidence to distinguish a
	// provider's explicit no-effect rejection (for example HTTP 429) from an
	// uncertain write outcome. Completion or the next failure replaces it.
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "runtime_publication_outbox", workspaceID).
		Set("status", "sending").Set("next_attempt_at", "").Set("last_attempt_at", now).Set("lease_owner", owner).Set("lease_expires_at", expires).
		SetExpression("fencing_token", ormbuilder.Add(ormbuilder.Column("fencing_token"), ormbuilder.Value(1))).Set("updated_at", now).
		Where(ormbuilder.And(ormbuilder.Equal("id", messageID), integrationDueOutboxPredicate(now))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, fmt.Errorf("build integration outbox claim: %w", err)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, fmt.Errorf("claim integration outbox message: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, fmt.Errorf("read claimed integration outbox count: %w", err)
	}
	if n == 0 {
		message, _, readErr := r.findOutbox(ctx, workspaceID, messageID)
		return message, false, readErr
	}
	message, ok, err := r.findOutbox(ctx, workspaceID, messageID)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("integration outbox message not found")
		}
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	return message, true, nil
}

func (r IntegrationWorkerStore) UpdateOutboxStatus(ctx context.Context, workspaceID, messageID, expectedLeaseOwner string, expectedFencingToken int64, status, responseRef, errorText, ackDeadlineAt, now string) (integrationmodel.IntegrationOutboxMessage, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("integration outbox id is required")
	}
	now = strings.TrimSpace(now)
	if now == "" {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("integration outbox update time is required")
	}
	ctx = integrationWorkerWorkspaceContext(ctx, workspaceID, expectedLeaseOwner)
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "runtime_publication_outbox", workspaceID).
		Set("status", status).Set("response_ref", responseRef).Set("error", errorText).Set("next_attempt_at", "").Set("ack_deadline_at", strings.TrimSpace(ackDeadlineAt)).Set("lease_owner", "").Set("lease_expires_at", "").Set("updated_at", now).
		Where(integrationPublicationPredicate(integrationLeasePredicate(messageID, "sending", expectedLeaseOwner, expectedFencingToken))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("build integration outbox status update: %w", err)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("update integration outbox status: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("read updated integration outbox count: %w", err)
	}
	if n == 0 {
		return integrationmodel.IntegrationOutboxMessage{}, mutation.MutationConflict("integration_outbox", messageID, mutation.MutationConflictLeaseLost, nil)
	}
	message, ok, err := r.findOutbox(ctx, workspaceID, messageID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	if !ok {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("integration outbox message not found")
	}
	return message, nil
}

func (r IntegrationWorkerStore) HeartbeatOutbox(ctx context.Context, workspaceID, messageID, expectedLeaseOwner string, expectedFencingToken int64, now string) (integrationmodel.IntegrationOutboxMessage, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	if strings.TrimSpace(now) == "" {
		now = time.Now().UTC().Format(time.RFC3339)
	}
	ctx = integrationWorkerWorkspaceContext(ctx, workspaceID, expectedLeaseOwner)
	expires := integrationWorkerLeaseExpiry(now)
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "runtime_publication_outbox", workspaceID).
		Set("lease_expires_at", expires).Set("updated_at", now).
		Where(integrationPublicationPredicate(integrationLeasePredicate(strings.TrimSpace(messageID), "sending", expectedLeaseOwner, expectedFencingToken))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("build integration outbox heartbeat: %w", err)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("read heartbeat integration outbox count: %w", err)
	}
	if n != 1 {
		return integrationmodel.IntegrationOutboxMessage{}, mutation.MutationConflict("integration_outbox", messageID, mutation.MutationConflictLeaseLost, nil)
	}
	message, _, err := r.findOutbox(ctx, workspaceID, messageID)
	return message, err
}

func (r IntegrationWorkerStore) ScheduleOutboxRetry(ctx context.Context, workspaceID, messageID, expectedLeaseOwner string, expectedFencingToken int64, delaySeconds int, errorText, nowText string) (integrationmodel.IntegrationOutboxMessage, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("integration outbox id is required")
	}
	if delaySeconds < 0 {
		delaySeconds = 60
	}
	now, err := time.Parse(time.RFC3339, strings.TrimSpace(nowText))
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("integration outbox retry time is invalid: %w", err)
	}
	nowText = now.UTC().Format(time.RFC3339)
	ctx = integrationWorkerWorkspaceContext(ctx, workspaceID, expectedLeaseOwner)
	next := now.Add(time.Duration(delaySeconds) * time.Second).Format(time.RFC3339)
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "runtime_publication_outbox", workspaceID).
		Set("status", "queued").Set("error", errorText).SetExpression("attempt_count", ormbuilder.Add(ormbuilder.Column("attempt_count"), ormbuilder.Value(1))).
		Set("next_attempt_at", next).Set("last_attempt_at", nowText).Set("lease_owner", "").Set("lease_expires_at", "").Set("updated_at", nowText).
		Where(integrationPublicationPredicate(integrationLeasePredicate(messageID, "sending", expectedLeaseOwner, expectedFencingToken))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("build integration outbox retry: %w", err)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("schedule integration outbox retry: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("read scheduled integration outbox count: %w", err)
	}
	if n == 0 {
		return integrationmodel.IntegrationOutboxMessage{}, mutation.MutationConflict("integration_outbox", messageID, mutation.MutationConflictLeaseLost, nil)
	}
	message, ok, err := r.findOutbox(ctx, workspaceID, messageID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	if !ok {
		return integrationmodel.IntegrationOutboxMessage{}, fmt.Errorf("integration outbox message not found")
	}
	return message, nil
}

func (r IntegrationWorkerStore) findEvent(ctx context.Context, workspaceID, eventID string) (integrationmodel.IntegrationEvent, bool, error) {
	ctx = integrationWorkerWorkspaceContext(ctx, workspaceID, "")
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "integration_events", workspaceID).Columns(integrationEventColumns...).Where(ormbuilder.Equal("id", strings.TrimSpace(eventID))).Build()
	if err != nil {
		return integrationmodel.IntegrationEvent{}, false, err
	}
	row := r.db.QueryRowContext(ctx, query, args...)
	event, err := scanIntegrationEvent(row)
	if err == sql.ErrNoRows {
		return integrationmodel.IntegrationEvent{}, false, nil
	}
	if err != nil {
		return integrationmodel.IntegrationEvent{}, false, err
	}
	return event, true, nil
}
func (r IntegrationWorkerStore) findOutbox(ctx context.Context, workspaceID, messageID string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	ctx = integrationWorkerWorkspaceContext(ctx, workspaceID, "")
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "runtime_publication_outbox", workspaceID).Columns(integrationOutboxColumns...).Where(integrationPublicationPredicate(ormbuilder.Equal("id", strings.TrimSpace(messageID)))).Build()
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	row := r.db.QueryRowContext(ctx, query, args...)
	message, err := scanIntegrationOutboxMessage(row)
	if err == sql.ErrNoRows {
		return integrationmodel.IntegrationOutboxMessage{}, false, nil
	}
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	return message, true, nil
}

func integrationDueEventPredicate(now string) ormbuilder.Predicate {
	return ormbuilder.Or(
		ormbuilder.Equal("status", "received"),
		ormbuilder.And(ormbuilder.Equal("status", "failed"), ormbuilder.NotEqual("next_retry_at", ""), ormbuilder.LessThanOrEqual("next_retry_at", now)),
		ormbuilder.And(ormbuilder.Equal("status", "processing"), ormbuilder.LessThanOrEqual("lease_expires_at", now)),
	)
}

func integrationDueOutboxPredicate(now string) ormbuilder.Predicate {
	return integrationPublicationPredicate(ormbuilder.Or(
		ormbuilder.And(ormbuilder.Equal("status", "queued"), ormbuilder.Or(ormbuilder.Equal("next_attempt_at", ""), ormbuilder.LessThanOrEqual("next_attempt_at", now))),
		ormbuilder.And(ormbuilder.Equal("status", "sending"), ormbuilder.LessThanOrEqual("lease_expires_at", now)),
	))
}

func integrationPublicationPredicate(predicate ormbuilder.Predicate) ormbuilder.Predicate {
	return ormbuilder.And(ormbuilder.Equal("publication_type", "integration.connector"), predicate)
}

func integrationLeasePredicate(id, status, owner string, token int64) ormbuilder.Predicate {
	return ormbuilder.And(ormbuilder.Equal("id", strings.TrimSpace(id)), ormbuilder.Equal("status", status), ormbuilder.Equal("lease_owner", strings.TrimSpace(owner)), ormbuilder.Equal("fencing_token", token))
}

const integrationWorkerLeaseTTL = 5 * time.Minute

func integrationWorkerLeaseCutoff(now string) string {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(now))
	if err != nil {
		parsed = time.Now().UTC()
	}
	return parsed.Add(-integrationWorkerLeaseTTL).Format(time.RFC3339)
}

func integrationWorkerLeaseExpiry(now string) string {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(now))
	if err != nil {
		parsed = time.Now().UTC()
	}
	return parsed.Add(integrationWorkerLeaseTTL).Format(time.RFC3339)
}
