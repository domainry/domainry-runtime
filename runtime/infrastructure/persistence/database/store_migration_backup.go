package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/logging"
	migrationcontract "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/migration"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"go.uber.org/zap"
)

func (s *RuntimeStore) ensureMigrationBackupForExistingData(ctx context.Context, cfg config.Config) error {
	if s.migrationBackupReady {
		return nil
	}
	hasData, err := s.hasExistingApplicationData(ctx)
	if err != nil {
		return fmt.Errorf("check existing data before migration backup: %w", err)
	}
	if !hasData {
		s.migrationBackupReady = true
		s.migrationBackupID = "bootstrap-empty"
		return nil
	}
	policy := s.sqlBase().RuntimeEngine.MigrationBackupPolicy()
	if policy.LocalSnapshot {
		backupPath, err := s.createSQLiteMigrationBackup(ctx, cfg)
		if err != nil {
			return err
		}
		s.migrationBackupReady = true
		checksum, checksumErr := s.migrationBackupChecksum(backupPath)
		if checksumErr != nil {
			return checksumErr
		}
		s.migrationBackupID = policy.BackupIDPrefix + checksum[:16]
		logging.FromContext(ctx).Info(
			"database migration backup created",
			zap.String("backup_id", s.migrationBackupID),
			zap.String("database_engine", policy.EvidenceEngine),
		)
		if s.operationalMetrics != nil {
			s.operationalMetrics.ObserveBackupSuccess(time.Now().UTC())
		}
		return nil
	}
	if strings.TrimSpace(policy.EvidenceEngine) == "" {
		return fmt.Errorf("database engine does not define a migration backup policy")
	}
	evidence, err := validateExternalMigrationBackup(policy.EvidenceEngine, cfg.MigrationBackupEvidencePath)
	if err != nil {
		return err
	}
	if s.operationalMetrics != nil {
		s.operationalMetrics.ObserveBackupSuccess(evidence.VerifiedAt)
	}
	s.migrationBackupID = evidence.BackupID
	s.migrationBackupReady = true
	return nil
}

// EnsureMigrationBackup secures the pre-change backup for a database that
// already holds application data and returns its id. Definition upgrades call
// it before executing physical steps; the result is cached per store so the
// runtime schema migration and the definition upgrade share one backup.
func (s *RuntimeStore) EnsureMigrationBackup(ctx context.Context) (string, error) {
	if err := s.ensureMigrationBackupForExistingData(ctx, s.runtimeMigrationConfig()); err != nil {
		return "", err
	}
	return s.migrationBackupID, nil
}

func (s *RuntimeStore) migrationBackupChecksum(path string) (string, error) {
	if s.backupChecksum != nil {
		return s.backupChecksum(path)
	}
	return migrationChecksum(path)
}

func validateExternalMigrationBackup(driver, evidencePath string) (migrationcontract.BackupEvidence, error) {
	driver = strings.TrimSpace(driver)
	if driver == "" {
		return migrationcontract.BackupEvidence{}, fmt.Errorf("migration backup evidence engine is required")
	}
	if strings.TrimSpace(evidencePath) == "" {
		return migrationcontract.BackupEvidence{}, fmt.Errorf("existing %s application data detected before pending migrations; MIGRATION_BACKUP_EVIDENCE_PATH with a verified backup_id is required", driver)
	}
	evidence, err := migrationcontract.ReadBackupEvidence(evidencePath)
	if err != nil {
		return migrationcontract.BackupEvidence{}, err
	}
	if evidence.Engine != driver {
		return migrationcontract.BackupEvidence{}, fmt.Errorf("backup evidence engine %q does not match %q", evidence.Engine, driver)
	}
	return evidence, nil
}

func (s *RuntimeStore) hasExistingApplicationData(ctx context.Context) (bool, error) {
	tables, err := s.applicationTables(ctx)
	if err != nil {
		return false, err
	}
	for _, table := range tables {
		var count int
		query := "SELECT COUNT(*) FROM " + s.tableIdentifier(table)
		if err := s.schemaDatabase().QueryRowContext(ctx, query).Scan(&count); err != nil {
			return false, fmt.Errorf("count %s: %w", table, err)
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (s *RuntimeStore) applicationTables(ctx context.Context) ([]string, error) {
	base := s.sqlBase()
	query := base.RuntimeEngine.ApplicationTablesQuery(base.SQLRenderer, base.DatabaseSchema)
	if strings.TrimSpace(query.Statement) == "" {
		return nil, fmt.Errorf("unsupported database driver %q: application table introspection is unavailable", s.engine.Name())
	}
	rows, err := s.schemaDatabase().QueryContext(ctx, query.Statement, query.Arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tables := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if isMigrationSystemTable(name) {
			continue
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(tables)
	return tables, nil
}

func isMigrationSystemTable(name string) bool {
	switch strings.TrimSpace(name) {
	case "", "_schema_migrations":
		return true
	default:
		return false
	}
}

func (s *RuntimeStore) createSQLiteMigrationBackup(ctx context.Context, cfg config.Config) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dbPath := strings.TrimSpace(cfg.DatabaseDSN)
	if dbPath == "" {
		dbPath = strings.TrimSpace(cfg.DBPath)
	}
	if dbPath == "" || dbPath == ":memory:" || strings.HasPrefix(dbPath, "file:") {
		return "", fmt.Errorf("existing SQLite data detected but APP_DB_PATH is not a copyable file path; provide a file-backed database so Runtime can create a verified encrypted backup")
	}
	if err := os.MkdirAll(cfg.MigrationBackupDir, 0o755); err != nil {
		return "", fmt.Errorf("create migration backup directory: %w", err)
	}
	backupPath := filepath.Join(cfg.MigrationBackupDir, filepath.Base(dbPath)+"."+time.Now().UTC().Format("20060102T150405Z")+".bak.enc")
	if _, err := os.Stat(backupPath); err == nil {
		return "", fmt.Errorf("sqlite migration backup already exists: %s", backupPath)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect sqlite migration backup: %w", err)
	}
	plainPath := backupPath + ".partial"
	if _, err := s.schemaDatabase().ExecContext(ctx, "VACUUM INTO "+s.placeholder(1), plainPath); err != nil {
		_ = os.Remove(plainPath)
		return "", fmt.Errorf("create consistent sqlite migration backup: %w", err)
	}
	defer os.Remove(plainPath)
	if err := migrationcontract.EncryptBackupFile(plainPath, backupPath, s.secretMaterialKey[:]); err != nil {
		return "", fmt.Errorf("encrypt sqlite migration backup: %w", err)
	}
	return backupPath, nil
}
