package postgres

import (
	"context"
	"database/sql"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormdriver "github.com/domainry/domainry-orm/driver"
	ormpostgres "github.com/domainry/domainry-orm/postgres"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	postgresevidence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres/evidence"
	postgresmigration "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres/migration"
	postgresprojectdatabase "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres/projectdatabase"
	postgresrecord "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres/record"
	postgresreport "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres/report"
	postgresrls "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres/rls"
	postgresschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres/schema"
)

type engineProfile struct {
	ormdriver.Profile
	persistencedriver.MigrationProfile
	persistencedriver.ProjectDatabaseProfile
	persistencedriver.SchemaProfile
	persistencedriver.WorkspaceRLSProfile
	evidence persistencedriver.EvidenceSchemaProfile
	record   persistencedriver.RecordProfile
	report   persistencedriver.ReportProfile
}

func newEngineProfile() engineProfile {
	return engineProfile{
		Profile: ormpostgres.NewProfile(), MigrationProfile: postgresmigration.NewProfile(),
		ProjectDatabaseProfile: postgresprojectdatabase.NewProfile(),
		SchemaProfile:          postgresschema.NewProfile(), WorkspaceRLSProfile: postgresrls.NewProfile(),
		evidence: postgresevidence.NewProfile(), record: postgresrecord.NewProfile(), report: postgresreport.NewProfile(),
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
