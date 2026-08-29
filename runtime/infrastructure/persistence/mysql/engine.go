package mysql

import (
	"context"
	"database/sql"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormdriver "github.com/domainry/domainry-orm/driver"
	ormmysql "github.com/domainry/domainry-orm/mysql"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	mysqlevidence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql/evidence"
	mysqlmigration "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql/migration"
	mysqlprojectdatabase "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql/projectdatabase"
	mysqlrecord "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql/record"
	mysqlreport "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql/report"
	mysqlrls "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql/rls"
	mysqlschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql/schema"
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
		Profile: ormmysql.NewProfile(), MigrationProfile: mysqlmigration.NewProfile(),
		ProjectDatabaseProfile: mysqlprojectdatabase.NewProfile(),
		SchemaProfile:          mysqlschema.NewProfile(), WorkspaceRLSProfile: mysqlrls.NewProfile(),
		evidence: mysqlevidence.NewProfile(), record: mysqlrecord.NewProfile(), report: mysqlreport.NewProfile(),
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
func (profile Engine) ReportDateBucket(value, grain string, date bool) (string, error) {
	return profile.report.DateBucket(value, grain, date)
}
