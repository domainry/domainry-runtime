package appschema

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func newLegacyDefinitionStore(t *testing.T) ApplicationSchemaStore {
	t.Helper()
	store := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	return NewApplicationSchemaStore(store)
}

func TestLegacyApplicationDefinitionCompatibilityLifecycle(t *testing.T) {
	repository := newLegacyDefinitionStore(t)
	if _, err := repository.UpsertApplicationDefinition(t.Context(), "unknown", "key", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("unsupported resource type was accepted")
	}
	if _, err := repository.UpsertApplicationDefinition(t.Context(), "object", "key", appschemamodel.ApplicationDefinitionUpsertRequest{}); err == nil || !strings.Contains(err.Error(), "payload is required") {
		t.Fatalf("empty payload error=%v", err)
	}
	if _, err := repository.UpsertApplicationDefinition(t.Context(), "object", "key", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("malformed payload was accepted")
	}

	expectAbsent := ""
	account, err := repository.UpsertApplicationDefinition(t.Context(), "object", " account ", appschemamodel.ApplicationDefinitionUpsertRequest{
		ExpectedSchemaHash: &expectAbsent,
		Payload:            json.RawMessage(`{"key":"ignored","name":"Account"}`),
		SourceKind:         " builder ",
		SourceID:           " workspace-a ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if account.ResourceKey != "account" || account.Name != "Account" || account.SourceKind != "builder" || account.SourceID != "workspace-a" || account.SchemaVersion != "1" {
		t.Fatalf("account=%+v", account)
	}
	replayed, err := repository.UpsertApplicationDefinition(t.Context(), "object", "account", appschemamodel.ApplicationDefinitionUpsertRequest{
		ExpectedSchemaHash: &expectAbsent,
		Payload:            json.RawMessage(`{"key":"ignored","name":"Account"}`),
		SourceKind:         "builder",
		SourceID:           "workspace-a",
	})
	if err != nil || replayed.SchemaHash != account.SchemaHash || replayed.SchemaVersion != account.SchemaVersion {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	contact, err := repository.UpsertApplicationDefinition(t.Context(), "object", "contact", appschemamodel.ApplicationDefinitionUpsertRequest{
		Payload: json.RawMessage(`{"key":"contact","name":"Contact"}`),
	})
	if err != nil || contact.SourceKind != "user" || contact.SourceID != "metadata_api" {
		t.Fatalf("contact=%+v err=%v", contact, err)
	}

	all, err := repository.ListApplicationDefinitions(t.Context(), "object", "")
	if err != nil || len(all) != 2 || all[0].ResourceKey != "account" || all[1].ResourceKey != "contact" {
		t.Fatalf("all=%+v err=%v", all, err)
	}
	workspace, err := repository.ListApplicationDefinitions(t.Context(), "object", " workspace-a ")
	if err != nil || len(workspace) != 1 || workspace[0].ResourceKey != "account" || workspace[0].ResourceType != "object" {
		t.Fatalf("workspace=%+v err=%v", workspace, err)
	}
	if _, err := repository.ListApplicationDefinitions(t.Context(), "unknown", ""); err == nil {
		t.Fatal("list accepted unsupported type")
	}

	loaded, found, err := repository.GetApplicationDefinition(t.Context(), "object", "account")
	if err != nil || !found || loaded.SchemaHash != account.SchemaHash || string(loaded.Payload) == "" {
		t.Fatalf("loaded=%+v found=%v err=%v", loaded, found, err)
	}
	if _, found, err := repository.GetApplicationDefinition(t.Context(), "object", "missing"); err != nil || found {
		t.Fatalf("missing found=%v err=%v", found, err)
	}
	if _, _, err := repository.GetApplicationDefinition(t.Context(), "unknown", "missing"); err == nil {
		t.Fatal("get accepted unsupported type")
	}
	versions, err := repository.ListApplicationDefinitionVersions(t.Context(), "object", "account")
	if err != nil || len(versions) != 1 || versions[0].ResourceKey != "account" || versions[0].SchemaHash != account.SchemaHash {
		t.Fatalf("versions=%+v err=%v", versions, err)
	}
	if empty, err := repository.ListApplicationDefinitionVersions(t.Context(), "object", "missing"); err != nil || len(empty) != 0 {
		t.Fatalf("empty versions=%+v err=%v", empty, err)
	}

	if err := repository.DisableApplicationDefinition(t.Context(), "object", "contact"); err != nil {
		t.Fatal(err)
	}
	disabled, found, err := repository.GetApplicationDefinition(t.Context(), "object", "contact")
	if err != nil || !found || disabled.DisabledAt == "" {
		t.Fatalf("disabled=%+v found=%v err=%v", disabled, found, err)
	}
	active, err := repository.ListApplicationDefinitions(t.Context(), "object", "")
	if err != nil || len(active) != 1 || active[0].ResourceKey != "account" {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	if err := repository.DisableApplicationDefinition(t.Context(), "object", "missing"); err == nil || !strings.Contains(err.Error(), "notFound") {
		t.Fatalf("missing disable error=%v", err)
	}
	if err := repository.DisableApplicationDefinition(t.Context(), "unknown", "missing"); err == nil {
		t.Fatal("disable accepted unsupported type")
	}
}

func TestLegacyApplicationDefinitionCompatibilityPropagatesDatabaseFailures(t *testing.T) {
	t.Run("cancelled", func(t *testing.T) {
		repository := newLegacyDefinitionStore(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		calls := []func() error{
			func() error {
				_, err := repository.UpsertApplicationDefinition(ctx, "object", "account", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"account","name":"Account"}`)})
				return err
			},
			func() error { return repository.DisableApplicationDefinition(ctx, "object", "account") },
			func() error { _, _, err := repository.GetApplicationDefinition(ctx, "object", "account"); return err },
			func() error {
				_, err := repository.ListApplicationDefinitionVersions(ctx, "object", "account")
				return err
			},
			func() error {
				_, err := repository.RollbackApplicationDefinition(ctx, "object", "account", appschemamodel.ApplicationDefinitionRollbackRequest{}, auditmodel.AuditEvent{})
				return err
			},
		}
		for index, call := range calls {
			if err := call(); !errors.Is(err, context.Canceled) {
				t.Fatalf("call %d error=%v", index, err)
			}
		}
	})

	t.Run("missing active table", func(t *testing.T) {
		repository := newLegacyDefinitionStore(t)
		if _, err := repository.store.DB().ExecContext(t.Context(), `DROP TABLE object_definitions`); err != nil {
			t.Fatal(err)
		}
		if _, err := repository.ListApplicationDefinitions(t.Context(), "object", ""); err == nil || !strings.Contains(err.Error(), "list object definitions") {
			t.Fatalf("list error=%v", err)
		}
		if _, _, err := repository.GetApplicationDefinition(t.Context(), "object", "account"); err == nil || !strings.Contains(err.Error(), "get object definition") {
			t.Fatalf("get error=%v", err)
		}
		if err := repository.DisableApplicationDefinition(t.Context(), "object", "account"); err == nil || !strings.Contains(err.Error(), "disable object") {
			t.Fatalf("disable error=%v", err)
		}
		if _, err := repository.UpsertApplicationDefinition(t.Context(), "object", "account", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"account","name":"Account"}`)}); err == nil {
			t.Fatal("upsert succeeded without active table")
		}
	})

	t.Run("missing version table", func(t *testing.T) {
		repository := newLegacyDefinitionStore(t)
		if _, err := repository.store.DB().ExecContext(t.Context(), `DROP TABLE metadata_definition_versions`); err != nil {
			t.Fatal(err)
		}
		if _, err := repository.ListApplicationDefinitionVersions(t.Context(), "object", "account"); err == nil || !strings.Contains(err.Error(), "list versions") {
			t.Fatalf("list versions error=%v", err)
		}
		if _, err := repository.UpsertApplicationDefinition(t.Context(), "object", "account", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"account","name":"Account"}`)}); err == nil {
			t.Fatal("upsert succeeded without version table")
		}
	})
}
