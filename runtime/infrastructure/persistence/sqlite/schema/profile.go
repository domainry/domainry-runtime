package schema

import (
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) ManagedDatabaseMarkerEnabled() bool        { return false }
func (Profile) ColumnDefinition(definition string) string { return strings.TrimSpace(definition) }
func (Profile) ApplicationTablesQuery(ormdialect.Renderer, string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'"}
}
func (Profile) WorkspaceTablesQuery(ormdialect.Renderer, string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT DISTINCT m.name FROM sqlite_master m JOIN pragma_table_info(m.name) p WHERE m.type = 'table' AND p.name = 'workspace_id' ORDER BY m.name"}
}
func (Profile) TableExistsQuery(renderer ormdialect.Renderer, _ string, table string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = " + renderer.Placeholder(1), Arguments: []any{table}}
}
func (Profile) IndexesQuery(renderer ormdialect.Renderer, _ string, table string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = " + renderer.Placeholder(1), Arguments: []any{table}}
}
