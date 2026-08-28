package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type credentialNotificationCommitterStub struct {
	connection integrationmodel.IntegrationConnection
	secret     integrationmodel.IntegrationSecret
	event      notificationmodel.NotificationEvent
	err        error
}

func (s *credentialNotificationCommitterStub) CommitIntegrationConnectionNotification(_ context.Context, value integrationmodel.IntegrationConnection, event notificationmodel.NotificationEvent) (integrationmodel.IntegrationConnection, error) {
	s.connection, s.event = value, event
	return value, s.err
}

func (s *credentialNotificationCommitterStub) CommitIntegrationSecretNotification(_ context.Context, value integrationmodel.IntegrationSecret, _ string, event notificationmodel.NotificationEvent) (integrationmodel.IntegrationSecret, error) {
	s.secret, s.event = value, event
	return value, s.err
}

type credentialExpirySourceStub struct {
	values []integrationmodel.IntegrationSecret
	err    error
	before string
	limit  int
}

func (s *credentialExpirySourceStub) ListIntegrationCredentialExpiryCandidates(_ context.Context, _ principalmodel.SystemScope, before string, limit int) ([]integrationmodel.IntegrationSecret, error) {
	s.before, s.limit = before, limit
	return append([]integrationmodel.IntegrationSecret(nil), s.values...), s.err
}

func TestIntegrationCredentialExpiryIntentMilestonesAndSafety(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	base := integrationmodel.IntegrationSecret{Key: "erp-token", WorkspaceID: "workspace-a", Status: "active", Description: "ERP token", CreatedBy: "owner-1", UpdatedAt: "v1"}
	for _, test := range []struct {
		name      string
		expiresAt time.Time
		status    string
		eventType string
		milestone string
		days      int
	}{
		{name: "thirty days", expiresAt: now.Add(20 * 24 * time.Hour), eventType: "integration.credential.expiring", milestone: "30d", days: 20},
		{name: "seven days", expiresAt: now.Add(6 * 24 * time.Hour), eventType: "integration.credential.expiring", milestone: "7d", days: 6},
		{name: "one day", expiresAt: now.Add(12 * time.Hour), eventType: "integration.credential.expiring", milestone: "1d", days: 1},
		{name: "expired by time", expiresAt: now.Add(-time.Minute), eventType: "integration.credential.expired", milestone: "expired", days: 0},
		{name: "expired by state", expiresAt: now.Add(time.Hour), status: "expired", eventType: "integration.credential.expired", milestone: "expired", days: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			secret := base
			secret.ExpiresAt = test.expiresAt.Format(time.RFC3339)
			if test.status != "" {
				secret.Status = test.status
			}
			intent, ok := integrationCredentialExpiryIntent(secret, now)
			if !ok || intent.EventType != test.eventType || intent.Variables["days_remaining"] != test.days {
				t.Fatalf("intent=%+v ok=%v", intent, ok)
			}
			if intent.RecipientUserIDs[0] != "owner-1" || intent.SubjectID != "erp-token" || intent.AlertState != notificationmodel.NotificationAlertFiring {
				t.Fatalf("unsafe or incomplete routing: %+v", intent)
			}
			if intent.SourceEventID != "credential_expiry:erp-token:"+test.expiresAt.Format(time.RFC3339)+":"+test.milestone {
				t.Fatalf("source identity=%q", intent.SourceEventID)
			}
			if _, found := intent.Variables["fingerprint"]; found {
				t.Fatal("fingerprint leaked into notification variables")
			}
			if _, found := intent.Variables["value_ref"]; found {
				t.Fatal("secret reference leaked into notification variables")
			}
		})
	}
	for _, invalid := range []integrationmodel.IntegrationSecret{
		{Key: "missing-workspace", Status: "active", ExpiresAt: now.Format(time.RFC3339), CreatedBy: "owner"},
		{WorkspaceID: "workspace-a", Status: "active", ExpiresAt: now.Format(time.RFC3339), CreatedBy: "owner"},
		{Key: "missing-owner", WorkspaceID: "workspace-a", Status: "active", ExpiresAt: now.Format(time.RFC3339)},
		{Key: "invalid-time", WorkspaceID: "workspace-a", Status: "active", ExpiresAt: "invalid", CreatedBy: "owner"},
		{Key: "disabled", WorkspaceID: "workspace-a", Status: "disabled", ExpiresAt: now.Format(time.RFC3339), CreatedBy: "owner"},
		{Key: "revoked", WorkspaceID: "workspace-a", Status: "revoked", ExpiresAt: now.Format(time.RFC3339), CreatedBy: "owner"},
		{Key: "far-away", WorkspaceID: "workspace-a", Status: "active", ExpiresAt: now.Add(31 * 24 * time.Hour).Format(time.RFC3339), CreatedBy: "owner"},
	} {
		if _, ok := integrationCredentialExpiryIntent(invalid, now); ok {
			t.Fatalf("invalid candidate emitted: %+v", invalid)
		}
	}
}

