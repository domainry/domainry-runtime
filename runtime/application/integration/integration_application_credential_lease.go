package integration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const credentialLeasePollInterval = 50 * time.Millisecond
const integrationCredentialNotificationWindow = 30 * 24 * time.Hour

func (s *IntegrationApplicationService) AcquireCredentialRefreshLease(ctx context.Context, connection integrationmodel.IntegrationConnection, callTimeout time.Duration) (func(), error) {
	workspaceID := connection.WorkspaceID
	if err := integrationAuthorizeWorkspaceCommand(workspaceID); err != nil {
		return nil, err
	}
	localRelease, err := s.registry.AcquireCredentialLease(ctx, workspaceID+":"+connection.Key)
	if err != nil {
		return nil, err
	}
	repository, _ := s.configRepo.(integrationrepository.IntegrationCredentialLeaseRepository)
	if provider, ok := s.configRepo.(integrationrepository.IntegrationCredentialLeaseRepositoryProvider); repository == nil && ok {
		repository = provider.CredentialLeaseRepository()
	}
	if repository == nil {
		return localRelease, nil
	}
	owner := newCredentialLeaseOwner()
	duration := credentialRefreshLeaseDuration(callTimeout)
	for {
		now := time.Now().UTC()
		acquired, acquireErr := repository.TryAcquireCredentialRefreshLease(ctx, workspaceID, connection.Key, owner, now.Format(time.RFC3339), now.Add(duration).Format(time.RFC3339))
		if acquireErr != nil {
			localRelease()
			return nil, acquireErr
		}
		if acquired {
			return func() {
				_ = repository.ReleaseCredentialRefreshLease(ctx, workspaceID, connection.Key, owner)
				localRelease()
			}, nil
		}
		timer := time.NewTimer(credentialLeasePollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			localRelease()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func credentialRefreshLeaseDuration(callTimeout time.Duration) time.Duration {
	if callTimeout <= 0 {
		return 2 * time.Minute
	}
	duration := callTimeout + 30*time.Second
	if duration < 2*time.Minute {
		return 2 * time.Minute
	}
	if duration > 10*time.Minute {
		return 10 * time.Minute
	}
	return duration
}

func newCredentialLeaseOwner() string {
	return rand.Text()
}

func (s *IntegrationApplicationService) compileCredentialRefreshFailedNotification(connection integrationmodel.IntegrationConnection, refreshErr error, now time.Time) (notificationmodel.NotificationEvent, bool, error) {
	if s.compileNotification == nil || s.credentialNotifications == nil || strings.TrimSpace(connection.CreatedBy) == "" {
		return notificationmodel.NotificationEvent{}, false, nil
	}
	errorCode := valueOrDefault(integrationErrorCode(refreshErr), "backend.integration.credential.refresh_failed")
	intent := notificationmodel.NotificationIntent{
		ID:          credentialNotificationID(connection.WorkspaceID, "refresh_failed", connection.Key, now.Format(time.RFC3339Nano)),
		WorkspaceID: connection.WorkspaceID, SourceEventID: "credential_refresh_failed:" + connection.Key + ":" + now.Format(time.RFC3339Nano),
		EventType: "integration.credential.refresh_failed", Severity: "critical", Surface: "business_workspace",
		RecipientUserIDs: []string{strings.TrimSpace(connection.CreatedBy)}, SubjectType: "integration_connection", SubjectID: connection.Key, SubjectVersion: connection.UpdatedAt,
		GroupKey: "integration_credential_connection:" + connection.Key, DedupeKey: "integration.credential.refresh_failed:" + connection.Key,
		AlertState: notificationmodel.NotificationAlertFiring, OccurredAt: now.Format(time.RFC3339Nano),
		Variables: map[string]any{
			"credential_name": credentialDisplayName(connection.Name, connection.Key),
			"connection_name": credentialDisplayName(connection.Name, connection.Key),
			"error_code":      errorCode,
		},
	}
	event, err := s.compileNotification(intent)
	return event, err == nil, err
}

// ProcessCredentialExpiryNotifications publishes idempotent 30/7/1-day and
// expired alert milestones. The expiry source is system-scoped and returns no
// secret material; a missing owner is deliberately skipped rather than
// broadening the audience.
func (s *IntegrationApplicationService) ProcessCredentialExpiryNotifications(ctx context.Context, now time.Time, limit int, scope principalmodel.SystemScope) (int, error) {
	if !scope.Valid() {
		return 0, forbidden("backend.system_scope_required")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s.credentialExpirySource == nil || s.publishNotification == nil {
		return 0, nil
	}
	if limit <= 0 {
		limit = 100
	}
	now = now.UTC()
	candidates, err := s.credentialExpirySource.ListIntegrationCredentialExpiryCandidates(ctx, scope, now.Add(integrationCredentialNotificationWindow).Format(time.RFC3339Nano), limit)
	if err != nil {
		return 0, err
	}
	published := 0
	for _, candidate := range candidates {
		intent, ok := integrationCredentialExpiryIntent(candidate, now)
		if !ok {
			continue
		}
		_, created, publishErr := s.publishNotification(ctx, intent, scope)
		if publishErr != nil {
			return published, publishErr
		}
		if created {
			published++
		}
	}
	return published, nil
}

func integrationCredentialExpiryIntent(secret integrationmodel.IntegrationSecret, now time.Time) (notificationmodel.NotificationIntent, bool) {
	owner, workspaceID, key := strings.TrimSpace(secret.CreatedBy), strings.TrimSpace(secret.WorkspaceID), strings.TrimSpace(secret.Key)
	expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(secret.ExpiresAt))
	if integrationAuthorizeWorkspaceCommand(workspaceID) != nil {
		return notificationmodel.NotificationIntent{}, false
	}
	if owner == "" || key == "" || err != nil || secret.Status == "disabled" || secret.Status == "revoked" {
		return notificationmodel.NotificationIntent{}, false
	}
	expiresAt = expiresAt.UTC()
	remaining := expiresAt.Sub(now.UTC())
	if remaining > integrationCredentialNotificationWindow {
		return notificationmodel.NotificationIntent{}, false
	}
	eventType, severity, milestone, daysRemaining := "integration.credential.expiring", "warning", "30d", int(math.Ceil(remaining.Hours()/24))
	if remaining <= 0 || secret.Status == "expired" {
		eventType, severity, milestone, daysRemaining = "integration.credential.expired", "critical", "expired", 0
	} else if remaining <= 24*time.Hour {
		milestone = "1d"
	} else if remaining <= 7*24*time.Hour {
		milestone = "7d"
	}
	sourceID := "credential_expiry:" + key + ":" + expiresAt.Format(time.RFC3339) + ":" + milestone
	return notificationmodel.NotificationIntent{
		ID:          credentialNotificationID(workspaceID, eventType, key, expiresAt.Format(time.RFC3339), milestone),
		WorkspaceID: workspaceID, SourceEventID: sourceID, EventType: eventType, Severity: severity, Surface: "business_workspace",
		RecipientUserIDs: []string{owner}, SubjectType: "integration_secret", SubjectID: key, SubjectVersion: secret.UpdatedAt,
		GroupKey: "integration_credential_secret:" + key, DedupeKey: sourceID, AlertState: notificationmodel.NotificationAlertFiring,
		OccurredAt: now.UTC().Format(time.RFC3339Nano), Variables: map[string]any{
			"credential_name": credentialDisplayName(secret.Description, key),
			"expires_at":      expiresAt.Format(time.RFC3339),
			"days_remaining":  daysRemaining,
		},
	}, true
}

func (s *IntegrationApplicationService) compileCredentialRecoveryNotification(before, after integrationmodel.IntegrationSecret, now time.Time) (notificationmodel.NotificationEvent, bool, error) {
	if s.compileNotification == nil || s.credentialNotifications == nil || strings.TrimSpace(after.CreatedBy) == "" || !credentialExpiryWasAlerting(before, now) || !credentialExpiryIsHealthy(after, now) {
		return notificationmodel.NotificationEvent{}, false, nil
	}
	intent := notificationmodel.NotificationIntent{
		ID:          credentialNotificationID(after.WorkspaceID, "recovered", after.Key, after.RotatedAt),
		WorkspaceID: after.WorkspaceID, SourceEventID: "credential_recovered:" + after.Key + ":" + after.RotatedAt,
		EventType: "integration.credential.recovered", Severity: "info", Surface: "business_workspace",
		RecipientUserIDs: []string{strings.TrimSpace(after.CreatedBy)}, SubjectType: "integration_secret", SubjectID: after.Key, SubjectVersion: after.UpdatedAt,
		GroupKey: "integration_credential_secret:" + after.Key, DedupeKey: "integration.credential.recovered:" + after.Key + ":" + after.RotatedAt,
		ActionState: notificationmodel.NotificationActionCompleted, AlertState: notificationmodel.NotificationAlertResolved,
		OccurredAt: now.UTC().Format(time.RFC3339Nano), Variables: map[string]any{"credential_name": credentialDisplayName(after.Description, after.Key)},
	}
	event, err := s.compileNotification(intent)
	return event, err == nil, err
}

func (s *IntegrationApplicationService) compileCredentialRefreshRecoveryNotification(connection integrationmodel.IntegrationConnection, now time.Time) (notificationmodel.NotificationEvent, bool, error) {
	if s.compileNotification == nil || s.credentialNotifications == nil || strings.TrimSpace(connection.CreatedBy) == "" {
		return notificationmodel.NotificationEvent{}, false, nil
	}
	intent := notificationmodel.NotificationIntent{
		ID:          credentialNotificationID(connection.WorkspaceID, "refresh_recovered", connection.Key, now.Format(time.RFC3339Nano)),
		WorkspaceID: connection.WorkspaceID, SourceEventID: "credential_refresh_recovered:" + connection.Key + ":" + now.Format(time.RFC3339Nano),
		EventType: "integration.credential.recovered", Severity: "info", Surface: "business_workspace",
		RecipientUserIDs: []string{strings.TrimSpace(connection.CreatedBy)}, SubjectType: "integration_connection", SubjectID: connection.Key, SubjectVersion: connection.UpdatedAt,
		GroupKey: "integration_credential_connection:" + connection.Key, DedupeKey: "integration.credential.refresh_recovered:" + connection.Key,
		ActionState: notificationmodel.NotificationActionCompleted, AlertState: notificationmodel.NotificationAlertResolved,
		OccurredAt: now.UTC().Format(time.RFC3339Nano), Variables: map[string]any{
			"credential_name": credentialDisplayName(connection.Name, connection.Key),
			"connection_name": credentialDisplayName(connection.Name, connection.Key),
		},
	}
	event, err := s.compileNotification(intent)
	return event, err == nil, err
}

func credentialExpiryWasAlerting(secret integrationmodel.IntegrationSecret, now time.Time) bool {
	if secret.Status == "expired" {
		return true
	}
	expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(secret.ExpiresAt))
	return err == nil && !expiresAt.After(now.UTC().Add(integrationCredentialNotificationWindow))
}

func credentialExpiryIsHealthy(secret integrationmodel.IntegrationSecret, now time.Time) bool {
	if secret.Status != "active" {
		return false
	}
	if strings.TrimSpace(secret.ExpiresAt) == "" {
		return true
	}
	expiresAt, err := time.Parse(time.RFC3339, secret.ExpiresAt)
	return err == nil && expiresAt.After(now.UTC().Add(integrationCredentialNotificationWindow))
}

func credentialDisplayName(name, key string) string {
	if value := strings.TrimSpace(name); value != "" {
		return value
	}
	return strings.TrimSpace(key)
}

func credentialNotificationID(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "notification_integration_" + hex.EncodeToString(hash[:16])
}
