package integration

import (
	"database/sql/driver"
	"errors"
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func scriptedIntegrationConfig(t *testing.T, state *integrationSQLState) IntegrationConfigStore {
	t.Helper()
	store := openStoreForGeneratedListTest(t)
	db := openIntegrationScriptedDB(state)
	t.Cleanup(func() {
		_ = db.Close()
		_ = store.Close()
	})
	repository := NewIntegrationConfigStore(store)
	repository.db = db
	return repository
}

func TestIntegrationConfigListSQLFailures(t *testing.T) {
	wantErr := errors.New("list failure")
	calls := []func(IntegrationConfigStore) error{
		func(r IntegrationConfigStore) error { _, err := r.ListSecrets(t.Context(), "default"); return err },
		func(r IntegrationConfigStore) error { _, err := r.ListConnections(t.Context(), "default"); return err },
		func(r IntegrationConfigStore) error {
			_, err := r.ListExternalIdentities(t.Context(), "default")
			return err
		},
		func(r IntegrationConfigStore) error {
			_, err := r.ListWebhookSubscriptions(t.Context(), "default", "", "", "", 1)
			return err
		},
		func(r IntegrationConfigStore) error { _, err := r.ListAPIKeys(t.Context(), "default"); return err },
	}
	for index, call := range calls {
		for _, step := range []integrationSQLQueryStep{{err: wantErr}, {columns: []string{"bad"}, rows: [][]driver.Value{{"bad"}}}, {columns: []string{"bad"}, nextErr: wantErr}} {
			repository := scriptedIntegrationConfig(t, &integrationSQLState{querySteps: []integrationSQLQueryStep{step}})
			if err := call(repository); err == nil {
				t.Fatalf("list call %d ignored failure %+v", index, step)
			}
		}
	}
}

func TestIntegrationConfigReplaceRowFailureStages(t *testing.T) {
	wantErr := errors.New("replace failure")
	states := []*integrationSQLState{
		{beginErr: wantErr},
		{execSteps: []integrationSQLExecStep{{err: wantErr}}},
		{execSteps: []integrationSQLExecStep{{rows: 1}, {err: wantErr}}},
		{execSteps: []integrationSQLExecStep{{rows: 1}, {rows: 1}}, commitErr: wantErr},
	}
	for index, state := range states {
		repository := scriptedIntegrationConfig(t, state)
		if err := repository.replaceRow(t.Context(), "_integration_secrets", "secret_key", "default", "token", []string{"id"}, []any{"id"}, "integration secret"); err == nil {
			t.Fatalf("replace stage %d succeeded", index)
		}
	}
	repository := scriptedIntegrationConfig(t, &integrationSQLState{execSteps: []integrationSQLExecStep{{rows: 1}, {rows: 1}}, rollbackErr: wantErr})
	if err := repository.replaceRow(t.Context(), "_integration_secrets", "secret_key", "default", "token", []string{"id"}, []any{"id"}, "integration secret"); err != nil {
		t.Fatalf("successful replace: %v", err)
	}
}

func TestIntegrationConfigActionTransactionEdges(t *testing.T) {
	wantErr := errors.New("action transaction failure")
	repository := scriptedIntegrationConfig(t, &integrationSQLState{querySteps: []integrationSQLQueryStep{{err: wantErr}}})
	tx, err := repository.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := database.WithActionExecutionTransaction(t.Context(), tx)
	if _, err := repository.ListSecrets(ctx, "default"); err == nil {
		t.Fatal("action transaction list error was ignored")
	}
	_ = tx.Rollback()

	for index, steps := range [][]integrationSQLExecStep{
		{{err: wantErr}},
		{{rows: 1}, {err: wantErr}},
	} {
		repository = scriptedIntegrationConfig(t, &integrationSQLState{execSteps: steps})
		tx, err = repository.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx = database.WithActionExecutionTransaction(t.Context(), tx)
		if err := repository.replaceRow(ctx, "_integration_secrets", "secret_key", "default", "token", []string{"id"}, []any{"id"}, "integration secret"); err == nil {
			t.Fatalf("action transaction replace stage %d succeeded", index)
		}
		_ = tx.Rollback()
	}
}

func TestIntegrationConfigUpsertReadAndWriteFailures(t *testing.T) {
	wantErr := errors.New("upsert failure")
	calls := []func(IntegrationConfigStore) error{
		func(r IntegrationConfigStore) error {
			_, err := r.UpsertSecret(t.Context(), "default", integrationmodel.IntegrationSecret{Key: "token"})
			return err
		},
		func(r IntegrationConfigStore) error {
			_, err := r.UpsertConnection(t.Context(), "default", integrationmodel.IntegrationConnection{Key: "connection"})
			return err
		},
		func(r IntegrationConfigStore) error {
			_, err := r.UpsertExternalIdentity(t.Context(), "default", integrationmodel.IntegrationExternalIdentity{Key: "identity"})
			return err
		},
		func(r IntegrationConfigStore) error {
			_, err := r.UpsertWebhookSubscription(t.Context(), "default", integrationmodel.IntegrationWebhookSubscription{Key: "subscription"})
			return err
		},
		func(r IntegrationConfigStore) error {
			_, err := r.UpsertAPIKey(t.Context(), "default", integrationmodel.IntegrationAPIKey{Key: "key"})
			return err
		},
	}
	for index, call := range calls {
		repository := scriptedIntegrationConfig(t, &integrationSQLState{querySteps: []integrationSQLQueryStep{{err: wantErr}}})
		if err := call(repository); err == nil {
			t.Fatalf("upsert call %d ignored metadata read failure", index)
		}
		repository = scriptedIntegrationConfig(t, &integrationSQLState{querySteps: []integrationSQLQueryStep{{}}, beginErr: wantErr})
		if err := call(repository); err == nil {
			t.Fatalf("upsert call %d ignored write failure", index)
		}
	}
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	repository := scriptedIntegrationConfig(t, &integrationSQLState{querySteps: []integrationSQLQueryStep{{}}})
	if _, err := repository.UpsertConnection(t.Context(), "default", integrationmodel.IntegrationConnection{Key: "connection", Config: cyclic}); err == nil {
		t.Fatal("cyclic connection config encoded")
	}
}

func TestIntegrationConfigMutationSQLFailures(t *testing.T) {
	wantErr := errors.New("mutation failure")
	repository := scriptedIntegrationConfig(t, &integrationSQLState{execSteps: []integrationSQLExecStep{{err: wantErr}}})
	if _, err := repository.UpdateAPIKeyLastUsed(t.Context(), "default", "key", "now"); err == nil {
		t.Fatal("api key update failure ignored")
	}
	repository = scriptedIntegrationConfig(t, &integrationSQLState{execSteps: []integrationSQLExecStep{{rows: 1}}, querySteps: []integrationSQLQueryStep{{err: wantErr}}})
	if _, err := repository.UpdateAPIKeyLastUsed(t.Context(), "default", "key", "now"); err == nil {
		t.Fatal("api key reload failure ignored")
	}
	repository = scriptedIntegrationConfig(t, &integrationSQLState{execSteps: []integrationSQLExecStep{{err: wantErr}}})
	if _, err := repository.DeleteConnection(t.Context(), "default", "connection"); err == nil {
		t.Fatal("connection delete failure ignored")
	}
	repository = scriptedIntegrationConfig(t, &integrationSQLState{execSteps: []integrationSQLExecStep{{rows: 1, rowsErr: wantErr}}})
	if _, err := repository.DeleteConnection(t.Context(), "default", "connection"); err == nil {
		t.Fatal("connection affected-row failure ignored")
	}
}

func TestIntegrationSecretAndCredentialLeaseSQLFailures(t *testing.T) {
	wantErr := errors.New("secret and lease SQL failure")
	repository := scriptedIntegrationConfig(t, &integrationSQLState{querySteps: []integrationSQLQueryStep{{err: wantErr}}})
	if err := repository.PutSecretMaterial(t.Context(), "default", "token", "value"); err == nil {
		t.Fatal("secret metadata read failure ignored")
	}
	repository = scriptedIntegrationConfig(t, &integrationSQLState{querySteps: []integrationSQLQueryStep{{}}, beginErr: wantErr})
	if err := repository.PutSecretMaterial(t.Context(), "default", "token", "value"); err == nil {
		t.Fatal("secret write failure ignored")
	}

	validLease := func(r IntegrationConfigStore) (bool, error) {
		return r.TryAcquireCredentialRefreshLease(t.Context(), "default", "connection", "owner", "2026-07-20T00:00:00Z", "2026-07-20T00:01:00Z")
	}
	for _, state := range []*integrationSQLState{
		{execSteps: []integrationSQLExecStep{{err: wantErr}}},
		{execSteps: []integrationSQLExecStep{{rowsErr: wantErr}}},
		{execSteps: []integrationSQLExecStep{{rows: 0}, {err: wantErr}}, querySteps: []integrationSQLQueryStep{{}}},
	} {
		repository = scriptedIntegrationConfig(t, state)
		if _, err := validLease(repository); err == nil {
			t.Fatal("credential lease failure ignored")
		}
	}
	repository = scriptedIntegrationConfig(t, &integrationSQLState{
		execSteps:  []integrationSQLExecStep{{rows: 0}, {err: wantErr}},
		querySteps: []integrationSQLQueryStep{{columns: []string{"lease_owner"}, rows: [][]driver.Value{{"other"}}}},
	})
	if acquired, err := validLease(repository); err != nil || acquired {
		t.Fatalf("existing lease acquired=%v err=%v", acquired, err)
	}
	repository = scriptedIntegrationConfig(t, &integrationSQLState{execSteps: []integrationSQLExecStep{{err: wantErr}}})
	if err := repository.ReleaseCredentialRefreshLease(t.Context(), "default", "connection", "owner"); err == nil {
		t.Fatal("release lease failure ignored")
	}
}
