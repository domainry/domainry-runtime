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
)

func (engineProfile) WorkspaceRLSSupported() bool { return false }
func (engineProfile) ApplyWorkspaceRLS(context.Context, *sql.DB, ormdialect.Renderer, string, string, string) error {
	return nil
}
func (engineProfile) InspectWorkspaceRLS(context.Context, *sql.DB, ormdialect.Renderer, string, string, string) (persistencedriver.WorkspaceRLSStatus, error) {
	return persistencedriver.WorkspaceRLSStatus{}, nil
}
func (engineProfile) OrderedDecimalTextStorage() bool         { return false }
func (engineProfile) RecordReadIsolation() sql.IsolationLevel { return sql.LevelSerializable }
func (engineProfile) ReportDateBucket(value, grain string, _ bool) (string, error) {
	formats := map[string]string{"day": "%Y-%m-%d 00:00:00", "week": "%x-%v-1 00:00:00", "month": "%Y-%m-01 00:00:00", "year": "%Y-01-01 00:00:00"}
	if grain == "quarter" {
		return "STR_TO_DATE(CONCAT(YEAR(" + value + "), '-', LPAD(((QUARTER(" + value + ") - 1) * 3) + 1, 2, '0'), '-01'), '%Y-%m-%d')", nil
	}
	return "DATE_FORMAT(" + value + ", '" + formats[grain] + "')", nil
}

type engineProfile struct {
	ormdriver.Profile
	evidence persistencedriver.EvidenceSchemaProfile
}

func newEngineProfile() engineProfile {
	return engineProfile{Profile: ormmysql.NewProfile(), evidence: mysqlevidence.NewProfile()}
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
