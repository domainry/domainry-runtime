package postgres

import (
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormdriver "github.com/domainry/domainry-orm/driver"
	ormpostgres "github.com/domainry/domainry-orm/postgres"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

type engineProfile struct{ ormdriver.Profile }

func newEngineProfile() engineProfile { return engineProfile{Profile: ormpostgres.NewProfile()} }

func (engineProfile) ManagedDatabaseMarkerEnabled() bool        { return true }
func (engineProfile) ColumnDefinition(definition string) string { return strings.TrimSpace(definition) }
func (engineProfile) ApplicationTablesQuery(renderer ormdialect.Renderer, databaseSchema string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT table_name FROM information_schema.tables WHERE table_schema = " + renderer.Placeholder(1), Arguments: []any{databaseSchema}}
}
func (engineProfile) WorkspaceTablesQuery(renderer ormdialect.Renderer, databaseSchema string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT DISTINCT table_name FROM information_schema.columns WHERE table_schema = " + renderer.Placeholder(1) + " AND column_name = 'workspace_id' ORDER BY table_name", Arguments: []any{databaseSchema}}
}
