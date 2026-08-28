package integration

import (
	"context"
	"errors"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"testing"
)

type contextIntegrationConfigContract interface {
	ListSecrets(context.Context, string) ([]integrationmodel.IntegrationSecret, error)
	UpsertSecret(context.Context, string, integrationmodel.IntegrationSecret) (integrationmodel.IntegrationSecret, error)
	PutSecretMaterial(context.Context, string, string, string) error
	ResolveSecretMaterial(context.Context, string, string) (string, error)
	ListConnections(context.Context, string) ([]integrationmodel.IntegrationConnection, error)
	UpsertConnection(context.Context, string, integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error)
	ListExternalIdentities(context.Context, string) ([]integrationmodel.IntegrationExternalIdentity, error)
	UpsertExternalIdentity(context.Context, string, integrationmodel.IntegrationExternalIdentity) (integrationmodel.IntegrationExternalIdentity, error)
	ListWebhookSubscriptions(context.Context, string, string, string, string, int) ([]integrationmodel.IntegrationWebhookSubscription, error)
	UpsertWebhookSubscription(context.Context, string, integrationmodel.IntegrationWebhookSubscription) (integrationmodel.IntegrationWebhookSubscription, error)
	ListAPIKeys(context.Context, string) ([]integrationmodel.IntegrationAPIKey, error)
	UpsertAPIKey(context.Context, string, integrationmodel.IntegrationAPIKey) (integrationmodel.IntegrationAPIKey, error)
	FindAPIKeyByTokenHash(context.Context, string, string) (integrationmodel.IntegrationAPIKey, bool, error)
	UpdateAPIKeyLastUsed(context.Context, string, string, string) (integrationmodel.IntegrationAPIKey, error)
}

var _ contextIntegrationConfigContract = IntegrationConfigStore{}

