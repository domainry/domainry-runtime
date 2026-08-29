package schema

import (
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) ManagedDatabaseMarkerEnabled() bool { return true }
func (Profile) ColumnDefinition(definition string) string {
	definition = strings.TrimSpace(definition)
	definition = strings.ReplaceAll(definition, "TEXT NOT NULL DEFAULT '[]'", "TEXT NOT NULL DEFAULT ('[]')")
	definition = strings.ReplaceAll(definition, "TEXT NOT NULL DEFAULT '{}'", "TEXT NOT NULL DEFAULT ('{}')")
	definition = strings.ReplaceAll(definition, "TEXT NOT NULL DEFAULT ''", "TEXT NOT NULL DEFAULT ('')")
	return definition
}
func (Profile) ApplicationTablesQuery(ormdialect.Renderer, string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()"}
}
func (Profile) WorkspaceTablesQuery(renderer ormdialect.Renderer, databaseSchema string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT DISTINCT table_name FROM information_schema.columns WHERE table_schema = " + renderer.Placeholder(1) + " AND column_name = 'workspace_id' ORDER BY table_name", Arguments: []any{databaseSchema}}
}
func (Profile) TableExistsQuery(renderer ormdialect.Renderer, _ string, table string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = " + renderer.Placeholder(1), Arguments: []any{table}}
}
func (Profile) IndexesQuery(renderer ormdialect.Renderer, _ string, table string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT DISTINCT INDEX_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = " + renderer.Placeholder(1), Arguments: []any{table}}
}
