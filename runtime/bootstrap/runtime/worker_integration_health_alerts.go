package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	notificationcontract "github.com/domainry/domainry-notification-sdk/contract"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
)

const integrationHealthAlertGroupPrefix = "installation.integration.health."

func (a *Runtime) startIntegrationHealthAlertWorker(ctx context.Context) {
	if a == nil || !a.cfg.IntegrationHealthAlertsEnabled || a.integrationBinding == nil || a.notificationBinding == nil || a.identityProjection == nil {
		return
	}
	managementBinding, ok := a.integrationBinding.(integrationsdk.ManagementBinding)
	if !ok || managementBinding.Management() == nil {
		return
	}
	monitor, ok := managementBinding.Management().(integrationsdk.ProviderRunMonitoring)
	if !ok {
		return
	}
	alertBinding, ok := a.notificationBinding.(notificationsdk.SystemAlertBinding)
	if !ok || alertBinding.SystemAlerts() == nil || a.notificationBinding.Publisher() == nil {
		return
	}
	if strings.TrimSpace(a.cfg.IdentityWorkspaceID) == "" || strings.TrimSpace(a.cfg.NotificationWorkspaceID) == "" {
		return
	}

	a.startControlledWorker(ctx, "integration_health_alerts", func(workerCtx context.Context) <-chan struct{} {
		return workerplatform.StartNamedLoop(workerCtx, "integration_health_alerts", a.cfg.IntegrationHealthAlertPollInterval, func() {
			runLoggedRuntimeWorkerTick(workerCtx, a.worker.Control, "Integration health alert evaluation failed", func() error {
				return a.runIntegrationHealthAlertTick(workerCtx, monitor, alertBinding.SystemAlerts(), a.notificationBinding.Publisher())
			})
		})
	})
}

func (a *Runtime) runIntegrationHealthAlertTick(ctx context.Context, monitor integrationsdk.ProviderRunMonitoring, alerts notificationsdk.SystemAlerts, publisher notificationsdk.Publisher) error {
	snapshot, err := monitor.ProviderRunSnapshot(ctx)
	if err != nil {
		return fmt.Errorf("read Integration health snapshot: %w", err)
	}
	conditions, err := deploymentapplication.EvaluateIntegrationHealth(snapshot, a.integrationHealthAlertPolicy())
	if err != nil {
		return err
	}
	observedAt, err := time.Parse(time.RFC3339Nano, snapshot.ObservedAt)
	if err != nil {
		return fmt.Errorf("parse Integration health observed_at: %w", err)
	}
	recipients, err := installationAdministratorRecipients(ctx, a.identityProjection, a.cfg.IdentityWorkspaceID, observedAt)
	if err != nil {
		return err
	}
	_, err = publishIntegrationHealthAlertTransitions(ctx, strings.TrimSpace(a.cfg.NotificationWorkspaceID), recipients, conditions, alerts, publisher)
	return err
}

func (a *Runtime) integrationHealthAlertPolicy() deploymentapplication.IntegrationHealthAlertPolicy {
	return deploymentapplication.IntegrationHealthAlertPolicy{
		ReadyDueLimit: int64(a.cfg.IntegrationHealthReadyDueLimit), FailedDueLimit: int64(a.cfg.IntegrationHealthFailedDueLimit),
		ExpiredLeaseLimit: int64(a.cfg.IntegrationHealthExpiredLeaseLimit), DeadLetterLimit: int64(a.cfg.IntegrationHealthDeadLetterLimit),
		GoogleHTTP429HourLimit: int64(a.cfg.IntegrationHealthGoogleHTTP429HourLimit), GoogleGmailRateLimitHourLimit: int64(a.cfg.IntegrationHealthGoogleGmailRateLimitHourLimit), FeishuRateLimitHourLimit: int64(a.cfg.IntegrationHealthFeishuRateLimitHourLimit), GoogleGmailHistoryGapHourLimit: int64(a.cfg.IntegrationHealthGoogleGmailHistoryGapHourLimit), GoogleQuotaUsedPercentLimit: int64(a.cfg.IntegrationHealthGoogleQuotaUsedPercentLimit), QueueOldestAgeLimit: a.cfg.IntegrationHealthQueueOldestAgeLimit,
		GooglePushDelayLimit: a.cfg.IntegrationHealthGooglePushDelayLimit, GoogleSyncDelayLimit: a.cfg.IntegrationHealthGoogleSyncDelayLimit,
	}
}

