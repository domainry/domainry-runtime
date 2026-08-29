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
	sqliterecord "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite/record"
	sqlitereport "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite/report"
	sqliterls "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite/rls"
	sqliteschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite/schema"
)

type engineProfile struct {
	ormdriver.Profile
	persistencedriver.MigrationProfile
	persistencedriver.SchemaProfile
	persistencedriver.WorkspaceRLSProfile
	evidence persistencedriver.EvidenceSchemaProfile
	record   persistencedriver.RecordProfile
	report   persistencedriver.ReportProfile
}

func newEngineProfile() engineProfile {
	return engineProfile{
		Profile: ormsqlite.NewProfile(), MigrationProfile: sqlitemigration.NewProfile(),
		SchemaProfile: sqliteschema.NewProfile(), WorkspaceRLSProfile: sqliterls.NewProfile(),
		evidence: sqliteevidence.NewProfile(), record: sqliterecord.NewProfile(), report: sqlitereport.NewProfile(),
	}
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
