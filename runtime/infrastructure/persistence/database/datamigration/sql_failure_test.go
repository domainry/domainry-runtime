package datamigration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCopyBatchStagedSQLFailures(t *testing.T) {
	wantErr := errors.New("injected migration SQL failure")
	table := TableInventory{Name: "records", Columns: []ColumnInventory{{Name: "id"}}, PrimaryKey: []string{"id"}}
	plan := TablePlan{Name: "records", CheckpointKey: []string{"id"}, Conversions: []ConversionPlan{{Column: "id", Strategy: "identity"}}}
	newCopier := func(sourceState, targetState *migrationSQLState) (Copier, func()) {
		source := sql.OpenDB(migrationSQLConnector{state: sourceState})
		target := sql.OpenDB(migrationSQLConnector{state: targetState})
		return Copier{Source: source, Target: target, SourceEngine: EngineSQLite, TargetSchema: "runtime", Plan: Plan{Source: Inventory{Tables: []TableInventory{table}}}}, func() {
			_ = source.Close()
			_ = target.Close()
		}
	}
	tests := []struct {
		name   string
		source *migrationSQLState
		target *migrationSQLState
	}{
		{name: "source query", source: &migrationSQLState{querySteps: []migrationQueryStep{{err: wantErr}}}, target: &migrationSQLState{}},
		{name: "target begin", source: &migrationSQLState{querySteps: []migrationQueryStep{{columns: []string{"id"}}}}, target: &migrationSQLState{beginErrors: []error{wantErr}}},
		{name: "scan", source: &migrationSQLState{querySteps: []migrationQueryStep{{columns: []string{"id", "extra"}, rows: [][]driver.Value{{"1", "extra"}}}}}, target: &migrationSQLState{}},
		{name: "upsert", source: &migrationSQLState{querySteps: []migrationQueryStep{{columns: []string{"id"}, rows: [][]driver.Value{{"1"}}}}}, target: &migrationSQLState{execErrors: []error{wantErr}}},
		{name: "rows terminal", source: &migrationSQLState{querySteps: []migrationQueryStep{{columns: []string{"id"}, nextErr: wantErr}}}, target: &migrationSQLState{}},
		{name: "commit", source: &migrationSQLState{querySteps: []migrationQueryStep{{columns: []string{"id"}, rows: [][]driver.Value{{"1"}}}}}, target: &migrationSQLState{commitErrors: []error{wantErr}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copier, closeDB := newCopier(test.source, test.target)
			defer closeDB()
			if _, _, err := copier.copyBatch(t.Context(), plan, TableCheckpoint{}, 1); err == nil {
				t.Fatal("staged SQL failure was ignored")
			}
		})
	}
	source := sql.OpenDB(migrationSQLConnector{state: &migrationSQLState{querySteps: []migrationQueryStep{{columns: []string{"id"}, rows: [][]driver.Value{{int64(2)}}}}}})
	target := sql.OpenDB(migrationSQLConnector{state: &migrationSQLState{}})
	defer source.Close()
	defer target.Close()
	copier := Copier{Source: source, Target: target, TargetSchema: "runtime", Plan: Plan{Source: Inventory{Tables: []TableInventory{table}}}}
	badConversion := plan
	badConversion.Conversions = []ConversionPlan{{Column: "id", Strategy: "sqlite_zero_one_to_boolean"}}
	if _, _, err := copier.copyBatch(t.Context(), badConversion, TableCheckpoint{}, 1); err == nil || !strings.Contains(err.Error(), "convert records row") {
		t.Fatalf("conversion stage error=%v", err)
	}
}

func TestVerificationStagedScanRowsAndReferenceFailures(t *testing.T) {
	wantErr := errors.New("injected verification SQL failure")
	table := TableInventory{Name: "records", Columns: []ColumnInventory{{Name: "id"}}, PrimaryKey: []string{"id"}}
	plan := TablePlan{Name: "records", CheckpointKey: []string{"id"}, Conversions: []ConversionPlan{{Column: "id", Strategy: "identity"}}}
	for _, step := range []migrationQueryStep{
		{columns: []string{"id", "extra"}, rows: [][]driver.Value{{"1", "extra"}}},
		{columns: []string{"id"}, nextErr: wantErr},
	} {
		db := sql.OpenDB(migrationSQLConnector{state: &migrationSQLState{querySteps: []migrationQueryStep{step}}})
		copier := Copier{Source: db, Plan: Plan{Source: Inventory{Tables: []TableInventory{table}}}}
		if _, err := copier.digestTable(t.Context(), db, EngineSQLite, "", plan, false); err == nil {
			t.Fatal("verification row failure was ignored")
		}
		_ = db.Close()
	}
	table.ForeignKeys = []ForeignInventory{{Columns: []string{"id"}, ReferencedTable: "parents", ReferencedColumns: []string{"id"}}}
	db := sql.OpenDB(migrationSQLConnector{state: &migrationSQLState{querySteps: []migrationQueryStep{{columns: []string{"id"}}, {err: wantErr}}}})
	copier := Copier{Source: db, Plan: Plan{Source: Inventory{Tables: []TableInventory{table}}}}
	if _, err := copier.digestTable(t.Context(), db, EngineSQLite, "", plan, false); !errors.Is(err, wantErr) {
		t.Fatalf("reference error=%v", err)
	}
	_ = db.Close()
}

