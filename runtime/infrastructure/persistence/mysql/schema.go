package mysql

import (
	"context"
	"database/sql"
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormdriver "github.com/domainry/domainry-orm/driver"
	ormmysql "github.com/domainry/domainry-orm/mysql"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	mysqlevidence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql/evidence"
	mysqlrecord "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql/record"
	mysqlreport "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql/report"
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
	evidence persistencedriver.EvidenceSchemaProfile
	record   persistencedriver.RecordProfile
	report   persistencedriver.ReportProfile
}

func newEngineProfile() engineProfile {
	return engineProfile{Profile: ormmysql.NewProfile(), evidence: mysqlevidence.NewProfile(), record: mysqlrecord.NewProfile(), report: mysqlreport.NewProfile()}
}

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
func (engineProfile) TableExistsQuery(renderer ormdialect.Renderer, _ string, table string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = " + renderer.Placeholder(1), Arguments: []any{table}}
}
func (engineProfile) IndexesQuery(renderer ormdialect.Renderer, _ string, table string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT DISTINCT INDEX_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = " + renderer.Placeholder(1), Arguments: []any{table}}
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
