// Integration worker persistence.
package integration

import (
	"context"
	"database/sql"
	"fmt"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"sort"

	"github.com/domainry/domainry-runtime/runtime/platform/capacity"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"

	"strings"
	"time"

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
	s := r.store
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
		rows, queryErr := r.db.QueryContext(workspaceCtx, "SELECT "+integrationEventColumnsSQL(s)+" FROM "+s.TableIdentifier("integration_events")+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(1)+" AND ("+s.Identifier("status")+" = "+s.Placeholder(2)+" OR ("+s.Identifier("status")+" = "+s.Placeholder(3)+" AND "+s.Identifier("next_retry_at")+" <> "+s.Placeholder(4)+" AND "+s.Identifier("next_retry_at")+" <= "+s.Placeholder(5)+") OR ("+s.Identifier("status")+" = "+s.Placeholder(6)+" AND "+s.Identifier("lease_expires_at")+" <= "+s.Placeholder(7)+")) ORDER BY CASE WHEN "+s.Identifier("status")+" = "+s.Placeholder(8)+" THEN 0 ELSE 1 END ASC, "+s.Identifier("next_retry_at")+" ASC, "+s.Identifier("received_at")+" ASC LIMIT "+s.Placeholder(9), workspaceID, "received", "failed", "", now, "processing", now, "failed", limit)
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
	s := r.store
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
	result, err := r.db.ExecContext(ctx, "UPDATE "+s.TableIdentifier("integration_events")+" SET "+s.Identifier("status")+" = "+s.Placeholder(1)+", "+s.Identifier("error")+" = "+s.Placeholder(2)+", "+s.Identifier("next_retry_at")+" = "+s.Placeholder(3)+", "+s.Identifier("lease_owner")+" = "+s.Placeholder(4)+", "+s.Identifier("lease_expires_at")+" = "+s.Placeholder(5)+", "+s.Identifier("fencing_token")+" = "+s.Identifier("fencing_token")+" + 1, "+s.Identifier("updated_at")+" = "+s.Placeholder(6)+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(7)+" AND "+s.Identifier("id")+" = "+s.Placeholder(8)+" AND (("+s.Identifier("status")+" = 'failed' AND "+s.Identifier("next_retry_at")+" <> '' AND "+s.Identifier("next_retry_at")+" <= "+s.Placeholder(9)+") OR "+s.Identifier("status")+" = 'received' OR ("+s.Identifier("status")+" = 'processing' AND "+s.Identifier("lease_expires_at")+" <= "+s.Placeholder(10)+"))", "processing", "", "", owner, expires, now, workspaceID, eventID, now, now)
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
	s := r.store
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
	result, err := r.db.ExecContext(ctx, "UPDATE "+s.TableIdentifier("integration_events")+" SET "+s.Identifier("status")+" = "+s.Placeholder(1)+", "+s.Identifier("error")+" = "+s.Placeholder(2)+", "+s.Identifier("next_retry_at")+" = "+s.Placeholder(3)+", "+s.Identifier("lease_owner")+" = '', "+s.Identifier("lease_expires_at")+" = '', "+s.Identifier("updated_at")+" = "+s.Placeholder(4)+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(5)+" AND "+s.Identifier("id")+" = "+s.Placeholder(6)+" AND "+s.Identifier("status")+" = 'processing' AND "+s.Identifier("lease_owner")+" = "+s.Placeholder(7)+" AND "+s.Identifier("fencing_token")+" = "+s.Placeholder(8), status, errorText, "", now, workspaceID, eventID, strings.TrimSpace(expectedLeaseOwner), expectedFencingToken)
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
	result, err := r.db.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("integration_events")+" SET "+r.store.Identifier("lease_expires_at")+" = "+r.store.Placeholder(1)+", "+r.store.Identifier("updated_at")+" = "+r.store.Placeholder(2)+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(3)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(4)+" AND "+r.store.Identifier("status")+" = 'processing' AND "+r.store.Identifier("lease_owner")+" <> '' AND "+r.store.Identifier("lease_owner")+" = "+r.store.Placeholder(5)+" AND "+r.store.Identifier("fencing_token")+" = "+r.store.Placeholder(6), integrationWorkerLeaseExpiry(now), now, workspaceID, strings.TrimSpace(eventID), strings.TrimSpace(expectedLeaseOwner), expectedFencingToken)
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
	s := r.store
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
	result, err := r.db.ExecContext(ctx, "UPDATE "+s.TableIdentifier("integration_events")+" SET "+s.Identifier("status")+" = "+s.Placeholder(1)+", "+s.Identifier("error")+" = "+s.Placeholder(2)+", "+s.Identifier("attempt_count")+" = "+s.Identifier("attempt_count")+" + 1, "+s.Identifier("next_retry_at")+" = "+s.Placeholder(3)+", "+s.Identifier("last_attempt_at")+" = "+s.Placeholder(4)+", "+s.Identifier("lease_owner")+" = '', "+s.Identifier("lease_expires_at")+" = '', "+s.Identifier("updated_at")+" = "+s.Placeholder(5)+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(6)+" AND "+s.Identifier("id")+" = "+s.Placeholder(7)+" AND "+s.Identifier("status")+" = 'processing' AND "+s.Identifier("lease_owner")+" = "+s.Placeholder(8)+" AND "+s.Identifier("fencing_token")+" = "+s.Placeholder(9), "failed", errorText, next, nowText, nowText, workspaceID, eventID, strings.TrimSpace(expectedLeaseOwner), expectedFencingToken)
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
	s := r.store
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
		query := "SELECT " + integrationOutboxColumnsSQL(s) + " FROM " + s.TableIdentifier("integration_outbox_messages") + " WHERE " + s.Identifier("workspace_id") + " = " + s.Placeholder(1) + " AND ((" + s.Identifier("status") + " = 'queued' AND (" + s.Identifier("next_attempt_at") + " = '' OR " + s.Identifier("next_attempt_at") + " <= " + s.Placeholder(2) + ")) OR (" + s.Identifier("status") + " = 'sending' AND " + s.Identifier("lease_expires_at") + " <= " + s.Placeholder(3) + ")) ORDER BY " + s.Identifier("created_at") + " ASC LIMIT " + s.Placeholder(4)
		rows, queryErr := r.db.QueryContext(workspaceCtx, query, workspaceID, now, now, limit)
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
	s := r.store
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
	result, err := r.db.ExecContext(ctx, "UPDATE "+s.TableIdentifier("integration_outbox_messages")+" SET "+s.Identifier("status")+" = 'sending', "+s.Identifier("next_attempt_at")+" = '', "+s.Identifier("last_attempt_at")+" = "+s.Placeholder(1)+", "+s.Identifier("lease_owner")+" = "+s.Placeholder(2)+", "+s.Identifier("lease_expires_at")+" = "+s.Placeholder(3)+", "+s.Identifier("fencing_token")+" = "+s.Identifier("fencing_token")+" + 1, "+s.Identifier("updated_at")+" = "+s.Placeholder(4)+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(5)+" AND "+s.Identifier("id")+" = "+s.Placeholder(6)+" AND (("+s.Identifier("status")+" = 'queued' AND ("+s.Identifier("next_attempt_at")+" = '' OR "+s.Identifier("next_attempt_at")+" <= "+s.Placeholder(7)+")) OR ("+s.Identifier("status")+" = 'sending' AND "+s.Identifier("lease_expires_at")+" <= "+s.Placeholder(8)+"))", now, owner, expires, now, workspaceID, messageID, now, now)
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
	s := r.store
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
	result, err := r.db.ExecContext(ctx, "UPDATE "+s.TableIdentifier("integration_outbox_messages")+" SET "+s.Identifier("status")+" = "+s.Placeholder(1)+", "+s.Identifier("response_ref")+" = "+s.Placeholder(2)+", "+s.Identifier("error")+" = "+s.Placeholder(3)+", "+s.Identifier("next_attempt_at")+" = "+s.Placeholder(4)+", "+s.Identifier("ack_deadline_at")+" = "+s.Placeholder(5)+", "+s.Identifier("lease_owner")+" = '', "+s.Identifier("lease_expires_at")+" = '', "+s.Identifier("updated_at")+" = "+s.Placeholder(6)+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(7)+" AND "+s.Identifier("id")+" = "+s.Placeholder(8)+" AND "+s.Identifier("status")+" = 'sending' AND "+s.Identifier("lease_owner")+" = "+s.Placeholder(9)+" AND "+s.Identifier("fencing_token")+" = "+s.Placeholder(10), status, responseRef, errorText, "", strings.TrimSpace(ackDeadlineAt), now, workspaceID, messageID, strings.TrimSpace(expectedLeaseOwner), expectedFencingToken)
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
	result, err := r.db.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("integration_outbox_messages")+" SET "+r.store.Identifier("lease_expires_at")+" = "+r.store.Placeholder(1)+", "+r.store.Identifier("updated_at")+" = "+r.store.Placeholder(2)+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(3)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(4)+" AND "+r.store.Identifier("status")+" = 'sending' AND "+r.store.Identifier("lease_owner")+" <> '' AND "+r.store.Identifier("lease_owner")+" = "+r.store.Placeholder(5)+" AND "+r.store.Identifier("fencing_token")+" = "+r.store.Placeholder(6), expires, now, workspaceID, strings.TrimSpace(messageID), strings.TrimSpace(expectedLeaseOwner), expectedFencingToken)
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
	s := r.store
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
	result, err := r.db.ExecContext(ctx, "UPDATE "+s.TableIdentifier("integration_outbox_messages")+" SET "+s.Identifier("status")+" = "+s.Placeholder(1)+", "+s.Identifier("error")+" = "+s.Placeholder(2)+", "+s.Identifier("attempt_count")+" = "+s.Identifier("attempt_count")+" + 1, "+s.Identifier("next_attempt_at")+" = "+s.Placeholder(3)+", "+s.Identifier("last_attempt_at")+" = "+s.Placeholder(4)+", "+s.Identifier("lease_owner")+" = '', "+s.Identifier("lease_expires_at")+" = '', "+s.Identifier("updated_at")+" = "+s.Placeholder(5)+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(6)+" AND "+s.Identifier("id")+" = "+s.Placeholder(7)+" AND "+s.Identifier("status")+" = 'sending' AND "+s.Identifier("lease_owner")+" = "+s.Placeholder(8)+" AND "+s.Identifier("fencing_token")+" = "+s.Placeholder(9), "queued", errorText, next, nowText, nowText, workspaceID, messageID, strings.TrimSpace(expectedLeaseOwner), expectedFencingToken)
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
	s := r.store
	ctx = integrationWorkerWorkspaceContext(ctx, workspaceID, "")
	row := r.db.QueryRowContext(ctx, "SELECT "+integrationEventColumnsSQL(s)+" FROM "+s.TableIdentifier("integration_events")+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(1)+" AND "+s.Identifier("id")+" = "+s.Placeholder(2), workspaceID, strings.TrimSpace(eventID))
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
	s := r.store
	ctx = integrationWorkerWorkspaceContext(ctx, workspaceID, "")
	row := r.db.QueryRowContext(ctx, "SELECT "+integrationOutboxColumnsSQL(s)+" FROM "+s.TableIdentifier("integration_outbox_messages")+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(1)+" AND "+s.Identifier("id")+" = "+s.Placeholder(2), workspaceID, strings.TrimSpace(messageID))
	message, err := scanIntegrationOutboxMessage(row)
	if err == sql.ErrNoRows {
		return integrationmodel.IntegrationOutboxMessage{}, false, nil
	}
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, false, err
	}
	return message, true, nil
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
