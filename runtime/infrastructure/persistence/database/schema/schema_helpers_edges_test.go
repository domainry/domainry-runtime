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
func (schemaHelperStore) RuntimeTableExists(context.Context, string) (bool, error) {
	return false, nil
}
func (schemaHelperStore) ApplicationSchemaIDColumnType() string { return "TEXT" }
func (schemaHelperStore) LocalizedTextKeyColumnType() string    { return "TEXT" }
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
	if got := quotedColumnDefinitions(schemaHelperStore{}, []string{"id", "name TEXT NOT NULL", "PRIMARY KEY (workspace_id, id)"}); got != `"id", "name" normalized:TEXT NOT NULL, PRIMARY KEY ("workspace_id", "id")` {
		t.Fatalf("quoted definitions=%q", got)
	}
}

var _ Store = schemaHelperStore{}
