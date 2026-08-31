package integration

import (
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestIntegrationConfigStoreCompleteLifecycle(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationConfigStore(store)

	secret, err := repository.UpsertSecret(t.Context(), "workspace-primary", integrationmodel.IntegrationSecret{Key: "token", Kind: "bearer", Status: "active", CreatedBy: "creator"})
	if err != nil {
		t.Fatal(err)
	}
	secret.Description, secret.CreatedBy = "updated", ""
	secret, err = repository.UpsertSecret(t.Context(), "workspace-primary", secret)
	if err != nil || secret.CreatedBy != "creator" {
		t.Fatalf("updated secret=%+v err=%v", secret, err)
	}
	if values, err := repository.ListSecrets(t.Context(), "workspace-primary"); err != nil || len(values) != 1 || values[0].Description != "updated" {
		t.Fatalf("secrets=%+v err=%v", values, err)
	}
	secret.CreatedBy = "new-creator"
	if secret, err = repository.UpsertSecret(t.Context(), "workspace-primary", secret); err != nil || secret.CreatedBy != "new-creator" {
		t.Fatalf("explicit secret creator=%+v err=%v", secret, err)
	}

	connection, err := repository.UpsertConnection(t.Context(), "workspace-primary", integrationmodel.IntegrationConnection{
		Key: "primary", ConnectorKey: "webhook", ProviderKey: "http", Status: "active",
		Config: map[string]any{"url": "https://example.test"}, SecretRefs: map[string]string{"token": "token"}, CreatedBy: "creator",
	})
	if err != nil {
		t.Fatal(err)
	}
	connection.Name, connection.CreatedBy = "Primary", ""
	if _, err := repository.UpsertConnection(t.Context(), "workspace-primary", connection); err != nil {
		t.Fatal(err)
	}

	identity := integrationmodel.IntegrationExternalIdentity{Key: "external", Provider: "slack", ExternalSubject: "U1", ActorID: "user", RoleKey: "member", Status: "active", CreatedBy: "creator"}
	if _, err := repository.UpsertExternalIdentity(t.Context(), "workspace-primary", identity); err != nil {
		t.Fatal(err)
	}
	identity.ExternalName, identity.CreatedBy = "User", ""
	if _, err := repository.UpsertExternalIdentity(t.Context(), "workspace-primary", identity); err != nil {
		t.Fatal(err)
	}
	if values, err := repository.ListExternalIdentities(t.Context(), "workspace-primary"); err != nil || len(values) != 1 || values[0].ExternalName != "User" {
		t.Fatalf("identities=%+v err=%v", values, err)
	}

	subscription := integrationmodel.IntegrationWebhookSubscription{Key: "orders", Name: "Orders", ConnectorKey: "webhook", ConnectionKey: "primary", EventTypes: []string{"order.*"}, Status: "active", CreatedBy: "creator"}
	if _, err := repository.UpsertWebhookSubscription(t.Context(), "workspace-primary", subscription); err != nil {
		t.Fatal(err)
	}
	subscription.Description, subscription.CreatedBy = "updated", ""
	if _, err := repository.UpsertWebhookSubscription(t.Context(), "workspace-primary", subscription); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{0, 501, 1} {
		values, err := repository.ListWebhookSubscriptions(t.Context(), "workspace-primary", " webhook ", "order.created", " active ", limit)
		if err != nil || len(values) != 1 || values[0].Description != "updated" {
			t.Fatalf("subscriptions(limit=%d)=%+v err=%v", limit, values, err)
		}
	}
	if values, err := repository.ListWebhookSubscriptions(t.Context(), "workspace-primary", "", "invoice.created", "", 1); err != nil || len(values) != 0 {
		t.Fatalf("nonmatching subscriptions=%+v err=%v", values, err)
	}

	apiKey := integrationmodel.IntegrationAPIKey{Key: "runtime", Name: "Runtime", TokenHash: "hash", ActorID: "admin", RoleKey: "admin", Scopes: []string{"records.read"}, Status: "active", CreatedBy: "creator"}
	if _, err := repository.UpsertAPIKey(t.Context(), "workspace-primary", apiKey); err != nil {
		t.Fatal(err)
	}
	apiKey.Name, apiKey.CreatedBy = "Updated", ""
	if _, err := repository.UpsertAPIKey(t.Context(), "workspace-primary", apiKey); err != nil {
		t.Fatal(err)
	}
	if values, err := repository.ListAPIKeys(t.Context(), "workspace-primary"); err != nil || len(values) != 1 || values[0].Name != "Updated" {
		t.Fatalf("api keys=%+v err=%v", values, err)
	}
	if value, ok, err := repository.FindAPIKeyByTokenHash(t.Context(), "workspace-primary", " hash "); err != nil || !ok || value.Key != "runtime" {
		t.Fatalf("find api key=%+v ok=%v err=%v", value, ok, err)
	}
	if _, ok, err := repository.FindAPIKeyByTokenHash(t.Context(), "workspace-primary", "missing"); err != nil || ok {
		t.Fatalf("missing api key ok=%v err=%v", ok, err)
	}
	if _, err := repository.UpdateAPIKeyLastUsed(t.Context(), "workspace-primary", "", ""); err == nil {
		t.Fatal("empty api key accepted")
	}
	if value, err := repository.UpdateAPIKeyLastUsed(t.Context(), "workspace-primary", "runtime", ""); err != nil || value.LastUsedAt == "" {
		t.Fatalf("updated api key=%+v err=%v", value, err)
	}
	if _, err := repository.UpdateAPIKeyLastUsed(t.Context(), "workspace-primary", "missing", "2026-07-20T00:00:00Z"); err == nil {
		t.Fatal("missing api key update succeeded")
	}
}

func TestCredentialRefreshLeaseInputEdges(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	repository := NewIntegrationConfigStore(store)
	for _, values := range [][4]string{{"", "owner", "now", "expires"}, {"connection", "", "now", "expires"}, {"connection", "owner", "", "expires"}, {"connection", "owner", "now", ""}} {
		if _, err := repository.TryAcquireCredentialRefreshLease(t.Context(), "workspace-primary", values[0], values[1], values[2], values[3]); err == nil {
			t.Fatalf("invalid lease input accepted: %q", values)
		}
	}
	if _, err := repository.DeleteConnection(t.Context(), "workspace-primary", " "); err == nil {
		t.Fatal("empty connection key accepted")
	}
}
