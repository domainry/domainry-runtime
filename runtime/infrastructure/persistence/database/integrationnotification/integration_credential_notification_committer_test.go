package integrationnotification

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	integrationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integration"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/testsupport/notificationsdkfixture"
)

func integrationCredentialStoreEvent(id, sourceID string) notificationmodel.NotificationEvent {
	return notificationmodel.NotificationEvent{
		ID: id, WorkspaceID: "workspace-a", Source: "integration", SourceEventID: sourceID,
		EventType: "integration.credential.expired", Category: "integration", Severity: "critical", Surface: "business_workspace",
		RecipientUserIDs: []string{"owner"}, SubjectType: "integration_secret", SubjectID: "secret", ActionState: notificationmodel.NotificationActionOpen,
		AlertState: notificationmodel.NotificationAlertFiring, OccurredAt: "2026-07-28T00:00:00Z",
		Snapshot: notificationmodel.NotificationInboxSnapshot{Title: "Credential expired", Body: "Rotate credential", TemplateKey: "integration.credential.expired.in_app", TemplateVersion: 1, TemplateLocale: "en-US", TemplateContentHash: "hash"},
		Status:   "queued", CreatedAt: "2026-07-28T00:00:00Z", UpdatedAt: "2026-07-28T00:00:00Z",
	}
}

