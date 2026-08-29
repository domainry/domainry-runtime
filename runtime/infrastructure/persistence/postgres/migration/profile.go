package migration

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) MigrationLedgerTypes() persistencedriver.MigrationLedgerTypes {
	return persistencedriver.MigrationLedgerTypes{Key: "TEXT", Timestamp: "TEXT"}
}
func (Profile) MigrationBackupPolicy() persistencedriver.MigrationBackupPolicy {
	return persistencedriver.MigrationBackupPolicy{EvidenceEngine: "postgres"}
}
func (Profile) MigrationRollbackPolicy() persistencedriver.MigrationRollbackPolicy {
	return persistencedriver.MigrationRollbackPolicy{Mode: "restore_external_backup_or_pitr", RequiresVerifiedBackup: true, Procedure: []string{"stop_runtime", "restore_verified_database_backup_or_pitr", "restart_runtime", "verify_migration_status"}}
}
func (Profile) EnsureMigrationNamespace(ctx context.Context, database persistencedriver.SchemaDatabase, renderer ormdialect.Renderer, databaseSchema string) error {
	if strings.TrimSpace(databaseSchema) == "" || strings.EqualFold(databaseSchema, "public") {
		return nil
	}
	if _, err := database.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+renderer.Identifier(databaseSchema)); err != nil {
		return fmt.Errorf("prepare PostgreSQL runtime schema: %w", err)
	}
	return nil
}
func (Profile) ConfigureMigrationTransaction(ctx context.Context, transaction *sql.Tx, renderer ormdialect.Renderer, databaseSchema string, lockTimeout, statementTimeout time.Duration) error {
	if _, err := transaction.ExecContext(ctx, "SELECT set_config('search_path', "+renderer.Placeholder(1)+", true)", databaseSchema); err != nil {
		return fmt.Errorf("set migration schema search path: %w", err)
	}
	if lockTimeout > 0 {
		if _, err := transaction.ExecContext(ctx, "SELECT set_config('lock_timeout', "+renderer.Placeholder(1)+", true)", fmt.Sprintf("%dms", lockTimeout.Milliseconds())); err != nil {
			return fmt.Errorf("set migration lock timeout: %w", err)
		}
	}
	if statementTimeout > 0 {
		if _, err := transaction.ExecContext(ctx, "SELECT set_config('statement_timeout', "+renderer.Placeholder(1)+", true)", fmt.Sprintf("%dms", statementTimeout.Milliseconds())); err != nil {
			return fmt.Errorf("set migration statement timeout: %w", err)
		}
	}
	return nil
}
func (Profile) AcquireMigrationLock(ctx context.Context, database *sql.DB, renderer ormdialect.Renderer, options persistencedriver.MigrationLockOptions) (persistencedriver.MigrationLock, error) {
	conn, err := database.Conn(ctx)
	if err != nil {
		return persistencedriver.MigrationLock{}, fmt.Errorf("acquire migration connection: %w", err)
	}
	key := "domainry_runtime_migrations:" + options.DatabaseSchema
	deadline := options.LockTimeout
	if deadline <= 0 {
		deadline = 30 * time.Second
	}
	lockCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	for {
		var locked bool
		err = conn.QueryRowContext(lockCtx, "SELECT pg_try_advisory_lock(hashtextextended("+renderer.Placeholder(1)+", 0))", key).Scan(&locked)
		if err != nil {
			_ = conn.Close()
			return persistencedriver.MigrationLock{}, fmt.Errorf("acquire migration lock: %w", err)
		}
		if locked {
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
		_, _ = conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock(hashtextextended("+renderer.Placeholder(1)+", 0))", key)
		_ = conn.Close()
	}}, nil
}
func (Profile) MigrationDatabasePath(config.Config) string { return "" }
