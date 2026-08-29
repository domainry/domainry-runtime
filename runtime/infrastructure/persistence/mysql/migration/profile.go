package migration

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) MigrationLedgerTypes() persistencedriver.MigrationLedgerTypes {
	return persistencedriver.MigrationLedgerTypes{Key: "VARCHAR(255)", Timestamp: "VARCHAR(64)"}
}
func (Profile) MigrationBackupPolicy() persistencedriver.MigrationBackupPolicy {
	return persistencedriver.MigrationBackupPolicy{EvidenceEngine: "mysql"}
}
func (Profile) MigrationRollbackPolicy() persistencedriver.MigrationRollbackPolicy {
	return persistencedriver.MigrationRollbackPolicy{Mode: "restore_external_backup", RequiresVerifiedBackup: true, Procedure: []string{"stop_runtime", "restore_verified_database_backup", "restart_runtime", "verify_migration_status"}}
}
func (Profile) EnsureMigrationNamespace(context.Context, persistencedriver.SchemaDatabase, ormdialect.Renderer, string) error {
	return nil
}
func (Profile) ConfigureMigrationTransaction(context.Context, *sql.Tx, ormdialect.Renderer, string, time.Duration, time.Duration) error {
	return nil
}
func (Profile) AcquireMigrationLock(ctx context.Context, database *sql.DB, renderer ormdialect.Renderer, options persistencedriver.MigrationLockOptions) (persistencedriver.MigrationLock, error) {
	conn, err := database.Conn(ctx)
	if err != nil {
		return persistencedriver.MigrationLock{}, fmt.Errorf("acquire migration connection: %w", err)
	}
	deadline := options.LockTimeout
	if deadline <= 0 {
		deadline = 30 * time.Second
	}
	lockCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	for {
		var result sql.NullInt64
		err = conn.QueryRowContext(lockCtx, "SELECT GET_LOCK("+renderer.Placeholder(1)+", 0)", "domainry_runtime_migrations").Scan(&result)
		if err != nil {
			_ = conn.Close()
			return persistencedriver.MigrationLock{}, fmt.Errorf("acquire migration lock: %w", err)
		}
		if result.Valid && result.Int64 == 1 {
			break
		}
		select {
		case <-lockCtx.Done():
			_ = conn.Close()
			return persistencedriver.MigrationLock{}, fmt.Errorf("migration.lock_timeout: owner=%s timeout=%s: %w", options.Owner, deadline, lockCtx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	return persistencedriver.MigrationLock{Connection: conn, Release: func() {
		timeout := options.ConnectTimeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		unlockCtx, unlockCancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer unlockCancel()
		_, _ = conn.ExecContext(unlockCtx, "SELECT RELEASE_LOCK("+renderer.Placeholder(1)+")", "domainry_runtime_migrations")
		_ = conn.Close()
	}}, nil
}
func (Profile) MigrationDatabasePath(config.Config) string { return "" }
