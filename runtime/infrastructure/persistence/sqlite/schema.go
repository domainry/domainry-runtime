package sqlite

import (
	"context"
	"database/sql"
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormdriver "github.com/domainry/domainry-orm/driver"
	ormsqlite "github.com/domainry/domainry-orm/sqlite"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	sqliteevidence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite/evidence"
)

func (engineProfile) WorkspaceRLSSupported() bool { return false }
func (engineProfile) ApplyWorkspaceRLS(context.Context, *sql.DB, ormdialect.Renderer, string, string, string) error {
	return nil
}
func (engineProfile) InspectWorkspaceRLS(context.Context, *sql.DB, ormdialect.Renderer, string, string, string) (persistencedriver.WorkspaceRLSStatus, error) {
	return persistencedriver.WorkspaceRLSStatus{}, nil
}
func (engineProfile) OrderedDecimalTextStorage() bool         { return true }
func (engineProfile) RecordReadIsolation() sql.IsolationLevel { return sql.LevelSerializable }
func (engineProfile) ReportDateBucket(value, grain string, _ bool) (string, error) {
	formats := map[string]string{"day": "%Y-%m-%dT00:00:00Z", "month": "%Y-%m-01T00:00:00Z", "year": "%Y-01-01T00:00:00Z"}
	if grain == "week" {
		return "strftime('%Y-%m-%dT00:00:00Z', " + value + ", '-' || ((CAST(strftime('%w', " + value + ") AS INTEGER) + 6) % 7) || ' days')", nil
	}
	if grain == "quarter" {
		return "printf('%04d-%02d-01T00:00:00Z', CAST(strftime('%Y', " + value + ") AS INTEGER), ((CAST(strftime('%m', " + value + ") AS INTEGER) - 1) / 3) * 3 + 1)", nil
	}
	return "strftime('" + formats[grain] + "', " + value + ")", nil
}

type engineProfile struct {
	ormdriver.Profile
	evidence persistencedriver.EvidenceSchemaProfile
}

func newEngineProfile() engineProfile {
	return engineProfile{Profile: ormsqlite.NewProfile(), evidence: sqliteevidence.NewProfile()}
}

func (engineProfile) ManagedDatabaseMarkerEnabled() bool        { return false }
func (engineProfile) ColumnDefinition(definition string) string { return strings.TrimSpace(definition) }
func (engineProfile) ApplicationTablesQuery(ormdialect.Renderer, string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'"}
}
func (engineProfile) WorkspaceTablesQuery(ormdialect.Renderer, string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT DISTINCT m.name FROM sqlite_master m JOIN pragma_table_info(m.name) p WHERE m.type = 'table' AND p.name = 'workspace_id' ORDER BY m.name"}
}
func (engineProfile) TableExistsQuery(renderer ormdialect.Renderer, _, table string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = " + renderer.Placeholder(1), Arguments: []any{table}}
}
func (engineProfile) IndexesQuery(renderer ormdialect.Renderer, _ string, table string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = " + renderer.Placeholder(1), Arguments: []any{table}}
}
func (profile engineProfile) EvidenceSchemaTypes(text string) persistencedriver.EvidenceSchemaTypes {
	return profile.evidence.Types(text)
}
func (profile engineProfile) NormalizeEvidenceSchema(ctx context.Context, database persistencedriver.SchemaDatabase, renderer ormdialect.Renderer) error {
	return profile.evidence.Normalize(ctx, database, renderer)
}
