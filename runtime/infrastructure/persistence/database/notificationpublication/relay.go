package notificationpublication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	sdkcontract "github.com/domainry/domainry-notification-sdk/contract"
	ormbuilder "github.com/domainry/domainry-orm/query"
)

const (
	publicationLeaseTTL   = 5 * time.Minute
	publicationMaxAttempt = 12
)

type publication struct {
	ID, WorkspaceID, IntentJSON, Status, LeaseOwner, LeaseExpiresAt string
	AttemptCount                                                    int
	FencingToken                                                    int64
}

type Relay struct {
	store     PublicationOutboxStore
	publisher notificationsdk.Publisher
	workerID  string
	clock     workerplatform.Clock
}

func NewRelay(store PublicationOutboxStore, publisher notificationsdk.Publisher, workerID string, clock workerplatform.Clock) (*Relay, error) {
	if store.runtime == nil || publisher == nil || strings.TrimSpace(workerID) == "" || clock == nil {
		return nil, fmt.Errorf("Notification SaaS publication relay dependencies are required")
	}
	return &Relay{store: store, publisher: publisher, workerID: strings.TrimSpace(workerID), clock: clock}, nil
}

func (r *Relay) ProcessDue(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	now := r.clock.Now().UTC().Format(time.RFC3339Nano)
	s := r.store.runtime
	due := ormbuilder.And(ormbuilder.Equal("publication_type", "notification.saas"), publicationDuePredicate(now))
	query, args, buildErr := ormbuilder.NewSelectBuilder(s.SQLRenderer, "runtime_publication_outbox").Columns("workspace_id", "id").Where(due).OrderBy(ormbuilder.Ascending("created_at"), ormbuilder.Ascending("id")).Limit(limit).Build()
	if buildErr != nil {
		return 0, fmt.Errorf("build due Notification SaaS publication list: %w", buildErr)
	}
	rows, err := s.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("list due Notification SaaS publications: %w", err)
	}
	defer rows.Close()
	locators := []workerplatform.DurableTaskLocator{}
	for rows.Next() {
		var locator workerplatform.DurableTaskLocator
		locator.QueueKind = "notification_publication"
		if err := rows.Scan(&locator.WorkspaceID, &locator.TaskID); err != nil {
			return 0, err
		}
		locators = append(locators, locator)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	processed := 0
	for _, locator := range locators {
		didWork, processErr := r.Process(ctx, locator)
		if processErr != nil {
			return processed, processErr
		}
		if didWork {
			processed++
		}
	}
	return processed, nil
}

func (r *Relay) Process(ctx context.Context, locator workerplatform.DurableTaskLocator) (bool, error) {
	claimed, ok, err := r.claim(ctx, locator)
	if err != nil || !ok {
		return false, err
	}
	var intent sdkcontract.NotificationIntent
	if err := json.Unmarshal([]byte(claimed.IntentJSON), &intent); err != nil {
		return true, r.fail(ctx, claimed, "notification.publication_payload_invalid", err, false)
	}
	event, _, publishErr := r.publisher.PublishIntent(ctx, intent)
	if publishErr == nil {
		return true, r.complete(ctx, claimed, event.ID)
	}
	code, retryable := publicationError(publishErr)
	return true, r.fail(ctx, claimed, code, publishErr, retryable)
}

