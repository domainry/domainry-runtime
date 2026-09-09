package appschema

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestEnsureObjectStorageReplacesLegacyRecordActorColumns(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.DB().Exec(`CREATE TABLE actor_migration (
		workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
		deleted BOOLEAN NOT NULL DEFAULT FALSE, ext_info TEXT NOT NULL DEFAULT '{}',
		create_user_id TEXT, update_user_id TEXT, name TEXT, UNIQUE (workspace_id, id)
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO actor_migration
		(workspace_id, id, created_at, updated_at, create_user_id, update_user_id, name)
		VALUES ('workspace', 'record', 'created', 'updated', 'creator', 'updater', 'name')`); err != nil {
		t.Fatal(err)
	}
	repository := NewApplicationSchemaStore(store)
	if err := repository.ensureObjectStorage(t.Context(), definitionmodel.ObjectSchema{Key: "actor_migration", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}, metadataUpgradeExecution{}); err != nil {
		t.Fatal(err)
	}
	columns, err := repository.tableColumns(t.Context(), "actor_migration")
	if err != nil {
		t.Fatal(err)
	}
	if !columns["create_by"] || !columns["update_by"] || columns["create_user_id"] || columns["update_user_id"] {
		t.Fatalf("actor columns=%v", columns)
	}
	var createBy, updateBy string
	if err := store.DB().QueryRow(`SELECT create_by, update_by FROM actor_migration WHERE id = 'record'`).Scan(&createBy, &updateBy); err != nil {
		t.Fatal(err)
	}
	if createBy != "creator" || updateBy != "updater" {
		t.Fatalf("actor values=(%q,%q)", createBy, updateBy)
	}
}