func TestProcessCredentialExpiryNotificationsUsesSystemScopeAndIdempotentPublisher(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	source := &credentialExpirySourceStub{values: []integrationmodel.IntegrationSecret{
		{Key: "one", WorkspaceID: "workspace", Status: "active", ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), CreatedBy: "owner"},
		{Key: "two", WorkspaceID: "workspace", Status: "active", ExpiresAt: now.Add(2 * time.Hour).Format(time.RFC3339), CreatedBy: "owner"},
		{Key: "unowned", WorkspaceID: "workspace", Status: "active", ExpiresAt: now.Add(time.Hour).Format(time.RFC3339)},
	}}
	var intents []notificationmodel.NotificationIntent
	service := NewIntegrationApplicationService(ApplicationDependencies{
		CredentialExpirySource: source,
		NotificationPublisher: func(_ context.Context, intent notificationmodel.NotificationIntent, _ principalmodel.SystemScope) (notificationmodel.NotificationEvent, bool, error) {
			intents = append(intents, intent)
			return notificationmodel.NotificationEvent{}, len(intents) == 1, nil
		},
	})
	if _, err := service.ProcessCredentialExpiryNotifications(t.Context(), now, 10, principalmodel.SystemScope{}); apperror.CodeOf(err) != "backend.system_scope_required" {
		t.Fatalf("invalid scope err=%v", err)
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test credential expiry")
	count, err := service.ProcessCredentialExpiryNotifications(t.Context(), now, 10, scope)
	if err != nil || count != 1 || len(intents) != 2 || source.limit != 10 {
		t.Fatalf("count=%d intents=%d source=%+v err=%v", count, len(intents), source, err)
	}
	if source.before != now.Add(integrationCredentialNotificationWindow).Format(time.RFC3339Nano) {
		t.Fatalf("expiry bound=%q", source.before)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.ProcessCredentialExpiryNotifications(cancelled, now, 10, scope); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err=%v", err)
	}
	source.err = errors.New("query failed")
	if _, err := service.ProcessCredentialExpiryNotifications(t.Context(), now, 10, scope); !errors.Is(err, source.err) {
		t.Fatalf("source err=%v", err)
	}
	if count, err := NewIntegrationApplicationService(ApplicationDependencies{}).ProcessCredentialExpiryNotifications(t.Context(), now, 0, scope); err != nil || count != 0 {
		t.Fatalf("unconfigured count=%d err=%v", count, err)
	}
	if count, err := NewIntegrationApplicationService(ApplicationDependencies{CredentialExpirySource: source}).ProcessCredentialExpiryNotifications(t.Context(), now, 0, scope); err != nil || count != 0 {
		t.Fatalf("missing publisher count=%d err=%v", count, err)
	}
	source.err = nil
	publishFailure := errors.New("notification unavailable")
	failing := NewIntegrationApplicationService(ApplicationDependencies{CredentialExpirySource: source, NotificationPublisher: func(context.Context, notificationmodel.NotificationIntent, principalmodel.SystemScope) (notificationmodel.NotificationEvent, bool, error) {
		return notificationmodel.NotificationEvent{}, false, publishFailure
	}})
	if _, err := failing.ProcessCredentialExpiryNotifications(t.Context(), now, 0, scope); !errors.Is(err, publishFailure) || source.limit != 100 {
		t.Fatalf("publish err=%v default limit=%d", err, source.limit)
	}
}