func (r *Relay) claim(ctx context.Context, locator workerplatform.DurableTaskLocator) (publication, bool, error) {
	s := r.store.runtime
	now := r.clock.Now().UTC()
	nowText, expires := now.Format(time.RFC3339Nano), now.Add(publicationLeaseTTL).Format(time.RFC3339Nano)
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.SQLRenderer, "runtime_publication_outbox", locator.WorkspaceID).Set("status", "sending").Set("last_attempt_at", nowText).Set("lease_owner", r.workerID).Set("lease_expires_at", expires).SetExpression("fencing_token", ormbuilder.Add(ormbuilder.Column("fencing_token"), ormbuilder.Value(1))).Set("updated_at", nowText).Where(ormbuilder.And(ormbuilder.Equal("publication_type", "notification.saas"), ormbuilder.Equal("id", locator.TaskID), publicationDuePredicate(nowText))).Build()
	if buildErr != nil {
		return publication{}, false, fmt.Errorf("build Notification SaaS publication claim: %w", buildErr)
	}
	result, err := s.DB().ExecContext(ctx, query, args...)
	if err != nil {
		return publication{}, false, fmt.Errorf("claim Notification SaaS publication: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows == 0 {
		return publication{}, false, err
	}
	var value publication
	lookup, lookupArgs, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.SQLRenderer, "runtime_publication_outbox", locator.WorkspaceID).Columns("id", "workspace_id", "intent_json", "status", "attempt_count", "lease_owner", "lease_expires_at", "fencing_token").Where(ormbuilder.And(ormbuilder.Equal("publication_type", "notification.saas"), ormbuilder.Equal("id", locator.TaskID))).Build()
	if buildErr != nil {
		return publication{}, false, buildErr
	}
	err = s.DB().QueryRowContext(ctx, lookup, lookupArgs...).Scan(&value.ID, &value.WorkspaceID, &value.IntentJSON, &value.Status, &value.AttemptCount, &value.LeaseOwner, &value.LeaseExpiresAt, &value.FencingToken)
	return value, err == nil, err
}

func (r *Relay) complete(ctx context.Context, value publication, remoteEventID string) error {
	return r.transition(ctx, value, "delivered", "", "", strings.TrimSpace(remoteEventID), "", false)
}

func (r *Relay) fail(ctx context.Context, value publication, code string, cause error, retryable bool) error {
	attempt := value.AttemptCount + 1
	status, next, terminal := "queued", r.clock.Now().UTC().Add(publicationRetryDelay(attempt)).Format(time.RFC3339Nano), false
	if !retryable || attempt >= publicationMaxAttempt {
		status, next, terminal = "dead_letter", "", true
	}
	return r.transition(ctx, value, status, next, code, "", cause.Error(), terminal)
}

func (r *Relay) transition(ctx context.Context, value publication, status, next, code, remoteEventID, message string, terminal bool) error {
	s := r.store.runtime
	now := r.clock.Now().UTC().Format(time.RFC3339Nano)
	terminalAt := ""
	if terminal {
		terminalAt = now
	}
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.SQLRenderer, "runtime_publication_outbox", value.WorkspaceID).Set("status", status).SetExpression("attempt_count", ormbuilder.Add(ormbuilder.Column("attempt_count"), ormbuilder.Value(1))).Set("next_attempt_at", next).Set("lease_owner", "").Set("lease_expires_at", "").Set("remote_event_id", remoteEventID).Set("last_error_code", code).Set("last_error", message).Set("terminal_at", terminalAt).Set("updated_at", now).Where(ormbuilder.And(ormbuilder.Equal("publication_type", "notification.saas"), ormbuilder.Equal("id", value.ID), ormbuilder.Equal("status", "sending"), ormbuilder.Equal("lease_owner", value.LeaseOwner), ormbuilder.Equal("fencing_token", value.FencingToken))).Build()
	if buildErr != nil {
		return buildErr
	}
	result, err := s.DB().ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("Notification SaaS publication lease was lost")
	}
	return nil
}

func publicationDuePredicate(now string) ormbuilder.Predicate {
	return ormbuilder.Or(
		ormbuilder.And(ormbuilder.Equal("status", "queued"), ormbuilder.Or(ormbuilder.Equal("next_attempt_at", ""), ormbuilder.LessThanOrEqual("next_attempt_at", now))),
		ormbuilder.And(ormbuilder.Equal("status", "sending"), ormbuilder.LessThanOrEqual("lease_expires_at", now)),
	)
}

func publicationError(err error) (string, bool) {
	var sdkErr *notificationsdk.Error
	if errors.As(err, &sdkErr) {
		code := strings.TrimSpace(sdkErr.Code)
		if code == "" {
			code = "notification.remote_failed"
		}
		return code, sdkErr.Retryable
	}
	return "notification.remote_outcome_unknown", true
}

func publicationRetryDelay(attempt int) time.Duration {
	seconds := math.Pow(2, float64(attempt-1))
	if seconds > 300 {
		seconds = 300
	}
	return time.Duration(seconds) * time.Second
}
