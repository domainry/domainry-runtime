package migration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/filelock"
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
	return persistencedriver.MigrationBackupPolicy{LocalSnapshot: true, EvidenceEngine: "sqlite", BackupIDPrefix: "sqlite-"}
}
func (Profile) MigrationRollbackPolicy() persistencedriver.MigrationRollbackPolicy {
	return persistencedriver.MigrationRollbackPolicy{Mode: "restore_sqlite_backup", RequiresVerifiedBackup: true, Procedure: []string{"stop_runtime", "replace_database_with_latest_migration_backup", "restart_runtime", "verify_migration_status"}}
}
func (Profile) EnsureMigrationNamespace(context.Context, persistencedriver.SchemaDatabase, ormdialect.Renderer, string) error {
	return nil
}
func (Profile) ConfigureMigrationTransaction(context.Context, *sql.Tx, ormdialect.Renderer, string, time.Duration, time.Duration) error {
	return nil
}
func (Profile) AcquireMigrationLock(ctx context.Context, _ *sql.DB, _ ormdialect.Renderer, options persistencedriver.MigrationLockOptions) (persistencedriver.MigrationLock, error) {
	path := strings.TrimSpace(options.DatabasePath)
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return persistencedriver.MigrationLock{Release: func() {}}, nil
	}
	lockPath := path + ".migration.lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return persistencedriver.MigrationLock{}, err
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return persistencedriver.MigrationLock{}, fmt.Errorf("open sqlite migration lock: %w", err)
	}
	deadline := options.LockTimeout
	if deadline <= 0 {
		deadline = 30 * time.Second
	}
	lockCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	for {
		err = filelock.TryExclusive(file)
		if err == nil {
			break
		}
		select {
		case <-lockCtx.Done():
			_ = file.Close()
			return persistencedriver.MigrationLock{}, fmt.Errorf("migration.lock_timeout: owner=%s timeout=%s: %w", options.Owner, deadline, lockCtx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	owner := options.Owner + " acquired_at=" + time.Now().UTC().Format(time.RFC3339Nano)
	_ = file.Truncate(0)
	_, _ = file.WriteString(owner)
	return persistencedriver.MigrationLock{Release: func() { _ = filelock.Unlock(file); _ = file.Close() }}, nil
}
func (Profile) MigrationDatabasePath(cfg config.Config) string {
	if value := strings.TrimSpace(cfg.DBPath); value != "" {
		return value
	}
	return strings.TrimSpace(cfg.DatabaseDSN)
}
