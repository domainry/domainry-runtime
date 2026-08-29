package sqlite

import (
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormdriver "github.com/domainry/domainry-orm/driver"
	ormsqlite "github.com/domainry/domainry-orm/sqlite"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

type engineProfile struct{ ormdriver.Profile }

func newEngineProfile() engineProfile { return engineProfile{Profile: ormsqlite.NewProfile()} }

func (engineProfile) ManagedDatabaseMarkerEnabled() bool        { return false }
func (engineProfile) ColumnDefinition(definition string) string { return strings.TrimSpace(definition) }
func (engineProfile) ApplicationTablesQuery(ormdialect.Renderer, string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'"}
}
func (engineProfile) WorkspaceTablesQuery(ormdialect.Renderer, string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT DISTINCT m.name FROM sqlite_master m JOIN pragma_table_info(m.name) p WHERE m.type = 'table' AND p.name = 'workspace_id' ORDER BY m.name"}
}
