package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/requestcontext"
	postgrespersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	postgresrls "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres/rls"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestWorkspaceRLSStatusCopiesSlices(t *testing.T) {
	store := &RuntimeStore{workspaceRLS: WorkspaceRLSStatus{Enabled: true, CoveredTables: []string{"records"}, MissingTables: []string{"audit"}}}
	status := store.WorkspaceRLSStatus(t.Context())
	status.CoveredTables[0], status.MissingTables[0] = "changed", "changed"
	if store.workspaceRLS.CoveredTables[0] != "records" || store.workspaceRLS.MissingTables[0] != "audit" {
		t.Fatalf("store status mutated=%#v", store.workspaceRLS)
	}
}

func TestEnsureWorkspaceRLSGuardsAndSuccess(t *testing.T) {
	if err := (*RuntimeStore)(nil).EnsureWorkspaceRLS(t.Context()); err != nil {
		t.Fatalf("nil store: %v", err)
	}
	sqliteDialect, _ := dialectFor("sqlite")
	store := &RuntimeStore{dialect: sqliteDialect, config: config.Config{DatabaseRLSEnabled: true}, workspaceRLS: WorkspaceRLSStatus{Enabled: true}}
	if err := store.EnsureWorkspaceRLS(t.Context()); err != nil || store.workspaceRLS.Enabled {
		t.Fatalf("sqlite status=%#v err=%v", store.workspaceRLS, err)
	}
	postgresDialect, _ := dialectFor("postgres")
	store = &RuntimeStore{dialect: postgresDialect, workspaceRLS: WorkspaceRLSStatus{Enabled: true}}
	if err := store.EnsureWorkspaceRLS(t.Context()); err != nil || store.workspaceRLS.Enabled {
		t.Fatalf("disabled status=%#v err=%v", store.workspaceRLS, err)
	}
	store.config.DatabaseRLSEnabled = true
	if err := store.EnsureWorkspaceRLS(t.Context()); err == nil || !strings.Contains(err.Error(), "connection profile") {
		t.Fatalf("profile error=%v", err)
	}
	store.postgresProfile = &postgrespersistence.ConnectionProfile{}
	store.config.DatabaseMigrationMode = "apply"
	if err := store.EnsureWorkspaceRLS(t.Context()); err == nil || !strings.Contains(err.Error(), "migration connection") {
		t.Fatalf("migration error=%v", err)
	}

	script := &workspaceRLSScript{tables: []string{"audit_events", "records"}, policies: map[string]workspaceRLSPolicyFacts{"audit_events": {enabled: true, forced: true, policy: true}, "records": {enabled: true, forced: true, policy: true}}, ledgerCount: 1}
	db := sql.OpenDB(workspaceRLSConnector{script: script})
	defer db.Close()
	store = newWorkspaceRLSTestStore(postgresDialect, db, db, "apply")
	if err := store.EnsureWorkspaceRLS(t.Context()); err != nil {
		t.Fatalf("apply RLS: %v", err)
	}
	status := store.WorkspaceRLSStatus(t.Context())
	if !status.Enabled || !status.Forced || status.RuntimeRole != "runtime_user" || status.RoleOwnsTable || status.RoleBypassRLS || status.PolicyVersion != CurrentWorkspaceRLSPolicyVersion || len(status.CoveredTables) != 2 || len(status.MissingTables) != 0 || len(script.execs) != 10 {
		t.Fatalf("status=%#v execs=%#v", status, script.execs)
	}

	verifyScript := &workspaceRLSScript{tables: []string{"records"}, policies: map[string]workspaceRLSPolicyFacts{"records": {enabled: true, forced: true, policy: true}}, ledgerCount: 1}
	verifyDB := sql.OpenDB(workspaceRLSConnector{script: verifyScript})
	defer verifyDB.Close()
	store = newWorkspaceRLSTestStore(postgresDialect, verifyDB, nil, "verify")
	if err := store.EnsureWorkspaceRLS(t.Context()); err != nil || len(verifyScript.execs) != 0 {
		t.Fatalf("verify execs=%#v err=%v", verifyScript.execs, err)
	}
}

func TestWorkspaceRLSApplyFailures(t *testing.T) {
	postgresDialect, _ := dialectFor("postgres")
	for _, test := range []struct {
		name         string
		queryErr     error
		failContains string
		want         string
	}{
		{name: "inventory", queryErr: errors.New("catalog down"), want: "inventory workspace RLS tables"},
		{name: "table statement", failContains: "ENABLE ROW LEVEL SECURITY", want: "apply workspace RLS policy"},
		{name: "ledger table", failContains: "CREATE TABLE IF NOT EXISTS", want: "prepare workspace RLS policy ledger"},
		{name: "ledger version", failContains: "INSERT INTO", want: "record workspace RLS policy version"},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := &workspaceRLSScript{tables: []string{"records"}, policies: map[string]workspaceRLSPolicyFacts{"records": {enabled: true, forced: true, policy: true}}, ledgerCount: 1, queryErr: test.queryErr, failExecContains: test.failContains}
			db := sql.OpenDB(workspaceRLSConnector{script: script})
			defer db.Close()
			store := newWorkspaceRLSTestStore(postgresDialect, db, db, "apply")
			if err := store.EnsureWorkspaceRLS(t.Context()); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want=%q", err, test.want)
			}
		})
	}
}

