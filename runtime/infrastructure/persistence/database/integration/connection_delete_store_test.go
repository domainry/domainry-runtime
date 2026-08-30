package integration

import (
	"context"
	"errors"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"testing"
)

func TestDeleteConnectionAtomicallyRejectsWebhookReference(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationConfigStore(store)
	if _, err := repository.UpsertConnection(t.Context(), "workspace", integrationmodel.IntegrationConnection{Key: "hook", WorkspaceID: "workspace", ConnectorKey: "webhook", ProviderKey: "http", Status: "disabled"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpsertWebhookSubscription(t.Context(), "workspace", integrationmodel.IntegrationWebhookSubscription{Key: "events", WorkspaceID: "workspace", ConnectorKey: "webhook", ConnectionKey: "hook", Status: "disabled"}); err != nil {
		t.Fatal(err)
	}
	deleted, err := repository.DeleteConnection(t.Context(), "workspace", "hook")
	if err != nil || deleted {
		t.Fatalf("referenced delete=%v error=%v", deleted, err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "DELETE FROM _integration_webhook_subscriptions WHERE workspace_id = ? AND subscription_key = ?", "workspace", "events"); err != nil {
		t.Fatal(err)
	}
	deleted, err = repository.DeleteConnection(t.Context(), "workspace", "hook")
	if err != nil || !deleted {
		t.Fatalf("unreferenced delete=%v error=%v", deleted, err)
	}
}

func TestDeleteConnectionHonorsCancelledContext(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := NewIntegrationConfigStore(store).DeleteConnection(ctx, "workspace", "hook")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled delete error=%v", err)
	}
}
