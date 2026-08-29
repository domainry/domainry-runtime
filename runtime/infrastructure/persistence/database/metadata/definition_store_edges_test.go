package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func newLegacyDefinitionStore(t *testing.T) MetadataStore {
	t.Helper()
	store := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	return NewMetadataStore(store)
}

func TestLegacyMetadataDefinitionCompatibilityLifecycle(t *testing.T) {
	repository := newLegacyDefinitionStore(t)
	if _, err := repository.UpsertMetadataDefinition(t.Context(), "unknown", "key", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("unsupported resource type was accepted")
	}
	if _, err := repository.UpsertMetadataDefinition(t.Context(), "object", "key", metadatamodel.MetadataDefinitionUpsertRequest{}); err == nil || !strings.Contains(err.Error(), "payload is required") {
		t.Fatalf("empty payload error=%v", err)
	}
	if _, err := repository.UpsertMetadataDefinition(t.Context(), "object", "key", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("malformed payload was accepted")
	}

	expectAbsent := ""
	account, err := repository.UpsertMetadataDefinition(t.Context(), "object", " account ", metadatamodel.MetadataDefinitionUpsertRequest{
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
	replayed, err := repository.UpsertMetadataDefinition(t.Context(), "object", "account", metadatamodel.MetadataDefinitionUpsertRequest{
		ExpectedSchemaHash: &expectAbsent,
		Payload:            json.RawMessage(`{"key":"ignored","name":"Account"}`),
		SourceKind:         "builder",
		SourceID:           "workspace-a",
	})
	if err != nil || replayed.SchemaHash != account.SchemaHash || replayed.SchemaVersion != account.SchemaVersion {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	contact, err := repository.UpsertMetadataDefinition(t.Context(), "object", "contact", metadatamodel.MetadataDefinitionUpsertRequest{
		Payload: json.RawMessage(`{"key":"contact","name":"Contact"}`),
	})
	if err != nil || contact.SourceKind != "user" || contact.SourceID != "metadata_api" {
		t.Fatalf("contact=%+v err=%v", contact, err)
	}

	all, err := repository.ListMetadataDefinitions(t.Context(), "object", "")
	if err != nil || len(all) != 2 || all[0].ResourceKey != "account" || all[1].ResourceKey != "contact" {
		t.Fatalf("all=%+v err=%v", all, err)
	}
	workspace, err := repository.ListMetadataDefinitions(t.Context(), "object", " workspace-a ")
	if err != nil || len(workspace) != 1 || workspace[0].ResourceKey != "account" || workspace[0].ResourceType != "object" {
		t.Fatalf("workspace=%+v err=%v", workspace, err)
	}
	if _, err := repository.ListMetadataDefinitions(t.Context(), "unknown", ""); err == nil {
		t.Fatal("list accepted unsupported type")
	}

	loaded, found, err := repository.GetMetadataDefinition(t.Context(), "object", "account")
	if err != nil || !found || loaded.SchemaHash != account.SchemaHash || string(loaded.Payload) == "" {
		t.Fatalf("loaded=%+v found=%v err=%v", loaded, found, err)
	}
	if _, found, err := repository.GetMetadataDefinition(t.Context(), "object", "missing"); err != nil || found {
		t.Fatalf("missing found=%v err=%v", found, err)
	}
	if _, _, err := repository.GetMetadataDefinition(t.Context(), "unknown", "missing"); err == nil {
		t.Fatal("get accepted unsupported type")
	}
	versions, err := repository.ListMetadataDefinitionVersions(t.Context(), "object", "account")
	if err != nil || len(versions) != 1 || versions[0].ResourceKey != "account" || versions[0].SchemaHash != account.SchemaHash {
		t.Fatalf("versions=%+v err=%v", versions, err)
	}
	if empty, err := repository.ListMetadataDefinitionVersions(t.Context(), "object", "missing"); err != nil || len(empty) != 0 {
		t.Fatalf("empty versions=%+v err=%v", empty, err)
	}

	if err := repository.DisableMetadataDefinition(t.Context(), "object", "contact"); err != nil {
		t.Fatal(err)
	}
	disabled, found, err := repository.GetMetadataDefinition(t.Context(), "object", "contact")
	if err != nil || !found || disabled.DisabledAt == "" {
		t.Fatalf("disabled=%+v found=%v err=%v", disabled, found, err)
	}
	active, err := repository.ListMetadataDefinitions(t.Context(), "object", "")
	if err != nil || len(active) != 1 || active[0].ResourceKey != "account" {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	if err := repository.DisableMetadataDefinition(t.Context(), "object", "missing"); err == nil || !strings.Contains(err.Error(), "notFound") {
		t.Fatalf("missing disable error=%v", err)
	}
	if err := repository.DisableMetadataDefinition(t.Context(), "unknown", "missing"); err == nil {
		t.Fatal("disable accepted unsupported type")
	}
}

func TestLegacyMetadataDefinitionCompatibilityPropagatesDatabaseFailures(t *testing.T) {
	t.Run("cancelled", func(t *testing.T) {
		repository := newLegacyDefinitionStore(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		calls := []func() error{
			func() error {
				_, err := repository.UpsertMetadataDefinition(ctx, "object", "account", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"account","name":"Account"}`)})
				return err
			},
			func() error { return repository.DisableMetadataDefinition(ctx, "object", "account") },
			func() error { _, _, err := repository.GetMetadataDefinition(ctx, "object", "account"); return err },
			func() error {
				_, err := repository.ListMetadataDefinitionVersions(ctx, "object", "account")
				return err
			},
			func() error {
				_, err := repository.RollbackMetadataDefinition(ctx, "object", "account", metadatamodel.MetadataDefinitionRollbackRequest{}, auditmodel.AuditEvent{})
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
		if _, err := repository.ListMetadataDefinitions(t.Context(), "object", ""); err == nil || !strings.Contains(err.Error(), "list object definitions") {
			t.Fatalf("list error=%v", err)
		}
		if _, _, err := repository.GetMetadataDefinition(t.Context(), "object", "account"); err == nil || !strings.Contains(err.Error(), "get object definition") {
			t.Fatalf("get error=%v", err)
		}
		if err := repository.DisableMetadataDefinition(t.Context(), "object", "account"); err == nil || !strings.Contains(err.Error(), "disable object") {
			t.Fatalf("disable error=%v", err)
		}
		if _, err := repository.UpsertMetadataDefinition(t.Context(), "object", "account", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"account","name":"Account"}`)}); err == nil {
			t.Fatal("upsert succeeded without active table")
		}
	})

	t.Run("missing version table", func(t *testing.T) {
		repository := newLegacyDefinitionStore(t)
		if _, err := repository.store.DB().ExecContext(t.Context(), `DROP TABLE metadata_definition_versions`); err != nil {
			t.Fatal(err)
		}
		if _, err := repository.ListMetadataDefinitionVersions(t.Context(), "object", "account"); err == nil || !strings.Contains(err.Error(), "list versions") {
			t.Fatalf("list versions error=%v", err)
		}
		if _, err := repository.UpsertMetadataDefinition(t.Context(), "object", "account", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"account","name":"Account"}`)}); err == nil {
			t.Fatal("upsert succeeded without version table")
		}
	})
}