func installationAdministratorRecipients(ctx context.Context, projection identitysdk.Projection, workspaceID string, observedAt time.Time) ([]string, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if projection == nil || workspaceID == "" {
		return nil, fmt.Errorf("installation administrator projection scope is required")
	}
	scoped := requestcontext.WithWorkspaceID(ctx, workspaceID)
	roles, err := projection.ListRoles(scoped, identitysdk.ProjectionQuery{})
	if err != nil {
		return nil, fmt.Errorf("list installation roles: %w", err)
	}
	roleIDs := map[string]bool{}
	for _, role := range roles {
		if strings.TrimSpace(role.Key) == projectmodel.InstallationAdministratorRoleKey && strings.EqualFold(strings.TrimSpace(role.Status), identitysdk.UserStatusActive) {
			roleIDs[strings.TrimSpace(role.ID)] = true
		}
	}
	if len(roleIDs) == 0 {
		return nil, fmt.Errorf("active installation administrator role is unavailable")
	}
	assignments, err := projection.ListUserRoleAssignments(scoped, identitysdk.UserRoleAssignmentQuery{})
	if err != nil {
		return nil, fmt.Errorf("list installation administrator assignments: %w", err)
	}
	assigned := map[string]bool{}
	for _, assignment := range assignments {
		active, activeErr := identityRoleAssignmentActiveAt(assignment, observedAt)
		if activeErr != nil {
			return nil, activeErr
		}
		if roleIDs[strings.TrimSpace(assignment.RoleID)] && active {
			assigned[strings.TrimSpace(assignment.UserID)] = true
		}
	}
	users, err := projection.ListUsers(scoped, identitysdk.ProjectionQuery{})
	if err != nil {
		return nil, fmt.Errorf("list installation users: %w", err)
	}
	recipients := make([]string, 0, len(assigned))
	for _, user := range users {
		userID := strings.TrimSpace(user.ID)
		if assigned[userID] && strings.EqualFold(strings.TrimSpace(user.Status), identitysdk.UserStatusActive) {
			recipients = append(recipients, userID)
		}
	}
	sort.Strings(recipients)
	if len(recipients) == 0 {
		return nil, fmt.Errorf("active installation administrator recipient is unavailable")
	}
	return recipients, nil
}

func identityRoleAssignmentActiveAt(value identitysdk.UserRoleAssignment, at time.Time) (bool, error) {
	if !strings.EqualFold(strings.TrimSpace(value.Status), identitysdk.UserStatusActive) || strings.TrimSpace(value.UserID) == "" || strings.TrimSpace(value.RoleID) == "" {
		return false, nil
	}
	for name, raw := range map[string]string{"valid_from": value.ValidFrom, "valid_until": value.ValidUntil, "expires_at": stringValue(value.ExpiresAt)} {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		boundary, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return false, fmt.Errorf("parse installation administrator assignment %s: %w", name, err)
		}
		switch name {
		case "valid_from":
			if boundary.After(at) {
				return false, nil
			}
		default:
			if !boundary.After(at) {
				return false, nil
			}
		}
	}
	return true, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func publishIntegrationHealthAlertTransitions(ctx context.Context, workspaceID string, recipients []string, conditions []deploymentapplication.IntegrationHealthCondition, alerts notificationsdk.SystemAlerts, publisher notificationsdk.Publisher) (int, error) {
	if strings.TrimSpace(workspaceID) == "" || alerts == nil || publisher == nil {
		return 0, fmt.Errorf("Integration health alert publication ports are required")
	}
	published := 0
	for _, condition := range conditions {
		groupKey := integrationHealthAlertGroupPrefix + condition.Key
		for _, recipient := range recipients {
			recipient = strings.TrimSpace(recipient)
			group, found, err := alerts.GetAlertGroup(ctx, workspaceID, recipient, groupKey)
			if err != nil {
				return published, fmt.Errorf("read Integration health alert group %s for %s: %w", condition.Key, recipient, err)
			}
			if integrationHealthAlertStateMatches(found, group.State, condition.Firing) {
				continue
			}
			if found && group.State != notificationcontract.NotificationAlertFiring && group.State != notificationcontract.NotificationAlertAcknowledged && group.State != notificationcontract.NotificationAlertResolved {
				return published, fmt.Errorf("Integration health alert group %s has unsupported state %q", condition.Key, group.State)
			}
			lastEventID := "none"
			if found {
				lastEventID = group.LastEventID
			}
			intent := integrationHealthTransitionIntent(workspaceID, recipient, condition, groupKey, lastEventID)
			if _, _, err := publisher.PublishIntent(ctx, intent); err != nil {
				return published, fmt.Errorf("publish Integration health alert transition %s for %s: %w", condition.Key, recipient, err)
			}
			published++
		}
	}
	return published, nil
}

func integrationHealthAlertStateMatches(found bool, current string, firing bool) bool {
	if firing {
		return found && (current == notificationcontract.NotificationAlertFiring || current == notificationcontract.NotificationAlertAcknowledged)
	}
	return !found || current == notificationcontract.NotificationAlertResolved
}

func integrationHealthTransitionIntent(workspaceID, recipient string, condition deploymentapplication.IntegrationHealthCondition, groupKey, lastEventID string) notificationcontract.NotificationIntent {
	state, eventType := notificationcontract.NotificationAlertResolved, "integration.health.recovered"
	if condition.Firing {
		state, eventType = notificationcontract.NotificationAlertFiring, "integration.health.degraded"
	}
	seed := strings.Join([]string{workspaceID, recipient, groupKey, state, lastEventID}, "\x00")
	digest := sha256.Sum256([]byte(seed))
	eventID := "integration-health-" + hex.EncodeToString(digest[:])
	return notificationcontract.NotificationIntent{
		ID: eventID, WorkspaceID: workspaceID, SourceEventID: eventID, EventType: eventType,
		RecipientUserIDs: []string{recipient}, SubjectType: "integration_health", SubjectID: condition.Key, SubjectVersion: condition.ObservedAt,
		GroupKey: groupKey, DedupeKey: eventID, AlertState: state, OccurredAt: condition.ObservedAt,
		Variables: map[string]any{
			"metric_name": condition.MetricName, "observed_value": condition.Current, "threshold": condition.Limit,
			"unit": condition.Unit, "observed_at": condition.ObservedAt,
		},
	}
}
