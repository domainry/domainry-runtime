package appschema

import (
	"slices"
	"testing"

	"github.com/domainry/domainry-orm/dialect"
	ormschema "github.com/domainry/domainry-orm/schema"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	mysqlprofile "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema/mysql"
	postgresprofile "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema/postgres"
	sqliteprofile "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema/sqlite"
	appschemastorage "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema/storage"
)

func TestFreshBusinessTableUsesCanonicalColumnOrder(t *testing.T) {
	store := openStoreForMetadataTest(t)
	object := definitionmodel.ObjectSchema{Key: "ordered_customer", Fields: []definitionmodel.FieldSchema{
		{Key: "status", Type: "text"},
		{Key: "body", Type: "long_text"},
		{Key: "name", Type: "text"},
		{Key: "parent_id", Type: "text"},
		{Key: "disabled_field", Type: "text", DisabledAt: "2026-10-22"},
	}}
	if err := store.ensureObjectStorage(t.Context(), object); err != nil {
		t.Fatal(err)
	}
	rows, err := store.database().QueryContext(t.Context(), `PRAGMA table_info("ordered_customer")`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var ordinal, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&ordinal, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"id", "ordered_customer_id", "workspace_id", "parent_id", "owner_user_id", "owner_org_id", "name", "status", "deleted", "created_at", "updated_at", "create_by", "update_by", "body", "ext_info"}
	if !slices.Equal(names, want) {
		t.Fatalf("fresh business columns=%v want=%v", names, want)
	}
}

func TestTypedBusinessColumnsMatchStorageProfileTypes(t *testing.T) {
	profiles := map[string]appschemastorage.Profile{
		"sqlite":   sqliteprofile.NewApplicationSchemaStorageProfile(),
		"mysql":    mysqlprofile.NewApplicationSchemaStorageProfile(),
		"postgres": postgresprofile.NewApplicationSchemaStorageProfile(),
	}
	for driver, profile := range profiles {
		renderer, err := dialect.ParseRenderer(driver, "", "")
		if err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"text", "long_text", "integer", "datetime", "currency", "percent", "number", "boolean", "json", "file", "file_list", "multi_select", "relation"} {
			for _, indexed := range []bool{false, true} {
				field := definitionmodel.FieldSchema{Key: "value", Type: kind, Config: map[string]any{"max_length": 80}}
				physical, err := ormschema.NewTable(renderer, "typed_fields").Columns(ormschema.Column(field.Key, profile.FieldType(field, indexed))).PhysicalTable()
				if err != nil {
					t.Fatal(err)
				}
				if got, want := physical.Columns[0].Type, profile.FieldColumnType(field, indexed); got != want {
					t.Fatalf("%s %s indexed=%t type=%s want=%s", driver, kind, indexed, got, want)
				}
			}
		}
	}
}
