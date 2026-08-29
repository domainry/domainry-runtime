package schema

import (
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) ManagedDatabaseMarkerEnabled() bool        { return true }
func (Profile) ColumnDefinition(definition string) string { return strings.TrimSpace(definition) }
func (Profile) ApplicationTablesQuery(renderer ormdialect.Renderer, databaseSchema string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT table_name FROM information_schema.tables WHERE table_schema = " + renderer.Placeholder(1), Arguments: []any{databaseSchema}}
}
func (Profile) WorkspaceTablesQuery(renderer ormdialect.Renderer, databaseSchema string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT DISTINCT table_name FROM information_schema.columns WHERE table_schema = " + renderer.Placeholder(1) + " AND column_name = 'workspace_id' ORDER BY table_name", Arguments: []any{databaseSchema}}
}
func (Profile) TableExistsQuery(renderer ormdialect.Renderer, databaseSchema, table string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = " + renderer.Placeholder(1) + " AND table_name = " + renderer.Placeholder(2), Arguments: []any{databaseSchema, table}}
}
func (Profile) IndexesQuery(renderer ormdialect.Renderer, databaseSchema, table string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT indexname FROM pg_indexes WHERE schemaname = " + renderer.Placeholder(1) + " AND tablename = " + renderer.Placeholder(2), Arguments: []any{databaseSchema, table}}
}