func TestWorkspaceRLSInspectionFailuresAndMissingCoverage(t *testing.T) {
	postgresDialect, _ := dialectFor("postgres")
	for _, test := range []struct {
		name   string
		script *workspaceRLSScript
		want   string
	}{
		{name: "inventory", script: &workspaceRLSScript{queryErr: errors.New("catalog down")}, want: "inventory workspace RLS tables"},
		{name: "policy query", script: &workspaceRLSScript{tables: []string{"records"}, policyErr: errors.New("policy down")}, want: "inspect workspace RLS policy"},
		{name: "ledger query", script: &workspaceRLSScript{tables: []string{"records"}, policies: map[string]workspaceRLSPolicyFacts{"records": {enabled: true, forced: true, policy: true}}, ledgerErr: errors.New("ledger down")}, want: "verify workspace RLS policy version"},
		{name: "ledger missing", script: &workspaceRLSScript{tables: []string{"records"}, policies: map[string]workspaceRLSPolicyFacts{"records": {enabled: true, forced: true, policy: true}}}, want: "missing ledger entry"},
		{name: "table missing", script: &workspaceRLSScript{tables: []string{"records"}, policies: map[string]workspaceRLSPolicyFacts{"records": {enabled: true, forced: true}}, ledgerCount: 1}, want: "coverage is incomplete: records"},
		{name: "rls disabled", script: &workspaceRLSScript{tables: []string{"records"}, policies: map[string]workspaceRLSPolicyFacts{"records": {forced: true, policy: true}}, ledgerCount: 1}, want: "coverage is incomplete: records"},
		{name: "rls not forced", script: &workspaceRLSScript{tables: []string{"records"}, policies: map[string]workspaceRLSPolicyFacts{"records": {enabled: true, policy: true}}, ledgerCount: 1}, want: "coverage is incomplete: records"},
		{name: "runtime role owns table", script: &workspaceRLSScript{tables: []string{"records"}, policies: map[string]workspaceRLSPolicyFacts{"records": {enabled: true, forced: true, owner: true, policy: true}}, ledgerCount: 1}, want: "coverage is incomplete: records"},
		{name: "runtime role bypasses RLS", script: &workspaceRLSScript{tables: []string{"records"}, policies: map[string]workspaceRLSPolicyFacts{"records": {enabled: true, forced: true, bypass: true, policy: true}}, ledgerCount: 1}, want: "coverage is incomplete: records"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := sql.OpenDB(workspaceRLSConnector{script: test.script})
			defer db.Close()
			store := newWorkspaceRLSTestStore(postgresDialect, db, nil, "verify")
			if err := store.EnsureWorkspaceRLS(t.Context()); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want=%q", err, test.want)
			}
		})
	}
}

func TestWorkspaceTablesScanAndRowsErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		script *workspaceRLSScript
	}{
		{name: "scan", script: &workspaceRLSScript{tableValues: [][]driver.Value{{nil}}}},
		{name: "rows", script: &workspaceRLSScript{tables: []string{"records"}, rowsErr: errors.New("stream interrupted")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := sql.OpenDB(workspaceRLSConnector{script: test.script})
			defer db.Close()
			profile := postgrespersistence.Dialect{}.EngineProfile()
			renderer := postgrespersistence.Dialect{}.SQLDialect().WithSchema("public")
			if _, err := profile.InspectWorkspaceRLS(t.Context(), db, renderer, "public", "runtime_user", CurrentWorkspaceRLSPolicyVersion); err == nil {
				t.Fatal("expected workspace table error")
			}
		})
	}
}

