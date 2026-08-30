package appschema

import (
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"

	"context"
	"encoding/json"
	"errors"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"path/filepath"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func metadataTestInstallationScope() principalmodel.SystemScope {
	return principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test metadata repository")
}

func publishDefinitionWithoutAuditForTest(ctx context.Context, repository ApplicationSchemaStore, resourceType, resourceKey string, request appschemamodel.ApplicationDefinitionUpsertRequest) (appschemamodel.ApplicationDefinition, error) {
	return repository.publishDefinition(ctx, metadataTestInstallationScope(), resourceType, resourceKey, request, nil)
}

func openStoreForGeneratedListTest(t *testing.T) *database.RuntimeStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "metadata.db")})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestContextMetadataSnapshotRevisionChangesForSameRowContentUpdate(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	manifest := manifestmodel.ManifestSchema{TemplateID: "revision", Version: "1", Name: "Revision", Objects: []definitionmodel.ObjectSchema{{Key: "account", Name: "Account"}}}
	if err := NewApplicationSchemaStore(store).EnsureManifestMetadata(t.Context(), manifest); err != nil {
		t.Fatal(err)
	}
	repository := NewApplicationSchemaStore(store)
	if err := repository.refreshCatalogHash(t.Context()); err != nil {
		t.Fatal(err)
	}
	before, err := repository.SnapshotRevision(t.Context(), metadataTestInstallationScope())
	if err != nil {
		t.Fatal(err)
	}
	current, ok, err := repository.GetDefinition(t.Context(), metadataTestInstallationScope(), "object", "account")
	if err != nil || !ok {
		t.Fatalf("current=%#v ok=%v err=%v", current, ok, err)
	}
	updated, err := publishDefinitionWithoutAuditForTest(t.Context(), repository, "object", "account", appschemamodel.ApplicationDefinitionUpsertRequest{ExpectedSchemaHash: &current.SchemaHash, Payload: json.RawMessage(`{"key":"account","name":"Business Account"}`)})
	if err != nil || updated.SchemaHash == current.SchemaHash {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	after, err := repository.SnapshotRevision(t.Context(), metadataTestInstallationScope())
	if err != nil {
		t.Fatal(err)
	}
	if before == "" || after == "" || before == after {
		t.Fatalf("revision did not change: before=%q after=%q", before, after)
	}
}

func TestSnapshotRevisionReusesActionExecutionTransaction(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewApplicationSchemaStore(store)
	if err := repository.refreshCatalogHash(t.Context()); err != nil {
		t.Fatal(err)
	}
	expected, err := repository.SnapshotRevision(t.Context(), metadataTestInstallationScope())
	if err != nil {
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

	revision, err := repository.SnapshotRevision(ctx, metadataTestInstallationScope())
	if err != nil || revision != expected {
		t.Fatalf("Action transaction revision=%q expected=%q error=%v", revision, expected, err)
	}
}

func TestApplicationDefinitionPublishReplaysRevisionAndContentFingerprint(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewApplicationSchemaStore(store)
	for _, testCase := range []struct {
		resourceType string
		resourceKey  string
		first        json.RawMessage
		second       json.RawMessage
	}{
		{resourceType: "object", resourceKey: "account", first: json.RawMessage(`{"key":"account","name":"Account"}`), second: json.RawMessage(`{"key":"account","name":"Business Account"}`)},
		{resourceType: "automation_rule", resourceKey: "account.sync", first: json.RawMessage(`{"key":"account.sync","name":"Sync account","object_key":"account"}`), second: json.RawMessage(`{"key":"account.sync","name":"Sync business account","object_key":"account"}`)},
	} {
		t.Run(testCase.resourceType, func(t *testing.T) {
			expectAbsent := ""
			first, err := publishDefinitionWithoutAuditForTest(t.Context(), repository, testCase.resourceType, testCase.resourceKey, appschemamodel.ApplicationDefinitionUpsertRequest{ExpectedSchemaHash: &expectAbsent, Payload: testCase.first})
			if err != nil {
				t.Fatal(err)
			}
			replayedCreate, err := publishDefinitionWithoutAuditForTest(t.Context(), repository, testCase.resourceType, testCase.resourceKey, appschemamodel.ApplicationDefinitionUpsertRequest{ExpectedSchemaHash: &expectAbsent, Payload: testCase.first})
			if err != nil || replayedCreate.SchemaVersion != first.SchemaVersion || replayedCreate.SchemaHash != first.SchemaHash {
				t.Fatalf("create replay=%#v first=%#v err=%v", replayedCreate, first, err)
			}
			second, err := publishDefinitionWithoutAuditForTest(t.Context(), repository, testCase.resourceType, testCase.resourceKey, appschemamodel.ApplicationDefinitionUpsertRequest{ExpectedSchemaHash: &first.SchemaHash, Payload: testCase.second})
			if err != nil || second.SchemaVersion == first.SchemaVersion || second.SchemaHash == first.SchemaHash {
				t.Fatalf("second publication=%#v first=%#v err=%v", second, first, err)
			}
			replayedUpdate, err := publishDefinitionWithoutAuditForTest(t.Context(), repository, testCase.resourceType, testCase.resourceKey, appschemamodel.ApplicationDefinitionUpsertRequest{ExpectedSchemaHash: &first.SchemaHash, Payload: testCase.second})
			if err != nil || replayedUpdate.SchemaVersion != second.SchemaVersion || replayedUpdate.SchemaHash != second.SchemaHash {
				t.Fatalf("update replay=%#v second=%#v err=%v", replayedUpdate, second, err)
			}
			versions, err := repository.ListDefinitionVersions(t.Context(), metadataTestInstallationScope(), testCase.resourceType, testCase.resourceKey)
			if err != nil || len(versions) != 2 {
				t.Fatalf("versions=%#v err=%v", versions, err)
			}
			wrongRevision := "not-the-previous-revision"
			if _, err := publishDefinitionWithoutAuditForTest(t.Context(), repository, testCase.resourceType, testCase.resourceKey, appschemamodel.ApplicationDefinitionUpsertRequest{ExpectedSchemaHash: &wrongRevision, Payload: testCase.second}); err == nil {
				t.Fatal("same content with unrelated expected revision must conflict")
			}
		})
	}
}

var _ appschemarepository.ApplicationSchemaRepository = ApplicationSchemaStore{}

func TestApplicationSchemaStoreContractAndCancellation(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewApplicationSchemaStore(store)
	value, err := repository.UpsertLocalizedText(t.Context(), principalmodel.InstallationWorkspaceID, appschemamodel.LocalizedTextUpsertRequest{WorkspaceID: principalmodel.InstallationWorkspaceID, EntityType: "object", EntityKey: "customer", Property: "name", Locale: "en-US", Text: "Customer"})
	if err != nil {
		t.Fatalf("upsert localized text: %v", err)
	}
	values, err := repository.ListLocalizedTexts(t.Context(), principalmodel.InstallationWorkspaceID, appschemamodel.LocalizedTextQuery{EntityKey: "customer", Locale: "en-US"})
	if err != nil || len(values) != 1 || values[0].Text != value.Text {
		t.Fatalf("list localized texts=%#v err=%v", values, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.ListLocalizedTexts(cancelled, principalmodel.InstallationWorkspaceID, appschemamodel.LocalizedTextQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled list error=%v", err)
	}
	if _, err := repository.UpsertLocalizedText(cancelled, principalmodel.InstallationWorkspaceID, appschemamodel.LocalizedTextUpsertRequest{EntityType: "object", EntityKey: "never", Property: "name", Locale: "en-US", Text: "Never"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled upsert error=%v", err)
	}
	if err := repository.SyncManifest(cancelled, metadataTestInstallationScope(), manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "never_created"}}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled schema sync error=%v", err)
	}
}

func TestApplicationSchemaStoreWorkspaceIsolationContract(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewApplicationSchemaStore(store)

	for _, workspace := range []struct {
		id   string
		text string
	}{{id: "workspace-a", text: "Account A"}, {id: "workspace-b", text: "Account B"}} {
		if _, err := repository.UpsertLocalizedText(t.Context(), workspace.id, appschemamodel.LocalizedTextUpsertRequest{
			WorkspaceID: workspace.id, EntityType: "object", EntityKey: "account", Property: "name", Locale: "en-US", Text: workspace.text,
		}); err != nil {
			t.Fatalf("upsert %s: %v", workspace.id, err)
		}
	}
	for _, workspace := range []struct {
		id   string
		text string
	}{{id: "workspace-a", text: "Account A"}, {id: "workspace-b", text: "Account B"}} {
		values, err := repository.ListLocalizedTexts(t.Context(), workspace.id, appschemamodel.LocalizedTextQuery{WorkspaceID: workspace.id, EntityKey: "account", Locale: "en-US"})
		if err != nil || len(values) != 1 || values[0].WorkspaceID != workspace.id || values[0].Text != workspace.text {
			t.Fatalf("workspace %s values=%#v err=%v", workspace.id, values, err)
		}
	}
	if _, err := repository.ListLocalizedTexts(t.Context(), "", appschemamodel.LocalizedTextQuery{}); err == nil {
		t.Fatal("localized text list accepted a missing workspace")
	}
	if _, err := repository.UpsertLocalizedText(t.Context(), "", appschemamodel.LocalizedTextUpsertRequest{}); err == nil {
		t.Fatal("localized text upsert accepted a missing workspace")
	}
	if _, err := repository.ListLocalizedTexts(t.Context(), "workspace-a", appschemamodel.LocalizedTextQuery{WorkspaceID: "workspace-b"}); err == nil {
		t.Fatal("localized text list accepted mismatched workspace scopes")
	}
	if _, err := repository.UpsertLocalizedText(t.Context(), "workspace-a", appschemamodel.LocalizedTextUpsertRequest{WorkspaceID: "workspace-b"}); err == nil {
		t.Fatal("localized text upsert accepted mismatched workspace scopes")
	}

	zero := principalmodel.SystemScope{}
	installationCalls := []struct {
		name string
		call func() error
	}{
		{name: "snapshot revision", call: func() error { _, err := repository.SnapshotRevision(t.Context(), zero); return err }},
		{name: "load manifest", call: func() error { _, err := repository.LoadManifest(t.Context(), zero); return err }},
		{name: "sync manifest", call: func() error { return repository.SyncManifest(t.Context(), zero, manifestmodel.ManifestSchema{}) }},
		{name: "migration plan", call: func() error {
			_, err := repository.MigrationPlan(t.Context(), zero, manifestmodel.ManifestSchema{})
			return err
		}},
		{name: "publish definition", call: func() error {
			_, err := repository.PublishDefinition(t.Context(), zero, "object", "account", appschemamodel.ApplicationDefinitionUpsertRequest{}, auditmodel.AuditEvent{})
			return err
		}},
		{name: "complete refresh", call: func() error {
			return repository.CompleteDefinitionRefresh(t.Context(), zero, "object", "account", "hash", "")
		}},
		{name: "apply mutations", call: func() error {
			_, err := repository.ApplyDefinitionMutations(t.Context(), zero, nil, nil, &changeplanmodel.BusinessChangePlanPublication{})
			return err
		}},
		{name: "disable definition", call: func() error { return repository.DisableDefinition(t.Context(), zero, "object", "account") }},
		{name: "list definitions", call: func() error { _, err := repository.ListDefinitions(t.Context(), zero, "object"); return err }},
		{name: "get definition", call: func() error {
			_, _, err := repository.GetDefinition(t.Context(), zero, "object", "account")
			return err
		}},
		{name: "list definition versions", call: func() error {
			_, err := repository.ListDefinitionVersions(t.Context(), zero, "object", "account")
			return err
		}},
		{name: "rollback definition", call: func() error {
			_, err := repository.RollbackDefinition(t.Context(), zero, "object", "account", appschemamodel.ApplicationDefinitionRollbackRequest{}, auditmodel.AuditEvent{})
			return err
		}},
	}
	for _, testCase := range installationCalls {
		t.Run(testCase.name, func(t *testing.T) {
			if err := testCase.call(); err == nil {
				t.Fatal("installation operation accepted missing system scope")
			}
		})
	}
	if _, err := repository.ListDefinitions(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "wrong metadata scope"), "object"); err == nil {
		t.Fatal("installation metadata accepted runtime-global scope")
	}
}
