package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func (engineProfile) MigrationLedgerTypes() persistencedriver.MigrationLedgerTypes {
	return persistencedriver.MigrationLedgerTypes{Key: "TEXT", Timestamp: "TEXT"}
}
func (engineProfile) MigrationBackupPolicy() persistencedriver.MigrationBackupPolicy {
	return persistencedriver.MigrationBackupPolicy{LocalSnapshot: true, EvidenceEngine: "sqlite", BackupIDPrefix: "sqlite-"}
}
func (engineProfile) MigrationRollbackPolicy() persistencedriver.MigrationRollbackPolicy {
	return persistencedriver.MigrationRollbackPolicy{Mode: "restore_sqlite_backup", RequiresVerifiedBackup: true, Procedure: []string{"stop_runtime", "replace_database_with_latest_migration_backup", "restart_runtime", "verify_migration_status"}}
}
func (engineProfile) EnsureMigrationNamespace(context.Context, persistencedriver.SchemaDatabase, ormdialect.Renderer, string) error {
	return nil
}
func (engineProfile) ConfigureMigrationTransaction(context.Context, *sql.Tx, ormdialect.Renderer, string, time.Duration, time.Duration) error {
	return nil
}

func (engineProfile) MigrationDatabasePath(cfg config.Config) string {
	if value := strings.TrimSpace(cfg.DBPath); value != "" {
		return value
	}
	return strings.TrimSpace(cfg.DatabaseDSN)
}
