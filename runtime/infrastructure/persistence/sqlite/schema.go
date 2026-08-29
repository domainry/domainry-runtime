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
	sqlitemigration "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite/migration"
	sqliterecord "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite/record"
	sqlitereport "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite/report"
)

func (engineProfile) WorkspaceRLSSupported() bool { return false }
func (engineProfile) ApplyWorkspaceRLS(context.Context, *sql.DB, ormdialect.Renderer, string, string, string) error {
	return nil
}
func (engineProfile) InspectWorkspaceRLS(context.Context, *sql.DB, ormdialect.Renderer, string, string, string) (persistencedriver.WorkspaceRLSStatus, error) {
	return persistencedriver.WorkspaceRLSStatus{}, nil
}

type engineProfile struct {
	ormdriver.Profile
	persistencedriver.MigrationProfile
	evidence persistencedriver.EvidenceSchemaProfile
	record   persistencedriver.RecordProfile
	report   persistencedriver.ReportProfile
}

func newEngineProfile() engineProfile {
	return engineProfile{Profile: ormsqlite.NewProfile(), MigrationProfile: sqlitemigration.NewProfile(), evidence: sqliteevidence.NewProfile(), record: sqliterecord.NewProfile(), report: sqlitereport.NewProfile()}
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
func (profile engineProfile) OrderedDecimalTextStorage() bool {
	return profile.record.OrderedDecimalTextStorage()
}
func (profile engineProfile) RecordReadIsolation() sql.IsolationLevel {
	return profile.record.ReadIsolation()
}
func (profile engineProfile) ReportDateBucket(value, grain string, date bool) (string, error) {
	return profile.report.DateBucket(value, grain, date)
}
