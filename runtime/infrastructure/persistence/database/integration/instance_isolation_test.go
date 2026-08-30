package integration

import (
	"context"
	"errors"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"testing"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func TestTwoRuntimeDatabasesKeepConnectorStateAndContextIndependent(t *testing.T) {
	firstStore := openStoreForGeneratedListTest(t)
	defer firstStore.Close()
	secondStore := openStoreForGeneratedListTest(t)
	defer secondStore.Close()
	for _, store := range []*database.RuntimeStore{firstStore, secondStore} {
		if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
			t.Fatal(err)
		}
	}

	first := NewIntegrationConfigStore(firstStore)
	second := NewIntegrationConfigStore(secondStore)
	ctx := t.Context()
	if _, err := first.UpsertConnection(ctx, "default", integrationmodel.IntegrationConnection{Key: "primary", WorkspaceID: "default", ConnectorKey: "email", ProviderKey: "smtp", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if _, err := second.UpsertConnection(ctx, "default", integrationmodel.IntegrationConnection{Key: "primary", WorkspaceID: "default", ConnectorKey: "email", ProviderKey: "sendgrid", Status: "configured"}); err != nil {
		t.Fatal(err)
	}
	if _, err := first.UpsertSecret(ctx, "default", integrationmodel.IntegrationSecret{Key: "credential", WorkspaceID: "default", Kind: "password", Status: "active", ValueRef: "material:credential"}); err != nil {
		t.Fatal(err)
	}
	if err := first.PutSecretMaterial(ctx, "default", "credential", "first-only"); err != nil {
		t.Fatal(err)
	}

	firstConnections, err := first.ListConnections(ctx, "default")
	if err != nil || len(firstConnections) != 1 || firstConnections[0].ProviderKey != "smtp" {
		t.Fatalf("first Runtime connections=%#v err=%v", firstConnections, err)
	}
	secondConnections, err := second.ListConnections(ctx, "default")
	if err != nil || len(secondConnections) != 1 || secondConnections[0].ProviderKey != "sendgrid" {
		t.Fatalf("second Runtime connections=%#v err=%v", secondConnections, err)
	}
	secondSecrets, err := second.ListSecrets(ctx, "default")
	if err != nil || len(secondSecrets) != 0 {
		t.Fatalf("Secret leaked between Runtime databases: %#v err=%v", secondSecrets, err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := second.UpsertConnection(cancelled, "default", integrationmodel.IntegrationConnection{Key: "cancelled", WorkspaceID: "default", ConnectorKey: "email", ProviderKey: "smtp"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller context was not preserved: %v", err)
	}
	secondConnections, err = second.ListConnections(ctx, "default")
	if err != nil || len(secondConnections) != 1 {
		t.Fatalf("cancelled mutation changed second Runtime: %#v err=%v", secondConnections, err)
	}
}
