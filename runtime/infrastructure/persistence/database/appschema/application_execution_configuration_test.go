package appschema

import (
	"testing"

	"github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func TestActionConfigurationUsesTheHostTransaction(t *testing.T) {
	store := openStoreForMetadataTest(t)
	if err := store.SyncManifestProjection(t.Context(), metadataTestInstallationScope(), manifestmodel.ManifestSchema{TemplateID: "timezone", Version: "1", TimeZone: "Asia/Tokyo", Objects: []definitionmodel.ObjectSchema{{Key: "sale", Name: "Sale"}}}); err != nil {
		t.Fatal(err)
	}
	before, err := store.ExecutionConfiguration(t.Context(), metadataTestInstallationScope())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.raw.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	statement, args, err := query.NewUpdateBuilder(store.raw.RuntimeRenderer(), "_application_schema_projection").
		Set("source_hash", "next-source").Set("schema_hash", "next-schema").Set("time_zone", "America/New_York").
		Where(query.Equal("id", "current")).Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatal(err)
	}
	ctx := database.WithActionExecutionTransaction(t.Context(), tx)
	current, err := store.ExecutionConfiguration(ctx, metadataTestInstallationScope())
	if err != nil || current.SchemaRevision != "next-source:next-schema" || current.TimeZone != "America/New_York" {
		t.Fatalf("configuration escaped Action transaction: %+v error=%v", current, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	after, err := store.ExecutionConfiguration(t.Context(), metadataTestInstallationScope())
	if err != nil || after != before {
		t.Fatalf("rolled-back configuration escaped: before=%+v after=%+v error=%v", before, after, err)
	}
}

func TestActionConfigurationRequiresInstallationScopeAndPersistedHeader(t *testing.T) {
	store := openStoreForMetadataTest(t)
	if _, err := store.ExecutionConfiguration(t.Context(), principalmodel.SystemScope{}); err == nil {
		t.Fatal("unscoped configuration read accepted")
	}
	if _, err := store.ExecutionConfiguration(t.Context(), metadataTestInstallationScope()); err == nil {
		t.Fatal("missing application header silently initialized")
	}
}
