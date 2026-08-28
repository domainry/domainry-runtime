package notificationpublication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	notificationsdk "github.com/domainry/domainry-notification-sdk"
	sdkcontract "github.com/domainry/domainry-notification-sdk/contract"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
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
	store     Store
	publisher notificationsdk.Publisher
	workerID  string
	clock     workerplatform.Clock
}

func NewRelay(store Store, publisher notificationsdk.Publisher, workerID string, clock workerplatform.Clock) (*Relay, error) {
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
	rows, err := s.DB().QueryContext(ctx, "SELECT "+s.Identifier("workspace_id")+","+s.Identifier("id")+" FROM "+s.TableIdentifier("notification_publication_outbox")+" WHERE (("+s.Identifier("status")+"='queued' AND ("+s.Identifier("next_attempt_at")+"='' OR "+s.Identifier("next_attempt_at")+"<="+s.Placeholder(1)+")) OR ("+s.Identifier("status")+"='sending' AND "+s.Identifier("lease_expires_at")+"<="+s.Placeholder(2)+")) ORDER BY "+s.Identifier("created_at")+" ASC LIMIT "+s.Placeholder(3), now, now, limit)
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
	result, err := s.DB().ExecContext(ctx, "UPDATE "+s.TableIdentifier("notification_publication_outbox")+" SET "+s.Identifier("status")+"='sending',"+s.Identifier("last_attempt_at")+"="+s.Placeholder(1)+","+s.Identifier("lease_owner")+"="+s.Placeholder(2)+","+s.Identifier("lease_expires_at")+"="+s.Placeholder(3)+","+s.Identifier("fencing_token")+"="+s.Identifier("fencing_token")+"+1,"+s.Identifier("updated_at")+"="+s.Placeholder(4)+" WHERE "+s.Identifier("workspace_id")+"="+s.Placeholder(5)+" AND "+s.Identifier("id")+"="+s.Placeholder(6)+" AND (("+s.Identifier("status")+"='queued' AND ("+s.Identifier("next_attempt_at")+"='' OR "+s.Identifier("next_attempt_at")+"<="+s.Placeholder(7)+")) OR ("+s.Identifier("status")+"='sending' AND "+s.Identifier("lease_expires_at")+"<="+s.Placeholder(8)+"))", nowText, r.workerID, expires, nowText, locator.WorkspaceID, locator.TaskID, nowText, nowText)
	if err != nil {
		return publication{}, false, fmt.Errorf("claim Notification SaaS publication: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows == 0 {
		return publication{}, false, err
	}
	var value publication
	err = s.DB().QueryRowContext(ctx, "SELECT "+s.Identifier("id")+","+s.Identifier("workspace_id")+","+s.Identifier("intent_json")+","+s.Identifier("status")+","+s.Identifier("attempt_count")+","+s.Identifier("lease_owner")+","+s.Identifier("lease_expires_at")+","+s.Identifier("fencing_token")+" FROM "+s.TableIdentifier("notification_publication_outbox")+" WHERE "+s.Identifier("workspace_id")+"="+s.Placeholder(1)+" AND "+s.Identifier("id")+"="+s.Placeholder(2), locator.WorkspaceID, locator.TaskID).Scan(&value.ID, &value.WorkspaceID, &value.IntentJSON, &value.Status, &value.AttemptCount, &value.LeaseOwner, &value.LeaseExpiresAt, &value.FencingToken)
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
	result, err := s.DB().ExecContext(ctx, "UPDATE "+s.TableIdentifier("notification_publication_outbox")+" SET "+s.Identifier("status")+"="+s.Placeholder(1)+","+s.Identifier("attempt_count")+"="+s.Identifier("attempt_count")+"+1,"+s.Identifier("next_attempt_at")+"="+s.Placeholder(2)+","+s.Identifier("lease_owner")+"='',"+s.Identifier("lease_expires_at")+"='',"+s.Identifier("remote_event_id")+"="+s.Placeholder(3)+","+s.Identifier("last_error_code")+"="+s.Placeholder(4)+","+s.Identifier("last_error")+"="+s.Placeholder(5)+","+s.Identifier("terminal_at")+"="+s.Placeholder(6)+","+s.Identifier("updated_at")+"="+s.Placeholder(7)+" WHERE "+s.Identifier("workspace_id")+"="+s.Placeholder(8)+" AND "+s.Identifier("id")+"="+s.Placeholder(9)+" AND "+s.Identifier("status")+"='sending' AND "+s.Identifier("lease_owner")+"="+s.Placeholder(10)+" AND "+s.Identifier("fencing_token")+"="+s.Placeholder(11), status, next, remoteEventID, code, message, terminalAt, now, value.WorkspaceID, value.ID, value.LeaseOwner, value.FencingToken)
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
