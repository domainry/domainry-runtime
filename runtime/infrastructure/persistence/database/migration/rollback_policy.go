package migration

import deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"

func RollbackPolicy(driver string) deploymentmodel.MigrationRollbackPolicy {
	switch driver {
	case "sqlite":
		return deploymentmodel.MigrationRollbackPolicy{Mode: "restore_sqlite_backup", RequiresVerifiedBackup: true, Procedure: []string{"stop_runtime", "replace_database_with_latest_migration_backup", "restart_runtime", "verify_migration_status"}}
	case "postgres":
		return deploymentmodel.MigrationRollbackPolicy{Mode: "restore_external_backup_or_pitr", RequiresVerifiedBackup: true, Procedure: []string{"stop_runtime", "restore_verified_database_backup_or_pitr", "restart_runtime", "verify_migration_status"}}
	case "mysql":
		return deploymentmodel.MigrationRollbackPolicy{Mode: "restore_external_backup", RequiresVerifiedBackup: true, Procedure: []string{"stop_runtime", "restore_verified_database_backup", "restart_runtime", "verify_migration_status"}}
	default:
		return deploymentmodel.MigrationRollbackPolicy{Mode: "unsupported", RequiresVerifiedBackup: true}
	}
}
