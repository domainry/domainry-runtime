package database

import (
	"context"
	"fmt"
	"strings"

	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

const CurrentWorkspaceRLSPolicyVersion = "v1"

type WorkspaceRLSStatus = persistencedriver.WorkspaceRLSStatus

func (s *RuntimeStore) WorkspaceRLSStatus(_ context.Context) WorkspaceRLSStatus {
	status := s.workspaceRLS
	status.CoveredTables = append([]string(nil), status.CoveredTables...)
	status.MissingTables = append([]string(nil), status.MissingTables...)
	return status
}

// EnsureWorkspaceRLS applies or verifies one generated policy template for
// every existing Runtime table that owns a workspace_id column. RLS remains
// opt-in; repository predicates are always the primary isolation boundary.
func (s *RuntimeStore) EnsureWorkspaceRLS(ctx context.Context) error {
	if s == nil {
		return nil
	}
	base := s.sqlBase()
	if !s.config.DatabaseRLSEnabled || !base.RuntimeEngine.WorkspaceRLSSupported() {
		s.workspaceRLS = WorkspaceRLSStatus{}
		return nil
	}
	if s.postgresProfile == nil {
		return fmt.Errorf("workspace RLS requires a PostgreSQL connection profile")
	}
	runtimeRole := strings.TrimSpace(s.postgresCapabilities.User)
	if s.config.EffectiveDatabaseMigrationMode() == "apply" {
		if s.migrationDB == nil {
			return fmt.Errorf("workspace RLS apply requires the migration connection")
		}
		if err := base.RuntimeEngine.ApplyWorkspaceRLS(ctx, s.migrationDB, base.SQLRenderer, base.DatabaseSchema, runtimeRole, CurrentWorkspaceRLSPolicyVersion); err != nil {
			return err
		}
	}
	status, err := base.RuntimeEngine.InspectWorkspaceRLS(ctx, s.db, base.SQLRenderer, base.DatabaseSchema, runtimeRole, CurrentWorkspaceRLSPolicyVersion)
	if err != nil {
		return err
	}
	s.workspaceRLS = status
	if len(status.MissingTables) > 0 {
		return fmt.Errorf("workspace RLS policy coverage is incomplete: %s", strings.Join(status.MissingTables, ", "))
	}
	return nil
}
