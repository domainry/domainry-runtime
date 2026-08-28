package database

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/migration"
)

// MigrationStatus reports database infrastructure state without exposing
// Store internals to the deployment-owned repository adapter.
func (s *RuntimeStore) MigrationStatus(ctx context.Context) (deploymentmodel.MigrationStatus, error) {
	status := deploymentmodel.MigrationStatus{Current: true, State: migration.StateCurrent, RuntimeVersion: s.config.RuntimeVersion, ExpectedPaths: append([]string(nil), s.expectedMigrations...), Rollback: migration.RollbackPolicy(s.Driver())}
	sort.Strings(status.ExpectedPaths)
	if len(status.ExpectedPaths) > 0 {
		status.MinSchemaVersion, _ = migrationIdentity(status.ExpectedPaths[0])
		status.MaxSchemaVersion, _ = migrationIdentity(status.ExpectedPaths[len(status.ExpectedPaths)-1])
	}
	if value := strings.TrimSpace(s.config.DatabaseMinSchemaVersion); value != "" {
		status.MinSchemaVersion = value
	}
	if value := strings.TrimSpace(s.config.DatabaseMaxSchemaVersion); value != "" {
		status.MaxSchemaVersion = value
	}
	status.Expected = len(status.ExpectedPaths)
	applied := map[string]struct{}{}
	currentSchemaVersion := ""
	rows, err := s.schemaDatabase().QueryContext(ctx, "SELECT "+s.identifier("path")+", "+s.identifier("checksum")+", "+s.identifier("dirty")+", "+s.identifier("applied_at")+" FROM "+s.tableIdentifier("_schema_migrations")+" ORDER BY "+s.identifier("path")+" ASC")
	if err != nil {
		status.Current = false
		return status, fmt.Errorf("read migration status: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var path, checksum, at string
		var dirty bool
		if err := rows.Scan(&path, &checksum, &dirty, &at); err != nil {
			status.Current = false
			return status, fmt.Errorf("scan migration status: %w", err)
		}
		status.AppliedPaths = append(status.AppliedPaths, path)
		applied[path] = struct{}{}
		if expected, tracked := s.expectedChecksums[path]; tracked {
			version, _ := migrationIdentity(filepath.Base(path))
			if currentSchemaVersion == "" || migration.CompareVersions(version, currentSchemaVersion) > 0 {
				currentSchemaVersion = version
			}
			if checksum != expected {
				status.DriftPaths = append(status.DriftPaths, path)
			}
		} else if !strings.HasPrefix(strings.TrimSpace(path), "module_") {
			version, _ := migrationIdentity(filepath.Base(path))
			if status.MaxSchemaVersion != "" && migration.CompareVersions(version, status.MaxSchemaVersion) > 0 {
				status.NewerPaths = append(status.NewerPaths, path)
			} else {
				status.UnknownPaths = append(status.UnknownPaths, path)
			}
		}
		if dirty {
			status.DirtyPaths = append(status.DirtyPaths, path)
		}
		if at > status.LastAppliedAt {
			status.LastAppliedAt = at
		}
	}
	if err := rows.Err(); err != nil {
		status.Current = false
		return status, fmt.Errorf("read migration rows: %w", err)
	}
	status.Applied = len(status.AppliedPaths)
	for _, path := range status.ExpectedPaths {
		if _, ok := applied[path]; !ok {
			status.PendingPaths = append(status.PendingPaths, path)
		}
	}
	status.Pending = len(status.PendingPaths)
	status.Drift = len(status.DriftPaths)
	status.Dirty = len(status.DirtyPaths)
	status.Unknown = len(status.UnknownPaths)
	status.Newer = len(status.NewerPaths)
	if currentSchemaVersion != "" && status.MinSchemaVersion != "" && migration.CompareVersions(currentSchemaVersion, status.MinSchemaVersion) < 0 && status.Pending == 0 {
		status.Pending = 1
	}
	if currentSchemaVersion != "" && status.MaxSchemaVersion != "" && migration.CompareVersions(currentSchemaVersion, status.MaxSchemaVersion) > 0 && status.Newer == 0 {
		status.Newer = 1
		status.NewerPaths = append(status.NewerPaths, currentSchemaVersion)
	}
	switch {
	case status.Dirty > 0:
		status.State, status.ErrorCode = migration.StateDirty, migration.StateDirty
	case status.Drift > 0:
		status.State, status.ErrorCode = migration.StateDrift, migration.StateDrift
	case status.Newer > 0:
		status.State, status.ErrorCode = migration.StateNewer, migration.StateNewer
	case status.Unknown > 0:
		status.State, status.ErrorCode = migration.StateUnknown, migration.StateUnknown
	case status.Pending > 0:
		status.State, status.ErrorCode = migration.StatePending, migration.StatePending
	}
	status.Current = status.State == migration.StateCurrent
	return status, nil
}