func TestSetLocalWorkspaceRLSContext(t *testing.T) {
	renderer := postgrespersistence.Dialect{}.SQLDialect().WithSchema("")
	if err := postgresrls.SetLocalWorkspaceContext(t.Context(), nil, renderer); err == nil {
		t.Fatal("expected nil transaction error")
	}
	script := &workspaceRLSScript{}
	db := sql.OpenDB(workspaceRLSConnector{script: script})
	defer db.Close()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := requestcontext.WithActorID(requestcontext.WithWorkspaceID(t.Context(), "workspace-1"), "actor-1")
	if err := postgresrls.SetLocalWorkspaceContext(ctx, tx, renderer); err != nil {
		t.Fatalf("set context: %v", err)
	}
	_ = tx.Rollback()
	if len(script.execArgs) != 1 || len(script.execArgs[0]) != 2 || script.execArgs[0][0].Value != "workspace-1" || script.execArgs[0][1].Value != "actor-1" {
		t.Fatalf("exec args=%#v", script.execArgs)
	}

	script = &workspaceRLSScript{failExecContains: "set_config"}
	db = sql.OpenDB(workspaceRLSConnector{script: script})
	defer db.Close()
	tx, _ = db.BeginTx(t.Context(), nil)
	defer tx.Rollback()
	if err := postgresrls.SetLocalWorkspaceContext(ctx, tx, renderer); err == nil || !strings.Contains(err.Error(), "transaction-local") {
		t.Fatalf("set context error=%v", err)
	}
}

func newWorkspaceRLSTestStore(dialect dialect, db, migrationDB *sql.DB, mode string) *RuntimeStore {
	return &RuntimeStore{db: db, migrationDB: migrationDB, dialect: dialect, databaseSchema: "public", config: config.Config{DatabaseRLSEnabled: true, DatabaseMigrationMode: mode}, postgresProfile: &postgrespersistence.ConnectionProfile{}, postgresCapabilities: postgrespersistence.Capabilities{User: "runtime_user"}}
}

type workspaceRLSScript struct {
	tables           []string
	tableValues      [][]driver.Value
	policies         map[string]workspaceRLSPolicyFacts
	ledgerCount      int
	queryErr         error
	policyErr        error
	ledgerErr        error
	rowsErr          error
	failExecContains string
	execs            []string
	execArgs         [][]driver.NamedValue
}

type workspaceRLSConnector struct{ script *workspaceRLSScript }

type workspaceRLSPolicyFacts struct {
	enabled bool
	forced  bool
	owner   bool
	bypass  bool
	policy  bool
}

func (c workspaceRLSConnector) Connect(context.Context) (driver.Conn, error) {
	return &workspaceRLSConn{script: c.script}, nil
}
func (workspaceRLSConnector) Driver() driver.Driver { return workspaceRLSDriver{} }

type workspaceRLSDriver struct{}

func (workspaceRLSDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type workspaceRLSConn struct{ script *workspaceRLSScript }

func (*workspaceRLSConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*workspaceRLSConn) Close() error                        { return nil }
func (*workspaceRLSConn) Begin() (driver.Tx, error)           { return workspaceRLSTx{}, nil }
func (*workspaceRLSConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return workspaceRLSTx{}, nil
}
func (c *workspaceRLSConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.script.execs = append(c.script.execs, query)
	c.script.execArgs = append(c.script.execArgs, append([]driver.NamedValue(nil), args...))
	if c.script.failExecContains != "" && strings.Contains(query, c.script.failExecContains) {
		return nil, errors.New("scripted exec failure")
	}
	return driver.RowsAffected(1), nil
}
func (c *workspaceRLSConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "information_schema.columns") {
		if c.script.queryErr != nil {
			return nil, c.script.queryErr
		}
		values := c.script.tableValues
		if values == nil {
			for _, table := range c.script.tables {
				values = append(values, []driver.Value{table})
			}
		}
		return &workspaceRLSRows{columns: []string{"table_name"}, values: values, finalErr: c.script.rowsErr}, nil
	}
	if strings.Contains(query, "pg_policies") {
		if c.script.policyErr != nil {
			return nil, c.script.policyErr
		}
		table, _ := args[1].Value.(string)
		policy := c.script.policies[table]
		return &workspaceRLSRows{columns: []string{"enabled", "forced", "owner", "bypass", "policy"}, values: [][]driver.Value{{policy.enabled, policy.forced, policy.owner, policy.bypass, policy.policy}}}, nil
	}
	if strings.Contains(query, "_runtime_rls_policies") {
		if c.script.ledgerErr != nil {
			return nil, c.script.ledgerErr
		}
		return &workspaceRLSRows{columns: []string{"count"}, values: [][]driver.Value{{int64(c.script.ledgerCount)}}}, nil
	}
	return nil, errors.New("unexpected query")
}

type workspaceRLSTx struct{}

func (workspaceRLSTx) Commit() error   { return nil }
func (workspaceRLSTx) Rollback() error { return nil }

type workspaceRLSRows struct {
	columns  []string
	values   [][]driver.Value
	index    int
	finalErr error
}

func (r *workspaceRLSRows) Columns() []string { return r.columns }
func (r *workspaceRLSRows) Close() error      { return nil }
func (r *workspaceRLSRows) Next(dest []driver.Value) error {
	if r.index < len(r.values) {
		copy(dest, r.values[r.index])
		r.index++
		return nil
	}
	if r.finalErr != nil {
		err := r.finalErr
		r.finalErr = nil
		return err
	}
	return io.EOF
}