func TestCopyBatchCheckpointKeyAbsentAfterSuccessfulRead(t *testing.T) {
	sourceState := &migrationSQLState{querySteps: []migrationQueryStep{{columns: []string{"workspace_id"}, rows: [][]driver.Value{{"workspace"}}}}}
	targetState := &migrationSQLState{}
	source := sql.OpenDB(migrationSQLConnector{state: sourceState})
	target := sql.OpenDB(migrationSQLConnector{state: targetState})
	defer source.Close()
	defer target.Close()
	table := TableInventory{Name: "records", Columns: []ColumnInventory{{Name: "workspace_id"}}}
	copier := Copier{Source: source, Target: target, TargetSchema: "runtime", Plan: Plan{Source: Inventory{Tables: []TableInventory{table}}}}
	_, _, err := copier.copyBatch(t.Context(), TablePlan{Name: "records", CheckpointKey: []string{"id"}, Conversions: []ConversionPlan{{Column: "workspace_id", Strategy: "identity"}}}, TableCheckpoint{}, 1)
	if err == nil || !strings.Contains(err.Error(), "checkpoint key") {
		t.Fatalf("missing key error=%v", err)
	}
}

type migrationQueryStep struct {
	columns []string
	rows    [][]driver.Value
	err     error
	nextErr error
}

type migrationSQLState struct {
	beginErrors  []error
	execErrors   []error
	commitErrors []error
	querySteps   []migrationQueryStep
}

type migrationSQLConnector struct{ state *migrationSQLState }

func (c migrationSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &migrationSQLConn{state: c.state}, nil
}
func (migrationSQLConnector) Driver() driver.Driver { return migrationSQLDriver{} }

type migrationSQLDriver struct{}

func (migrationSQLDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type migrationSQLConn struct{ state *migrationSQLState }

func (*migrationSQLConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*migrationSQLConn) Close() error                        { return nil }
func (c *migrationSQLConn) Begin() (driver.Tx, error)         { return c.begin() }
func (c *migrationSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.begin()
}
func (c *migrationSQLConn) begin() (driver.Tx, error) {
	if len(c.state.beginErrors) > 0 {
		err := c.state.beginErrors[0]
		c.state.beginErrors = c.state.beginErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	return &migrationSQLTx{state: c.state}, nil
}
func (c *migrationSQLConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	if len(c.state.execErrors) == 0 {
		return driver.RowsAffected(1), nil
	}
	err := c.state.execErrors[0]
	c.state.execErrors = c.state.execErrors[1:]
	return driver.RowsAffected(0), err
}
func (c *migrationSQLConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if len(c.state.querySteps) == 0 {
		return &migrationSQLRows{columns: []string{"value"}}, nil
	}
	step := c.state.querySteps[0]
	c.state.querySteps = c.state.querySteps[1:]
	if step.err != nil {
		return nil, step.err
	}
	return &migrationSQLRows{columns: step.columns, rows: step.rows, nextErr: step.nextErr}, nil
}

type migrationSQLTx struct{ state *migrationSQLState }

func (tx *migrationSQLTx) Commit() error {
	if len(tx.state.commitErrors) == 0 {
		return nil
	}
	err := tx.state.commitErrors[0]
	tx.state.commitErrors = tx.state.commitErrors[1:]
	return err
}
func (*migrationSQLTx) Rollback() error { return nil }

type migrationSQLRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
	nextErr error
}

func (r *migrationSQLRows) Columns() []string { return r.columns }
func (*migrationSQLRows) Close() error        { return nil }
func (r *migrationSQLRows) Next(values []driver.Value) error {
	if r.index < len(r.rows) {
		copy(values, r.rows[r.index])
		r.index++
		return nil
	}
	if r.nextErr != nil {
		err := r.nextErr
		r.nextErr = nil
		return err
	}
	return io.EOF
}