func TestCredentialRefreshFailureCommitsStateAndSafeNotificationTogether(t *testing.T) {
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	committer := &credentialNotificationCommitterStub{}
	var intent notificationmodel.NotificationIntent
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository, CredentialNotifications: committer,
		NotificationCompiler: func(value notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
			intent = value
			return notificationmodel.NotificationEvent{ID: value.ID, WorkspaceID: value.WorkspaceID, SourceEventID: value.SourceEventID, EventType: value.EventType}, nil
		},
	})
	connection := integrationmodel.IntegrationConnection{Key: "erp", WorkspaceID: "workspace", Name: "ERP", Status: "active", CreatedBy: "owner"}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "worker"}}
	if err := service.RecordCredentialRefreshFailure(t.Context(), connection, principal, errors.New("provider response containing token=secret")); err != nil {
		t.Fatal(err)
	}
	if committer.connection.Status != "degraded" || committer.event.EventType != "integration.credential.refresh_failed" || intent.RecipientUserIDs[0] != "owner" {
		t.Fatalf("connection=%+v event=%+v intent=%+v", committer.connection, committer.event, intent)
	}
	if intent.Variables["error_code"] != "backend.integration.credential.refresh_failed" {
		t.Fatalf("raw provider error was not normalized: %+v", intent.Variables)
	}
	if _, found := intent.Variables["provider_response"]; found {
		t.Fatal("provider response leaked")
	}
	committer.err = errors.New("notification insert failed")
	if err := service.RecordCredentialRefreshFailure(t.Context(), connection, principal, errors.New("failed")); !errors.Is(err, committer.err) {
		t.Fatalf("atomic commit err=%v", err)
	}
	compilerFailure := errors.New("compile failed")
	compileFailing := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, CredentialNotifications: committer, NotificationCompiler: func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, compilerFailure
	}})
	if err := compileFailing.RecordCredentialRefreshFailure(t.Context(), connection, principal, errors.New("failed")); !errors.Is(err, compilerFailure) {
		t.Fatalf("compile err=%v", err)
	}
}

func TestCredentialRefreshRecoveryResolvesConnectionAlertAtomically(t *testing.T) {
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	committer := &credentialNotificationCommitterStub{}
	var intent notificationmodel.NotificationIntent
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository, CredentialNotifications: committer,
		NotificationCompiler: func(value notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
			intent = value
			return notificationmodel.NotificationEvent{ID: value.ID, WorkspaceID: value.WorkspaceID, EventType: value.EventType, AlertState: value.AlertState}, nil
		},
	})
	connection := integrationmodel.IntegrationConnection{Key: "erp", WorkspaceID: "workspace", Name: "ERP", Status: "degraded", CreatedBy: "owner"}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "worker"}}
	if err := service.RecordCredentialRefreshRecovery(t.Context(), connection, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization err=%v", err)
	}
	if err := service.RecordCredentialRefreshRecovery(t.Context(), connection, principal); err != nil {
		t.Fatal(err)
	}
	if committer.connection.Status != "active" || intent.EventType != "integration.credential.recovered" || intent.AlertState != notificationmodel.NotificationAlertResolved || intent.GroupKey != "integration_credential_connection:erp" {
		t.Fatalf("connection=%+v intent=%+v", committer.connection, intent)
	}
	committer.connection = integrationmodel.IntegrationConnection{}
	connection.Status = "active"
	if err := service.RecordCredentialRefreshRecovery(t.Context(), connection, principal); err != nil || committer.connection.Key != "" {
		t.Fatalf("healthy connection recovery err=%v commit=%+v", err, committer.connection)
	}
	connection.Status = "degraded"
	committer.err = errors.New("atomic recovery failed")
	if err := service.RecordCredentialRefreshRecovery(t.Context(), connection, principal); !errors.Is(err, committer.err) {
		t.Fatalf("recovery err=%v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := service.RecordCredentialRefreshRecovery(cancelled, connection, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err=%v", err)
	}
	compilerFailure := errors.New("compile failed")
	compileFailing := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, CredentialNotifications: committer, NotificationCompiler: func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, compilerFailure
	}})
	committer.err = nil
	if err := compileFailing.RecordCredentialRefreshRecovery(t.Context(), connection, principal); !errors.Is(err, compilerFailure) {
		t.Fatalf("compile err=%v", err)
	}
	fallback := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository})
	if err := fallback.RecordCredentialRefreshRecovery(t.Context(), connection, principal); err != nil || repository.connections["erp"].Status != "active" {
		t.Fatalf("fallback err=%v connection=%+v", err, repository.connections["erp"])
	}
}

