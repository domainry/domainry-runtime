package integration

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestWebPushSubscriptionIsolationRevocationCleanupAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "push.db")
	open := func() *database.RuntimeStore {
		store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: path, IntegrationSecretKey: "test-integration-secret-key"})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.EnsureRuntimeSchema(t.Context()); err != nil {
			t.Fatal(err)
		}
		return store
	}
	store := open()
	repository := NewIntegrationDeliveryStore(store)
	active := integrationmodel.WebPushSubscription{ID: "sub", WorkspaceID: "tenant-a", UserID: "user-a", EndpointHash: "hash-a", Endpoint: "https://push.example/a", P256DH: "p256dh-secret", Auth: "auth-secret"}
	if _, err := repository.UpsertWebPushSubscription(t.Context(), "tenant-a", active); err != nil {
		t.Fatal(err)
	}
	first, _, err := repository.GetWebPushSubscription(t.Context(), "tenant-a", "sub")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := repository.UpsertWebPushSubscription(t.Context(), "tenant-a", active)
	if err != nil || duplicate.UpdatedAt != first.UpdatedAt {
		t.Fatalf("duplicate upsert was not idempotent: first=%#v duplicate=%#v err=%v", first, duplicate, err)
	}
	if _, err := repository.UpsertWebPushSubscription(t.Context(), "tenant-a", integrationmodel.WebPushSubscription{ID: "sub", WorkspaceID: "tenant-a", UserID: "user-b", EndpointHash: "hash-b", Endpoint: "https://push.example/b", P256DH: "other", Auth: "other"}); err == nil {
		t.Fatal("same-workspace cross-user subscription takeover succeeded")
	}
	if values, err := repository.ListWebPushSubscriptions(t.Context(), "tenant-a", "user-a"); err != nil || len(values) != 1 {
		t.Fatalf("duplicate upsert created extra rows values=%#v err=%v", values, err)
	}
	expired := integrationmodel.WebPushSubscription{ID: "old", WorkspaceID: "tenant-a", UserID: "user-a", EndpointHash: "hash-old", Endpoint: "https://push.example/old", P256DH: "p", Auth: "a", ExpiresAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}
	if _, err := repository.UpsertWebPushSubscription(t.Context(), "tenant-a", expired); err != nil {
		t.Fatal(err)
	}
	if values, err := repository.ListWebPushSubscriptions(t.Context(), "tenant-b", "user-a"); err != nil || len(values) != 0 {
		t.Fatalf("sibling tenant leaked values=%#v err=%v", values, err)
	}
	store.Close()
	store = open()
	defer store.Close()
	repository = NewIntegrationDeliveryStore(store)
	value, found, err := repository.GetWebPushSubscription(t.Context(), "tenant-a", "sub")
	if err != nil || !found || value.Auth != "auth-secret" {
		t.Fatalf("restart value=%#v found=%v err=%v", value, found, err)
	}
	encoded, _ := json.Marshal(value)
	if strings.Contains(string(encoded), "push.example") || strings.Contains(string(encoded), "p256dh-secret") || strings.Contains(string(encoded), "auth-secret") {
		t.Fatalf("public projection leaked subscription material: %s", encoded)
	}
	if count, err := repository.CleanupExpiredWebPushSubscriptions(t.Context(), "tenant-a"); err != nil || count != 1 {
		t.Fatalf("cleanup count=%d err=%v", count, err)
	}
	old, _, _ := repository.GetWebPushSubscription(t.Context(), "tenant-a", "old")
	if old.Status != "expired" || old.Endpoint != "" {
		t.Fatalf("expired material retained: %#v", old)
	}
	revoked, err := repository.RevokeWebPushSubscription(t.Context(), "tenant-a", "sub", "user-a")
	if err != nil || revoked.Status != "revoked" || revoked.Endpoint != "" {
		t.Fatalf("revoked=%#v err=%v", revoked, err)
	}
	if _, err := repository.RevokeWebPushSubscription(t.Context(), "tenant-a", "sub", "other-user"); err == nil {
		t.Fatal("cross-user revoke succeeded")
	}
	repeated, err := repository.RevokeWebPushSubscription(t.Context(), "tenant-a", "sub", "user-a")
	if err != nil || repeated.UpdatedAt != revoked.UpdatedAt || repeated.RevokedAt != revoked.RevokedAt {
		t.Fatalf("duplicate revoke was not idempotent: revoked=%#v repeated=%#v err=%v", revoked, repeated, err)
	}
}
