package database

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

const CurrentWorkspaceRLSPolicyVersion = "v1"

type WorkspaceRLSStatus struct {
	Enabled       bool     `json:"enabled"`
	Forced        bool     `json:"forced"`
	RuntimeRole   string   `json:"runtime_role,omitempty"`
	RoleOwnsTable bool     `json:"role_owns_table"`
	RoleBypassRLS bool     `json:"role_bypass_rls"`
	PolicyVersion string   `json:"policy_version,omitempty"`
	CoveredTables []string `json:"covered_tables,omitempty"`
	MissingTables []string `json:"missing_tables,omitempty"`
}

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
	if s == nil || s.dialect.Name() != "postgres" || !s.config.DatabaseRLSEnabled {
		if s != nil {
			s.workspaceRLS = WorkspaceRLSStatus{}
		}
		return nil
	}
	if s.postgresProfile == nil {
		return fmt.Errorf("workspace RLS requires a PostgreSQL connection profile")
	}
	if s.config.EffectiveDatabaseMigrationMode() == "apply" {
		if s.migrationDB == nil {
			return fmt.Errorf("workspace RLS apply requires the migration connection")
		}
		if err := s.applyWorkspaceRLSPolicies(ctx); err != nil {
			return err
		}
	}
	status, err := s.inspectWorkspaceRLS(ctx)
	if err != nil {
		return err
	}
	s.workspaceRLS = status
	if len(status.MissingTables) > 0 {
		return fmt.Errorf("workspace RLS policy coverage is incomplete: %s", strings.Join(status.MissingTables, ", "))
	}
	return nil
}

func (s *RuntimeStore) applyWorkspaceRLSPolicies(ctx context.Context) error {
	tables, err := s.workspaceTables(ctx, s.migrationDB)
	if err != nil {
		return err
	}
	runtimeRole := s.identifier(s.postgresCapabilities.User)
	policy := s.identifier("domainry_workspace_" + CurrentWorkspaceRLSPolicyVersion)
	for _, table := range tables {
		relation := s.tableIdentifier(table)
		statements := []string{
			"ALTER TABLE " + relation + " ENABLE ROW LEVEL SECURITY",
			"ALTER TABLE " + relation + " FORCE ROW LEVEL SECURITY",
			"DROP POLICY IF EXISTS " + policy + " ON " + relation,
			"CREATE POLICY " + policy + " ON " + relation + " FOR ALL TO " + runtimeRole +
				" USING (" + s.identifier("workspace_id") + "::text = NULLIF(current_setting('domainry.workspace_id', true), ''))" +
				" WITH CHECK (" + s.identifier("workspace_id") + "::text = NULLIF(current_setting('domainry.workspace_id', true), ''))",
		}
		for _, statement := range statements {
			if _, err := s.migrationDB.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply workspace RLS policy to %s: %w", table, err)
			}
		}
	}
	ledger := s.tableIdentifier("_runtime_rls_policies")
	if _, err := s.migrationDB.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+ledger+" ("+s.identifier("version")+" TEXT PRIMARY KEY, "+s.identifier("applied_at")+" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP)"); err != nil {
		return fmt.Errorf("prepare workspace RLS policy ledger: %w", err)
	}
	if _, err := s.migrationDB.ExecContext(ctx, "INSERT INTO "+ledger+" ("+s.identifier("version")+") VALUES ("+s.placeholder(1)+") ON CONFLICT ("+s.identifier("version")+") DO NOTHING", CurrentWorkspaceRLSPolicyVersion); err != nil {
		return fmt.Errorf("record workspace RLS policy version: %w", err)
	}
	return nil
}

func (s *RuntimeStore) inspectWorkspaceRLS(ctx context.Context) (WorkspaceRLSStatus, error) {
	tables, err := s.workspaceTables(ctx, s.db)
	if err != nil {
		return WorkspaceRLSStatus{}, err
	}
	status := WorkspaceRLSStatus{Enabled: true, Forced: true, RuntimeRole: s.postgresCapabilities.User, PolicyVersion: CurrentWorkspaceRLSPolicyVersion}
	policyName := "domainry_workspace_" + CurrentWorkspaceRLSPolicyVersion
	for _, table := range tables {
		var enabled, forced, roleOwnsTable, roleBypassRLS, policyExists bool
		err := s.db.QueryRowContext(ctx, `SELECT c.relrowsecurity, c.relforcerowsecurity,
c.relowner = (SELECT oid FROM pg_roles WHERE rolname = current_user),
COALESCE((SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user), true),
EXISTS (SELECT 1 FROM pg_policies WHERE schemaname = $1 AND tablename = $2 AND policyname = $3 AND (current_user = ANY(roles) OR 'public' = ANY(roles)))
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2`, s.DatabaseSchema(), table, policyName).Scan(&enabled, &forced, &roleOwnsTable, &roleBypassRLS, &policyExists)
		if err != nil {
			return WorkspaceRLSStatus{}, fmt.Errorf("inspect workspace RLS policy for %s: %w", table, err)
		}
		status.Forced = status.Forced && forced
		status.RoleOwnsTable = status.RoleOwnsTable || roleOwnsTable
		status.RoleBypassRLS = status.RoleBypassRLS || roleBypassRLS
		if enabled && forced && !roleOwnsTable && !roleBypassRLS && policyExists {
			status.CoveredTables = append(status.CoveredTables, table)
		} else {
			status.MissingTables = append(status.MissingTables, table)
		}
	}
	var versionCount int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+s.tableIdentifier("_runtime_rls_policies")+" WHERE "+s.identifier("version")+" = "+s.placeholder(1), CurrentWorkspaceRLSPolicyVersion).Scan(&versionCount); err != nil {
		return WorkspaceRLSStatus{}, fmt.Errorf("verify workspace RLS policy version %s: %w", CurrentWorkspaceRLSPolicyVersion, err)
	}
	if versionCount != 1 {
		return WorkspaceRLSStatus{}, fmt.Errorf("verify workspace RLS policy version %s: missing ledger entry", CurrentWorkspaceRLSPolicyVersion)
	}
	return status, nil
}

func (s *RuntimeStore) workspaceTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT table_name FROM information_schema.columns WHERE table_schema = $1 AND column_name = 'workspace_id' ORDER BY table_name`, s.DatabaseSchema())
	if err != nil {
		return nil, fmt.Errorf("inventory workspace RLS tables: %w", err)
	}
	defer rows.Close()
	tables := []string{}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(tables)
	return tables, nil
}

// SetLocalWorkspaceRLSContext binds tenant and actor identity to one
// PostgreSQL transaction. The third argument to set_config makes both values
// transaction-local, so pooled connections cannot leak them to the next user.
func SetLocalWorkspaceRLSContext(ctx context.Context, tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("workspace RLS transaction is required")
	}
	workspaceID := requestcontext.WorkspaceID(ctx)
	if workspaceID == "" {
		return fmt.Errorf("workspace RLS context requires workspace_id")
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('domainry.workspace_id', $1, true), set_config('domainry.actor_id', $2, true)`, workspaceID, requestcontext.ActorID(ctx)); err != nil {
		return fmt.Errorf("set transaction-local workspace RLS context: %w", err)
	}
	return nil
}
