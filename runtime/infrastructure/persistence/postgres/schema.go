package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/requestcontext"
	ormbuilder "github.com/domainry/domainry-orm/builder"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormdriver "github.com/domainry/domainry-orm/driver"
	ormpostgres "github.com/domainry/domainry-orm/postgres"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

type engineProfile struct{ ormdriver.Profile }

func newEngineProfile() engineProfile { return engineProfile{Profile: ormpostgres.NewProfile()} }

func (engineProfile) ManagedDatabaseMarkerEnabled() bool        { return true }
func (engineProfile) ColumnDefinition(definition string) string { return strings.TrimSpace(definition) }
func (engineProfile) ApplicationTablesQuery(renderer ormdialect.Renderer, databaseSchema string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT table_name FROM information_schema.tables WHERE table_schema = " + renderer.Placeholder(1), Arguments: []any{databaseSchema}}
}
func (engineProfile) WorkspaceTablesQuery(renderer ormdialect.Renderer, databaseSchema string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{Statement: "SELECT DISTINCT table_name FROM information_schema.columns WHERE table_schema = " + renderer.Placeholder(1) + " AND column_name = 'workspace_id' ORDER BY table_name", Arguments: []any{databaseSchema}}
}

func (engineProfile) WorkspaceRLSSupported() bool { return true }

func (engineProfile) ApplyWorkspaceRLS(ctx context.Context, database *sql.DB, renderer ormdialect.Renderer, databaseSchema, runtimeRole, policyVersion string) error {
	tables, err := postgresWorkspaceTables(ctx, database, renderer, databaseSchema)
	if err != nil {
		return err
	}
	role := renderer.Identifier(runtimeRole)
	policy := renderer.Identifier("domainry_workspace_" + policyVersion)
	for _, table := range tables {
		relation := renderer.Table(table)
		statements := []string{
			"ALTER TABLE " + relation + " ENABLE ROW LEVEL SECURITY",
			"ALTER TABLE " + relation + " FORCE ROW LEVEL SECURITY",
			"DROP POLICY IF EXISTS " + policy + " ON " + relation,
			"CREATE POLICY " + policy + " ON " + relation + " FOR ALL TO " + role +
				" USING (" + renderer.Identifier("workspace_id") + "::text = NULLIF(current_setting('domainry.workspace_id', true), ''))" +
				" WITH CHECK (" + renderer.Identifier("workspace_id") + "::text = NULLIF(current_setting('domainry.workspace_id', true), ''))",
		}
		for _, statement := range statements {
			if _, err := database.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply workspace RLS policy to %s: %w", table, err)
			}
		}
	}
	const ledgerTable = "_runtime_rls_policies"
	if _, err := database.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+renderer.Table(ledgerTable)+" ("+renderer.Identifier("version")+" TEXT PRIMARY KEY, "+renderer.Identifier("applied_at")+" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP)"); err != nil {
		return fmt.Errorf("prepare workspace RLS policy ledger: %w", err)
	}
	statement, arguments, err := ormbuilder.NewInsertBuilder(renderer, ledgerTable).Columns("version").Values(policyVersion).OnConflictDoNothing("version").Build()
	if err != nil {
		return fmt.Errorf("build workspace RLS policy ledger insert: %w", err)
	}
	if _, err := database.ExecContext(ctx, statement, arguments...); err != nil {
		return fmt.Errorf("record workspace RLS policy version: %w", err)
	}
	return nil
}

func (engineProfile) InspectWorkspaceRLS(ctx context.Context, database *sql.DB, renderer ormdialect.Renderer, databaseSchema, runtimeRole, policyVersion string) (persistencedriver.WorkspaceRLSStatus, error) {
	tables, err := postgresWorkspaceTables(ctx, database, renderer, databaseSchema)
	if err != nil {
		return persistencedriver.WorkspaceRLSStatus{}, err
	}
	status := persistencedriver.WorkspaceRLSStatus{Enabled: true, Forced: true, RuntimeRole: runtimeRole, PolicyVersion: policyVersion}
	policyName := "domainry_workspace_" + policyVersion
	for _, table := range tables {
		var enabled, forced, roleOwnsTable, roleBypassRLS, policyExists bool
		err := database.QueryRowContext(ctx, `SELECT c.relrowsecurity, c.relforcerowsecurity,
c.relowner = (SELECT oid FROM pg_roles WHERE rolname = current_user),
COALESCE((SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user), true),
EXISTS (SELECT 1 FROM pg_policies WHERE schemaname = $1 AND tablename = $2 AND policyname = $3 AND (current_user = ANY(roles) OR 'public' = ANY(roles)))
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2`, databaseSchema, table, policyName).Scan(&enabled, &forced, &roleOwnsTable, &roleBypassRLS, &policyExists)
		if err != nil {
			return persistencedriver.WorkspaceRLSStatus{}, fmt.Errorf("inspect workspace RLS policy for %s: %w", table, err)
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
	if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+renderer.Table("_runtime_rls_policies")+" WHERE "+renderer.Identifier("version")+" = "+renderer.Placeholder(1), policyVersion).Scan(&versionCount); err != nil {
		return persistencedriver.WorkspaceRLSStatus{}, fmt.Errorf("verify workspace RLS policy version %s: %w", policyVersion, err)
	}
	if versionCount != 1 {
		return persistencedriver.WorkspaceRLSStatus{}, fmt.Errorf("verify workspace RLS policy version %s: missing ledger entry", policyVersion)
	}
	return status, nil
}

func postgresWorkspaceTables(ctx context.Context, database *sql.DB, renderer ormdialect.Renderer, databaseSchema string) ([]string, error) {
	query := "SELECT DISTINCT table_name FROM information_schema.columns WHERE table_schema = " + renderer.Placeholder(1) + " AND column_name = 'workspace_id' ORDER BY table_name"
	rows, err := database.QueryContext(ctx, query, databaseSchema)
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
// PostgreSQL transaction so pooled connections cannot leak either value.
func SetLocalWorkspaceRLSContext(ctx context.Context, transaction *sql.Tx, renderer ormdialect.Renderer) error {
	if transaction == nil {
		return fmt.Errorf("workspace RLS transaction is required")
	}
	workspaceID := requestcontext.WorkspaceID(ctx)
	if strings.TrimSpace(workspaceID) == "" {
		return fmt.Errorf("workspace RLS context requires workspace_id")
	}
	statement := "SELECT set_config('domainry.workspace_id', " + renderer.Placeholder(1) + ", true), set_config('domainry.actor_id', " + renderer.Placeholder(2) + ", true)"
	if _, err := transaction.ExecContext(ctx, statement, workspaceID, requestcontext.ActorID(ctx)); err != nil {
		return fmt.Errorf("set transaction-local workspace RLS context: %w", err)
	}
	return nil
}
