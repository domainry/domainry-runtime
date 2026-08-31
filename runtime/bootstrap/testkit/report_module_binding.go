package testkit

import (
	"context"

	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodulehost "github.com/domainry/domainry-report-sdk/modulehost"
	reportmodule "github.com/domainry/domainry-report/module"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func openTestkitReportBinding(ctx context.Context, store *database.RuntimeStore) (reportsdk.Binding, error) {
	host := testkitReportHost{store: store}
	return reportmodule.NewFactory().Open(ctx, reportsdk.ApplicationRef{RuntimeID: "runtime-testkit"}, host)
}

type testkitReportHost struct{ store *database.RuntimeStore }

func (h testkitReportHost) Database() reportmodulehost.Database { return h.store.DB() }
func (h testkitReportHost) DatabaseFor(ctx context.Context) reportmodulehost.DBTX {
	if tx := database.ActionExecutionTransaction(ctx); tx != nil {
		return tx
	}
	return h.store.DB()
}
func (h testkitReportHost) Dialect() reportmodulehost.Dialect { return h.store.SQLRenderer }
func (h testkitReportHost) Migrations() reportmodulehost.MigrationRegistrar {
	return testkitReportMigrationRegistrar{store: h.store}
}

type testkitReportMigrationRegistrar struct{ store *database.RuntimeStore }

func (r testkitReportMigrationRegistrar) Driver() string { return r.store.Driver() }
func (r testkitReportMigrationRegistrar) Schema() string { return r.store.DatabaseSchema() }
func (r testkitReportMigrationRegistrar) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []reportmodulehost.SchemaMigration) error {
	return r.store.ApplyORMOwnedMigrations(ctx, owner, migrations)
}
