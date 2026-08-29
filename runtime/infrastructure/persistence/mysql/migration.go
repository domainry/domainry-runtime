package mysql

import (
	"context"
	"database/sql"
	"time"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func (engineProfile) MigrationLedgerTypes() persistencedriver.MigrationLedgerTypes {
	return persistencedriver.MigrationLedgerTypes{Key: "VARCHAR(255)", Timestamp: "VARCHAR(64)"}
}
func (engineProfile) EnsureMigrationNamespace(context.Context, persistencedriver.SchemaDatabase, ormdialect.Renderer, string) error {
	return nil
}
func (engineProfile) ConfigureMigrationTransaction(context.Context, *sql.Tx, ormdialect.Renderer, string, time.Duration, time.Duration) error {
	return nil
}

func (engineProfile) MigrationDatabasePath(config.Config) string { return "" }
