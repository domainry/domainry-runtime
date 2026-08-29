package schema

import (
	"context"
	"testing"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
)

type schemaHelperStore struct{}

func (schemaHelperStore) SchemaDB() SQLDatabase  { return nil }
func (schemaHelperStore) Driver() string         { return "sqlite" }
func (schemaHelperStore) DatabaseSchema() string { return "" }
func (schemaHelperStore) Identifier(value string) string {
	return `"` + value + `"`
}
func (schemaHelperStore) TableIdentifier(value string) string { return value }
func (schemaHelperStore) Placeholder(index int) string        { return "?" }
func (schemaHelperStore) CreateIndexIfMissing(context.Context, string, string, bool, ...string) error {
	return nil
}
func (schemaHelperStore) EnsureRuntimeColumn(context.Context, string, string, string) error {
	return nil
}
func (schemaHelperStore) RuntimeTableExists(context.Context, string) (bool, error) {
	return false, nil
}
func (schemaHelperStore) MetadataIDColumnType() string       { return "TEXT" }
func (schemaHelperStore) LocalizedTextKeyColumnType() string { return "TEXT" }
func (schemaHelperStore) RuntimeColumnDefinition(value string) string {
	return "normalized:" + value
}
func (schemaHelperStore) RuntimeProfile() persistencedriver.EngineProfile {
	return sqlite.NewEngine()
}
func (schemaHelperStore) RuntimeRenderer() ormdialect.Renderer {
	return sqlite.NewEngine().SQLDialect().WithSchema("")
}

func TestSchemaHelperValueShapes(t *testing.T) {
	for _, test := range []struct {
		input any
		want  string
	}{
		{input: nil, want: ""},
		{input: []byte("x"), want: "x"},
		{input: "value", want: "value"},
		{input: 42, want: "42"},
	} {
		if got := idempotencyMigrationString(test.input); got != test.want {
			t.Fatalf("migration string(%#v)=%q want %q", test.input, got, test.want)
		}
	}
	if !idempotencyMigrationStringsEqual(nil, nil) || !idempotencyMigrationStringsEqual([]string{"a", "b"}, []string{"a", "b"}) {
		t.Fatal("equal migration strings rejected")
	}
	if idempotencyMigrationStringsEqual([]string{"a"}, []string{"a", "b"}) || idempotencyMigrationStringsEqual([]string{"a"}, []string{"b"}) {
		t.Fatal("different migration strings accepted")
	}
	if !idempotencyMigrationContains([]string{"a", "b"}, "b") || idempotencyMigrationContains([]string{"a"}, "b") {
		t.Fatal("migration contains mismatch")
	}
	if got := quotedColumnDefinitions(schemaHelperStore{}, []string{"id", "name TEXT NOT NULL"}); got != `"id", "name" normalized:TEXT NOT NULL` {
		t.Fatalf("quoted definitions=%q", got)
	}
	if err := prepareIdempotencyReceiptMigrations(t.Context(), schemaHelperStore{}); err != nil {
		t.Fatalf("empty migration specs=%v", err)
	}
}

var _ Store = schemaHelperStore{}