func TestIntegrationCredentialStateAndNotificationCommitOrRollbackTogether(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "credential-notification.db"), IntegrationSecretKey: "credential-notification-test-key"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := notificationsdkfixture.BindTransactions(store); err != nil {
		t.Fatal(err)
	}
	configStore := integrationpersistence.NewIntegrationConfigStore(store)
	committer := NewIntegrationCredentialNotificationCommitter(store)
	connection, err := configStore.UpsertConnection(t.Context(), "workspace-a", integrationmodel.IntegrationConnection{Key: "erp", WorkspaceID: "workspace-a", ConnectorKey: "erp", ProviderKey: "api", Status: "active", CreatedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	connection.Status = "degraded"
	if _, err := committer.CommitIntegrationConnectionNotification(t.Context(), connection, integrationCredentialStoreEvent("notification-connection", "refresh:erp:v1")); err != nil {
		t.Fatal(err)
	}
	connections, err := configStore.ListConnections(t.Context(), "workspace-a")
	if err != nil || len(connections) != 1 || connections[0].Status != "degraded" {
		t.Fatalf("connections=%+v err=%v", connections, err)
	}
	connection.Status = "active"
	if _, err := committer.CommitIntegrationConnectionNotification(t.Context(), connection, integrationCredentialStoreEvent("notification-connection-duplicate", "refresh:erp:v1")); err == nil {
		t.Fatal("expected duplicate connection notification to roll back state")
	}
	connections, _ = configStore.ListConnections(t.Context(), "workspace-a")
	if connections[0].Status != "degraded" {
		t.Fatalf("duplicate notification changed connection: %+v", connections[0])
	}
	invalidConnection := connection
	invalidConnection.WorkspaceID = ""
	if _, err := committer.CommitIntegrationConnectionNotification(t.Context(), invalidConnection, integrationCredentialStoreEvent("notification-invalid-connection", "invalid:connection")); err == nil {
		t.Fatal("expected connection workspace mismatch")
	}

	secret, err := configStore.UpsertSecret(t.Context(), "workspace-a", integrationmodel.IntegrationSecret{Key: "secret", WorkspaceID: "workspace-a", Kind: "api_key", Status: "expired", CreatedBy: "owner", ExpiresAt: "2026-07-27T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	secret.Status, secret.ExpiresAt = "active", "2027-07-28T00:00:00Z"
	duplicate := integrationCredentialStoreEvent("notification-secret", "refresh:erp:v1")
	if _, err := committer.CommitIntegrationSecretNotification(t.Context(), secret, "new-secret-material", duplicate); err == nil {
		t.Fatal("expected duplicate source identity to roll back secret update")
	}
	secrets, err := configStore.ListSecrets(t.Context(), "workspace-a")
	if err != nil || len(secrets) != 1 || secrets[0].Status != "expired" || secrets[0].ExpiresAt != "2026-07-27T00:00:00Z" {
		t.Fatalf("rolled back secrets=%+v err=%v", secrets, err)
	}
	if _, err := configStore.ResolveSecretMaterial(t.Context(), "workspace-a", "secret"); err == nil {
		t.Fatal("rolled-back notification transaction left secret material behind")
	}
	secret.Status, secret.ExpiresAt = "active", "2027-07-28T00:00:00Z"
	if _, err := committer.CommitIntegrationSecretNotification(t.Context(), secret, "committed-secret-material", integrationCredentialStoreEvent("notification-secret-success", "secret:recovered:v2")); err != nil {
		t.Fatal(err)
	}
	if material, err := configStore.ResolveSecretMaterial(t.Context(), "workspace-a", "secret"); err != nil || material != "committed-secret-material" {
		t.Fatalf("material=%q err=%v", material, err)
	}
	withoutMaterial := secret
	withoutMaterial.Key = "metadata-only"
	if _, err := committer.CommitIntegrationSecretNotification(t.Context(), withoutMaterial, "", integrationCredentialStoreEvent("notification-secret-metadata", "secret:metadata:v1")); err != nil {
		t.Fatal(err)
	}
	invalidSecret := secret
	invalidSecret.Key = ""
	if _, err := committer.CommitIntegrationSecretNotification(t.Context(), invalidSecret, "material", integrationCredentialStoreEvent("notification-invalid-material", "secret:invalid-material")); err == nil {
		t.Fatal("expected invalid secret material identity")
	}
	mismatchedSecret := secret
	mismatchedSecret.WorkspaceID = ""
	if _, err := committer.CommitIntegrationSecretNotification(t.Context(), mismatchedSecret, "", integrationCredentialStoreEvent("notification-invalid-secret", "secret:invalid")); err == nil {
		t.Fatal("expected secret workspace mismatch")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := committer.CommitIntegrationConnectionNotification(cancelled, connection, integrationCredentialStoreEvent("cancelled-connection", "cancelled:connection")); !errors.Is(err, context.Canceled) {
		t.Fatalf("connection cancellation err=%v", err)
	}
	if _, err := committer.CommitIntegrationSecretNotification(cancelled, secret, "", integrationCredentialStoreEvent("cancelled-secret", "cancelled:secret")); !errors.Is(err, context.Canceled) {
		t.Fatalf("secret cancellation err=%v", err)
	}
	commitFailure := errors.New("commit failed")
	commitFailing := committer
	commitFailing.commit = func(*sql.Tx) error { return commitFailure }
	connection.Status = "active"
	if _, err := commitFailing.CommitIntegrationConnectionNotification(t.Context(), connection, integrationCredentialStoreEvent("commit-failed-connection", "commit-failed:connection")); !errors.Is(err, commitFailure) {
		t.Fatalf("connection commit err=%v", err)
	}
	connections, _ = configStore.ListConnections(t.Context(), "workspace-a")
	if connections[0].Status != "degraded" {
		t.Fatalf("failed commit persisted connection: %+v", connections[0])
	}
	commitFailedSecret := secret
	commitFailedSecret.Key = "commit-failed-secret"
	if _, err := commitFailing.CommitIntegrationSecretNotification(t.Context(), commitFailedSecret, "", integrationCredentialStoreEvent("commit-failed-secret-event", "commit-failed:secret")); !errors.Is(err, commitFailure) {
		t.Fatalf("secret commit err=%v", err)
	}
	secrets, _ = configStore.ListSecrets(t.Context(), "workspace-a")
	for _, value := range secrets {
		if value.Key == commitFailedSecret.Key {
			t.Fatalf("failed commit persisted secret: %+v", value)
		}
	}
}