func TestCredentialRecoveryHealthAndOptionalNotificationEdges(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	if !credentialExpiryIsHealthy(integrationmodel.IntegrationSecret{Status: "active"}, now) {
		t.Fatal("active credential without expiry should be healthy")
	}
	for _, secret := range []integrationmodel.IntegrationSecret{
		{Status: "expired"},
		{Status: "active", ExpiresAt: "invalid"},
		{Status: "active", ExpiresAt: now.Add(time.Hour).Format(time.RFC3339)},
	} {
		if credentialExpiryIsHealthy(secret, now) {
			t.Fatalf("unexpected healthy secret=%+v", secret)
		}
	}
	service := NewIntegrationApplicationService(ApplicationDependencies{})
	if _, ok, err := service.compileCredentialRefreshRecoveryNotification(integrationmodel.IntegrationConnection{CreatedBy: "owner"}, now); err != nil || ok {
		t.Fatalf("optional recovery ok=%v err=%v", ok, err)
	}
	if _, ok, err := service.compileCredentialRecoveryNotification(integrationmodel.IntegrationSecret{Status: "expired"}, integrationmodel.IntegrationSecret{Status: "active"}, now); err != nil || ok {
		t.Fatalf("optional secret recovery ok=%v err=%v", ok, err)
	}
	compiler := func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{EventType: intent.EventType}, nil
	}
	committer := &credentialNotificationCommitterStub{}
	compilerOnly := NewIntegrationApplicationService(ApplicationDependencies{NotificationCompiler: compiler})
	if _, ok, err := compilerOnly.compileCredentialRefreshFailedNotification(integrationmodel.IntegrationConnection{CreatedBy: "owner"}, errors.New("failed"), now); err != nil || ok {
		t.Fatalf("missing committer refresh failure ok=%v err=%v", ok, err)
	}
	configured := NewIntegrationApplicationService(ApplicationDependencies{NotificationCompiler: compiler, CredentialNotifications: committer})
	if _, ok, err := configured.compileCredentialRefreshFailedNotification(integrationmodel.IntegrationConnection{}, errors.New("failed"), now); err != nil || ok {
		t.Fatalf("ownerless refresh failure ok=%v err=%v", ok, err)
	}
	if _, ok, err := compilerOnly.compileCredentialRefreshRecoveryNotification(integrationmodel.IntegrationConnection{CreatedBy: "owner"}, now); err != nil || ok {
		t.Fatalf("missing committer refresh recovery ok=%v err=%v", ok, err)
	}
	if _, ok, err := configured.compileCredentialRefreshRecoveryNotification(integrationmodel.IntegrationConnection{}, now); err != nil || ok {
		t.Fatalf("ownerless refresh recovery ok=%v err=%v", ok, err)
	}
	before := integrationmodel.IntegrationSecret{Status: "expired", ExpiresAt: now.Add(-time.Hour).Format(time.RFC3339)}
	if _, ok, err := compilerOnly.compileCredentialRecoveryNotification(before, integrationmodel.IntegrationSecret{CreatedBy: "owner", Status: "active"}, now); err != nil || ok {
		t.Fatalf("missing committer recovery ok=%v err=%v", ok, err)
	}
	if _, ok, err := configured.compileCredentialRecoveryNotification(before, integrationmodel.IntegrationSecret{Status: "active"}, now); err != nil || ok {
		t.Fatalf("ownerless recovery ok=%v err=%v", ok, err)
	}
	unhealthy := integrationmodel.IntegrationSecret{CreatedBy: "owner", Status: "expired", ExpiresAt: now.Add(-time.Hour).Format(time.RFC3339)}
	if _, ok, err := configured.compileCredentialRecoveryNotification(before, unhealthy, now); err != nil || ok {
		t.Fatalf("unhealthy recovery ok=%v err=%v", ok, err)
	}
}

