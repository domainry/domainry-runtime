package sqlite

import (
	"context"
	"database/sql"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormdriver "github.com/domainry/domainry-orm/driver"
	ormsqlite "github.com/domainry/domainry-orm/sqlite"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	sqliteevidence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite/evidence"
	sqlitemigration "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite/migration"
	sqliteprojectdatabase "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite/projectdatabase"
	sqliterecord "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite/record"
	sqlitereport "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite/report"
	sqliterls "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite/rls"
	sqliteschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite/schema"
)

type Engine struct {
	Dialect
	ormdriver.Profile
	persistencedriver.MigrationProfile
	persistencedriver.ProjectDatabaseProfile
	persistencedriver.SchemaProfile
	persistencedriver.WorkspaceRLSProfile
	evidence persistencedriver.EvidenceSchemaProfile
	record   persistencedriver.RecordProfile
	report   persistencedriver.ReportProfile
}

func NewEngine() Engine {
	return Engine{Dialect: Dialect{},
		Profile: ormsqlite.NewProfile(), MigrationProfile: sqlitemigration.NewProfile(),
		ProjectDatabaseProfile: sqliteprojectdatabase.NewProfile(),
		SchemaProfile:          sqliteschema.NewProfile(), WorkspaceRLSProfile: sqliterls.NewProfile(),
		evidence: sqliteevidence.NewProfile(), record: sqliterecord.NewProfile(), report: sqlitereport.NewProfile(),
	}
}

func (profile Engine) Name() ormdialect.Name { return profile.Profile.Name() }

func (profile Engine) EvidenceSchemaTypes(text string) persistencedriver.EvidenceSchemaTypes {
	return profile.evidence.Types(text)
}
func (profile Engine) NormalizeEvidenceSchema(ctx context.Context, database persistencedriver.SchemaDatabase, renderer ormdialect.Renderer) error {
	return profile.evidence.Normalize(ctx, database, renderer)
}
func (profile Engine) OrderedDecimalTextStorage() bool {
	return profile.record.OrderedDecimalTextStorage()
}
func (profile Engine) RecordReadIsolation() sql.IsolationLevel {
	return profile.record.ReadIsolation()
}
func (profile Engine) DatabaseCurrentTimeQuery() persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT CAST(strftime('%s','now') AS INTEGER)"}
}
func (profile Engine) ReportDateBucket(value, grain string, date bool) (string, error) {
	return profile.report.DateBucket(value, grain, date)
}
