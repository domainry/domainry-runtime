package mysql

import (
	"context"
	"database/sql"
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormdriver "github.com/domainry/domainry-orm/driver"
	ormmysql "github.com/domainry/domainry-orm/mysql"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

func (engineProfile) WorkspaceRLSSupported() bool { return false }
func (engineProfile) ApplyWorkspaceRLS(context.Context, *sql.DB, ormdialect.Renderer, string, string, string) error {
	return nil
}
func (engineProfile) InspectWorkspaceRLS(context.Context, *sql.DB, ormdialect.Renderer, string, string, string) (persistencedriver.WorkspaceRLSStatus, error) {
	return persistencedriver.WorkspaceRLSStatus{}, nil
}

type engineProfile struct{ ormdriver.Profile }

func newEngineProfile() engineProfile { return engineProfile{Profile: ormmysql.NewProfile()} }

func (engineProfile) ManagedDatabaseMarkerEnabled() bool { return true }
func (engineProfile) ColumnDefinition(definition string) string {
	definition = strings.TrimSpace(definition)
	definition = strings.ReplaceAll(definition, "TEXT NOT NULL DEFAULT '[]'", "TEXT NOT NULL DEFAULT ('[]')")
	definition = strings.ReplaceAll(definition, "TEXT NOT NULL DEFAULT '{}'", "TEXT NOT NULL DEFAULT ('{}')")
	definition = strings.ReplaceAll(definition, "TEXT NOT NULL DEFAULT ''", "TEXT NOT NULL DEFAULT ('')")
	return definition
}
func (engineProfile) ApplicationTablesQuery(ormdialect.Renderer, string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()"}
}
func (engineProfile) WorkspaceTablesQuery(renderer ormdialect.Renderer, databaseSchema string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT DISTINCT table_name FROM information_schema.columns WHERE table_schema = " + renderer.Placeholder(1) + " AND column_name = 'workspace_id' ORDER BY table_name", Arguments: []any{databaseSchema}}
}