func TestCredentialRotationResolvesOnlyAnExistingExpiryAlert(t *testing.T) {
	now := time.Now().UTC()
	repository := newIntegrationSecretCommandRepository()
	repository.secrets["secret"] = integrationmodel.IntegrationSecret{
		Key: "secret", WorkspaceID: "workspace", Kind: "api_key", Status: "expired", Description: "ERP token", CreatedBy: "owner",
		ExpiresAt: now.Add(-time.Hour).Format(time.RFC3339),
	}
	committer := &credentialNotificationCommitterStub{}
	var intent notificationmodel.NotificationIntent
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository, CredentialNotifications: committer,
		NotificationCompiler: func(value notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
			intent = value
			return notificationmodel.NotificationEvent{ID: value.ID, WorkspaceID: value.WorkspaceID, EventType: value.EventType}, nil
		},
	})
	principal := integrationRotationPrincipal(PermissionSecretManage)
	saved, err := service.RotateIntegrationSecret(t.Context(), "secret", integrationmodel.IntegrationSecretUpsertRequest{ValueRef: "env:NEW", ExpiresAt: now.Add(90 * 24 * time.Hour).Format(time.RFC3339)}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "active" || intent.EventType != "integration.credential.recovered" || intent.AlertState != notificationmodel.NotificationAlertResolved || committer.event.EventType != intent.EventType {
		t.Fatalf("saved=%+v intent=%+v event=%+v", saved, intent, committer.event)
	}
	repository.secrets["healthy"] = integrationmodel.IntegrationSecret{Key: "healthy", WorkspaceID: "workspace", Kind: "api_key", Status: "active", CreatedBy: "owner", ExpiresAt: now.Add(90 * 24 * time.Hour).Format(time.RFC3339)}
	committer.event = notificationmodel.NotificationEvent{}
	if _, err := service.RotateIntegrationSecret(t.Context(), "healthy", integrationmodel.IntegrationSecretUpsertRequest{ValueRef: "env:NEXT", ExpiresAt: now.Add(120 * 24 * time.Hour).Format(time.RFC3339)}, principal); err != nil {
		t.Fatal(err)
	}
	if committer.event.EventType != "" {
		t.Fatalf("healthy credential emitted false recovery: %+v", committer.event)
	}
	repository.secrets["compile-failure"] = integrationmodel.IntegrationSecret{
		Key: "compile-failure", WorkspaceID: "workspace", Kind: "api_key", Status: "expired", CreatedBy: "owner", ExpiresAt: now.Add(-time.Hour).Format(time.RFC3339),
	}
	compilerFailure := errors.New("compile failed")
	compileFailing := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository, CredentialNotifications: committer,
		NotificationCompiler: func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
			return notificationmodel.NotificationEvent{}, compilerFailure
		},
	})
	if _, err := compileFailing.RotateIntegrationSecret(t.Context(), "compile-failure", integrationmodel.IntegrationSecretUpsertRequest{ValueRef: "env:NEXT", Status: "active", ExpiresAt: now.Add(90 * 24 * time.Hour).Format(time.RFC3339)}, principal); !errors.Is(err, compilerFailure) {
		t.Fatalf("compile failure err=%v", err)
	}
}