func TestIntegrationConfigStoreContractAndCancellation(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationConfigStore(store)
	connection, err := repository.UpsertConnection(t.Context(), "default", integrationmodel.IntegrationConnection{Key: "webhook", WorkspaceID: "default", ConnectorKey: "generic_webhook", ProviderKey: "http", Status: "configured", CreatedBy: "admin"})
	if err != nil {
		t.Fatalf("upsert connection: %v", err)
	}
	values, err := repository.ListConnections(t.Context(), "default")
	if err != nil || len(values) != 1 || values[0].Key != connection.Key || values[0].ProviderKey != "http" {
		t.Fatalf("list connections=%#v err=%v", values, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.ListConnections(cancelled, "default"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled list error=%v", err)
	}
	if _, err := repository.UpsertAPIKey(cancelled, "default", integrationmodel.IntegrationAPIKey{Key: "never", WorkspaceID: "default"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled upsert error=%v", err)
	}
}

func TestListConnectionsReusesActionExecutionTransaction(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationConfigStore(store)
	if _, err := repository.UpsertConnection(
		t.Context(),
		"default",
		integrationmodel.IntegrationConnection{
			Key: "primary", WorkspaceID: "default",
			ConnectorKey: "member_center", ProviderKey: "project",
			Status: "active",
		},
	); err != nil {
		t.Fatal(err)
	}
	store.DB().SetMaxOpenConns(1)
	connection, err := store.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer connection.ExecContext(context.WithoutCancel(t.Context()), "ROLLBACK")
	ctx := database.WithActionExecutionTransaction(t.Context(), connection)

	values, err := repository.ListConnections(ctx, "default")
	if err != nil || len(values) != 1 || values[0].Key != "primary" {
		t.Fatalf("Action transaction connections=%+v error=%v", values, err)
	}
}

func TestUpsertConnectionReusesActionExecutionTransaction(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	store.DB().SetMaxOpenConns(1)
	connection, err := store.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer connection.ExecContext(context.WithoutCancel(t.Context()), "ROLLBACK")
	ctx := database.WithActionExecutionTransaction(t.Context(), connection)
	repository := NewIntegrationConfigStore(store)
	saved, err := repository.UpsertConnection(ctx, "default", integrationmodel.IntegrationConnection{
		Key: "transactional", WorkspaceID: "default", ConnectorKey: "member_center", ProviderKey: "project", Status: "active",
	})
	if err != nil || saved.Key != "transactional" {
		t.Fatalf("transactional upsert=%+v error=%v", saved, err)
	}
}

func TestEnsureEvidenceSchemaAddsIntegrationConnectionProviderColumn(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	_, err := store.DB().Exec(`CREATE TABLE integration_connections (
        id TEXT PRIMARY KEY, connection_key TEXT NOT NULL, workspace_id TEXT NOT NULL,
        connector_key TEXT NOT NULL, name TEXT, status TEXT NOT NULL, config_json TEXT NOT NULL,
        secret_refs_json TEXT NOT NULL, created_by TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
    )`)
	if err != nil {
		t.Fatalf("create legacy integration_connections: %v", err)
	}
	if err := store.EnsureEvidenceSchema(t.Context()); err != nil {
		t.Fatalf("migrate evidence schema: %v", err)
	}
	repository := NewIntegrationConfigStore(store)
	saved, err := repository.UpsertConnection(t.Context(), "default", integrationmodel.IntegrationConnection{Key: "hook", WorkspaceID: "default", ConnectorKey: "webhook", ProviderKey: "http", Status: "configured"})
	if err != nil || saved.ProviderKey != "http" {
		t.Fatalf("upsert migrated provider connection=%#v err=%v", saved, err)
	}
}

func TestEnsureEvidenceSchemaAddsCredentialLifecycleColumns(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	_, err := store.DB().Exec(`CREATE TABLE integration_secrets (
        id TEXT PRIMARY KEY, secret_key TEXT NOT NULL, workspace_id TEXT NOT NULL,
        kind TEXT NOT NULL, status TEXT NOT NULL, description TEXT, value_ref TEXT,
        fingerprint TEXT, created_by TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
        disabled_at TEXT
    )`)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureEvidenceSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationConfigStore(store)
	secret, err := repository.UpsertSecret(t.Context(), "default", integrationmodel.IntegrationSecret{Key: "token", WorkspaceID: "default", Kind: "bearer_token", Status: "active", ExpiresAt: "2030-01-01T00:00:00Z", RotatedAt: "2029-01-01T00:00:00Z", LastTestedAt: "2029-01-02T00:00:00Z", LastTestStatus: "succeeded"})
	if err != nil {
		t.Fatal(err)
	}
	values, err := repository.ListSecrets(t.Context(), "default")
	if err != nil || len(values) != 1 || values[0].ExpiresAt != secret.ExpiresAt || values[0].LastTestStatus != "succeeded" {
		t.Fatalf("values=%+v error=%v", values, err)
	}
}

func TestIntegrationConfigStoreWorkspaceIsolationContract(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationConfigStore(store)

	for _, workspaceID := range []string{"workspace-a", "workspace-b"} {
		if _, err := repository.UpsertConnection(t.Context(), workspaceID, integrationmodel.IntegrationConnection{
			Key: "primary", WorkspaceID: workspaceID, ConnectorKey: "webhook", ProviderKey: workspaceID, Status: "active",
		}); err != nil {
			t.Fatalf("upsert %s connection: %v", workspaceID, err)
		}
		if err := repository.PutSecretMaterial(t.Context(), workspaceID, "token", workspaceID+"-secret"); err != nil {
			t.Fatalf("put %s secret material: %v", workspaceID, err)
		}
		if _, err := repository.UpsertAPIKey(t.Context(), workspaceID, integrationmodel.IntegrationAPIKey{
			Key: "runtime", WorkspaceID: workspaceID, TokenHash: workspaceID + "-hash", ActorID: "admin", RoleKey: "admin", Status: "active",
		}); err != nil {
			t.Fatalf("upsert %s api key: %v", workspaceID, err)
		}
	}
	connections, err := repository.ListConnections(t.Context(), "workspace-a")
	if err != nil || len(connections) != 1 || connections[0].ProviderKey != "workspace-a" {
		t.Fatalf("workspace-a connections=%#v err=%v", connections, err)
	}
	material, err := repository.ResolveSecretMaterial(t.Context(), "workspace-b", "token")
	if err != nil || material != "workspace-b-secret" {
		t.Fatalf("workspace-b secret material=%q err=%v", material, err)
	}
	if _, ok, err := repository.FindAPIKeyByTokenHash(t.Context(), "workspace-b", "workspace-a-hash"); err != nil || ok {
		t.Fatalf("cross-workspace api key lookup ok=%v err=%v", ok, err)
	}
	deleted, err := repository.DeleteConnection(t.Context(), "workspace-a", "primary")
	if err != nil || !deleted {
		t.Fatalf("delete workspace-a connection deleted=%v err=%v", deleted, err)
	}
	connections, err = repository.ListConnections(t.Context(), "workspace-b")
	if err != nil || len(connections) != 1 || connections[0].ProviderKey != "workspace-b" {
		t.Fatalf("workspace-b connection affected by workspace-a delete: %#v err=%v", connections, err)
	}

	missingChecks := []func() error{
		func() error { _, err := repository.ListSecrets(t.Context(), ""); return err },
		func() error { _, err := repository.ListConnections(t.Context(), ""); return err },
		func() error { _, err := repository.ListExternalIdentities(t.Context(), ""); return err },
		func() error {
			_, err := repository.ListWebhookSubscriptions(t.Context(), "", "", "", "", 1)
			return err
		},
		func() error { _, err := repository.ListAPIKeys(t.Context(), ""); return err },
		func() error { _, _, err := repository.FindAPIKeyByTokenHash(t.Context(), "", "hash"); return err },
		func() error { _, err := repository.UpdateAPIKeyLastUsed(t.Context(), "", "runtime", ""); return err },
		func() error { return repository.PutSecretMaterial(t.Context(), "", "token", "value") },
		func() error { _, err := repository.ResolveSecretMaterial(t.Context(), "", "token"); return err },
		func() error {
			_, err := repository.TryAcquireCredentialRefreshLease(t.Context(), "", "connection", "owner", "2026-07-19T00:00:00Z", "2026-07-19T00:01:00Z")
			return err
		},
		func() error { return repository.ReleaseCredentialRefreshLease(t.Context(), "", "connection", "owner") },
		func() error { _, err := repository.DeleteConnection(t.Context(), "", "connection"); return err },
	}
	for index, check := range missingChecks {
		if err := check(); err == nil {
			t.Fatalf("missing workspace check %d unexpectedly succeeded", index)
		}
	}

	mismatchChecks := []func() error{
		func() error {
			_, err := repository.UpsertSecret(t.Context(), "workspace-a", integrationmodel.IntegrationSecret{WorkspaceID: "workspace-b"})
			return err
		},
		func() error {
			_, err := repository.UpsertConnection(t.Context(), "workspace-a", integrationmodel.IntegrationConnection{WorkspaceID: "workspace-b"})
			return err
		},
		func() error {
			_, err := repository.UpsertExternalIdentity(t.Context(), "workspace-a", integrationmodel.IntegrationExternalIdentity{WorkspaceID: "workspace-b"})
			return err
		},
		func() error {
			_, err := repository.UpsertWebhookSubscription(t.Context(), "workspace-a", integrationmodel.IntegrationWebhookSubscription{WorkspaceID: "workspace-b"})
			return err
		},
		func() error {
			_, err := repository.UpsertAPIKey(t.Context(), "workspace-a", integrationmodel.IntegrationAPIKey{WorkspaceID: "workspace-b"})
			return err
		},
	}
	for index, check := range mismatchChecks {
		if err := check(); err == nil {
			t.Fatalf("workspace mismatch check %d unexpectedly succeeded", index)
		}
	}
}
