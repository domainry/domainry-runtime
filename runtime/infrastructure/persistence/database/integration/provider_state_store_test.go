package integration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestConnectorProviderStateLifecycleIsGenericAndFenced(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationConfigStore(store)
	connection, err := repository.UpsertConnection(t.Context(), "workspace", integrationmodel.IntegrationConnection{Key: "primary", WorkspaceID: "workspace", ConnectorKey: "example", ProviderKey: "provider", Status: "active", Config: map[string]any{}, SecretRefs: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	tasks := []integrationrepository.ConnectorProviderTask{{Key: "sync", StateVersion: 1, InitialState: json.RawMessage(`{"cursor":"10"}`)}, {Key: "watch", StateVersion: 1, InitialState: json.RawMessage(`{}`)}}
	if err := repository.SyncConnectorProviderTasks(t.Context(), connection, tasks, now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test connector provider states")
	due, err := repository.ListDueConnectorProviderStates(t.Context(), scope, 10, now.Format(time.RFC3339))
	if err != nil || len(due) != 2 || len(due[0].RelatedStates) != 2 {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	claimed, won, err := repository.ClaimConnectorProviderState(t.Context(), "workspace", "primary", "sync", "worker-a", now.Format(time.RFC3339), now.Add(time.Minute).Format(time.RFC3339))
	if err != nil || !won || claimed.FencingToken != 1 {
		t.Fatalf("claimed=%#v won=%v err=%v", claimed, won, err)
	}
	reclaimed, won, err := repository.ClaimConnectorProviderState(t.Context(), "workspace", "primary", "sync", "worker-b", now.Add(2*time.Minute).Format(time.RFC3339), now.Add(3*time.Minute).Format(time.RFC3339))
	if err != nil || !won || reclaimed.FencingToken != 2 {
		t.Fatalf("reclaimed=%#v won=%v err=%v", reclaimed, won, err)
	}
	if _, err := repository.CompleteConnectorProviderState(t.Context(), claimed, json.RawMessage(`{"cursor":"11"}`), now.Add(time.Minute).Format(time.RFC3339), now.Format(time.RFC3339)); err == nil {
		t.Fatal("stale lease completed provider state")
	}
	if _, err := repository.CompleteConnectorProviderState(t.Context(), reclaimed, json.RawMessage(`{"cursor":"12"}`), now.Add(time.Minute).Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if err := repository.SyncConnectorProviderTasks(t.Context(), connection, []integrationrepository.ConnectorProviderTask{{Key: "sync", StateVersion: 2}}, now.Format(time.RFC3339)); err == nil {
		t.Fatal("state version changed without an explicit migration")
	}
}

func TestLegacyGmailCursorMigratesIntoOpaqueProviderState(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	_, err := store.DB().ExecContext(t.Context(), `CREATE TABLE integration_gmail_sync_states (workspace_id TEXT, connection_key TEXT, account_email TEXT, history_id TEXT, status TEXT, next_poll_at TEXT, last_error_code TEXT, attempt_count INTEGER, fencing_token BIGINT, updated_at TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO integration_gmail_sync_states VALUES (?,?,?,?,?,?,?,?,?,?)`, "workspace", "gmail", "person@example.test", "123", "ready", "2026-08-24T00:00:00Z", "", 2, 7, "2026-08-23T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationConfigStore(store)
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test legacy connector provider migration")
	if err := repository.MigrateLegacyConnectorProviderStates(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	state, found, err := repository.getConnectorProviderState(t.Context(), "workspace", "gmail", "gmail_sync")
	if err != nil || !found || state.FencingToken != 7 || !strings.Contains(string(state.Payload), `"history_id":"123"`) {
		t.Fatalf("state=%+v found=%v err=%v", state, found, err)
	}
}
