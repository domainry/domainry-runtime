package appschema

import (
	"testing"

	"github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestPublicResourceAccessKeyIsGloballyIndexedAndCollisionProof(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewApplicationSchemaStore(store)
	resource := definitionmodel.ObjectPublicResource{Key: "digital_business_card", AccessKeyField: "share_key", StateField: "status", ActiveState: "published", Fields: []string{"name"}}
	object := definitionmodel.ObjectSchema{Key: "card", Fields: []definitionmodel.FieldSchema{
		{Key: "share_key", Type: "text", Required: true, Unique: true},
		{Key: "status", Type: "text", Required: true},
		{Key: "name", Type: "text"},
	}, PublicResources: []definitionmodel.ObjectPublicResource{resource}}
	if err := repository.ensureObjectStorage(t.Context(), object, metadataUpgradeExecution{}); err != nil {
		t.Fatal(err)
	}
	indexName := repository.publicResourceAccessIndexName(object.Key, resource)
	indexes, err := repository.tableIndexes(t.Context(), object.Key)
	if err != nil || !indexes[indexName] {
		t.Fatalf("public access index %q missing: indexes=%#v err=%v", indexName, indexes, err)
	}
	insert := func(workspace, id string) error {
		statement, arguments, err := query.NewInsertBuilder(store.RuntimeRenderer(), object.Key).
			Columns("workspace_id", "id", "created_at", "updated_at", "share_key", "status", "name").
			Values(workspace, id, "2026-09-18T00:00:00Z", "2026-09-18T00:00:00Z", "abcdefghijklmnopqrstuvwx", "published", "Yuki").Build()
		if err != nil {
			return err
		}
		_, err = store.DB().ExecContext(t.Context(), statement, arguments...)
		return err
	}
	if err := insert("workspace-a", "card-a"); err != nil {
		t.Fatal(err)
	}
	if err := insert("workspace-b", "card-b"); err == nil {
		t.Fatal("global public access-key collision was accepted")
	}

	object.PublicResources = nil
	if err := repository.ensureObjectStorage(t.Context(), object, metadataUpgradeExecution{}); err != nil {
		t.Fatal(err)
	}
	indexes, err = repository.tableIndexes(t.Context(), object.Key)
	if err != nil || indexes[indexName] {
		t.Fatalf("stale public access index retained: indexes=%#v err=%v", indexes, err)
	}
}
