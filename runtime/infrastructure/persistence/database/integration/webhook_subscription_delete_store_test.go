package integration

import (
	"context"
	"errors"
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestDeleteWebhookSubscriptionScopesAndReportsBackendResult(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationConfigStore(store)
	for _, workspaceID := range []string{"workspace-a", "workspace-b"} {
		if _, err := repository.UpsertWebhookSubscription(t.Context(), workspaceID, integrationmodel.IntegrationWebhookSubscription{Key: "orders", WorkspaceID: workspaceID, ConnectorKey: "webhook", ConnectionKey: "hook", Status: "disabled"}); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := repository.DeleteWebhookSubscription(t.Context(), "workspace-a", "orders")
	if err != nil || !deleted {
		t.Fatalf("delete=%v error=%v", deleted, err)
	}
	if deleted, err = repository.DeleteWebhookSubscription(t.Context(), "workspace-a", "orders"); err != nil || deleted {
		t.Fatalf("missing delete=%v error=%v", deleted, err)
	}
	values, err := repository.ListWebhookSubscriptions(t.Context(), "workspace-b", "", "", "", 10)
	if err != nil || len(values) != 1 {
		t.Fatalf("other workspace values=%#v error=%v", values, err)
	}
}

func TestDeleteWebhookSubscriptionHonorsCancelledContext(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := NewIntegrationConfigStore(store).DeleteWebhookSubscription(ctx, "workspace", "orders")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled delete error=%v", err)
	}
}

func TestDeleteWebhookSubscriptionValidationAndDriverErrors(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	repository := NewIntegrationConfigStore(store)
	if _, err := repository.DeleteWebhookSubscription(t.Context(), "", "orders"); err == nil {
		t.Fatal("empty workspace was accepted")
	}
	if _, err := repository.DeleteWebhookSubscription(t.Context(), "workspace", " "); err == nil {
		t.Fatal("empty subscription key was accepted")
	}

	wantErr := errors.New("delete failure")
	for index, step := range []integrationSQLExecStep{{err: wantErr}, {rowsErr: wantErr}} {
		repository := scriptedIntegrationConfig(t, &integrationSQLState{execSteps: []integrationSQLExecStep{step}})
		if _, err := repository.DeleteWebhookSubscription(t.Context(), "workspace", "orders"); err == nil {
			t.Fatalf("driver failure %d was ignored", index)
		}
	}
}
