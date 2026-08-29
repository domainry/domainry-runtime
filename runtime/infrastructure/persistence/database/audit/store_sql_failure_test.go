package audit

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
)

func TestAuditListAndOptionStagedSQLFailures(t *testing.T) {
	base := openAuditEdgeStore(t)
	wantErr := errors.New("injected audit query failure")
	auditColumns := []string{"id", "workspace_id", "event", "object_key", "record_id", "actor_id", "role_key", "summary", "metadata_json", "before_json", "after_json", "created_at"}
	optionColumns := []string{"value", "count"}
	for _, test := range []struct {
		name    string
		step    auditQueryStep
		options bool
	}{
		{name: "events query", step: auditQueryStep{err: wantErr}},
		{name: "events scan", step: auditQueryStep{columns: append(auditColumns, "extra"), rows: [][]driver.Value{{"id", "default", "event", "object", "record", "actor", "role", "summary", `{}`, `{}`, `{}`, "created", "extra"}}}},
		{name: "events rows", step: auditQueryStep{columns: auditColumns, nextErr: wantErr}},
		{name: "options query", step: auditQueryStep{err: wantErr}, options: true},
		{name: "options scan", step: auditQueryStep{columns: append(optionColumns, "extra"), rows: [][]driver.Value{{"actor", int64(1), "extra"}}}, options: true},
		{name: "options rows", step: auditQueryStep{columns: optionColumns, nextErr: wantErr}, options: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := sql.OpenDB(auditConnector{state: &auditDBState{querySteps: []auditQueryStep{test.step}}})
			defer db.Close()
			store := NewAuditStore(base)
			store.db = db
			if test.options {
				if _, err := store.ListAuditOptions(t.Context(), "default", auditmodel.AuditOptionQuery{Field: "actor_id"}); err == nil {
					t.Fatal("option SQL failure ignored")
				}
			} else if _, err := store.ListAuditEvents(t.Context(), "default", auditmodel.AuditEventQuery{}); err == nil {
				t.Fatal("event SQL failure ignored")
			}
		})
	}
}

func TestAuditCursorSQLMatchesExactIndexPrefixAndStableOrder(t *testing.T) {
	base := openAuditEdgeStore(t)
	if err := base.SetDialectForTesting("mysql"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		query auditmodel.AuditEventQuery
		want  []string
	}{
		{
			name:  "record",
			query: auditmodel.AuditEventQuery{ObjectKey: "customer", RecordID: "record-1", Limit: 10},
			want:  []string{"`workspace_id` = ?", "`object_key` = ?", "`record_id` = ?", "ORDER BY `created_at` DESC, `id` DESC LIMIT ?"},
		},
		{
			name:  "actor with cursor",
			query: auditmodel.AuditEventQuery{ActorID: "actor-1", Cursor: auditmodel.EncodeAuditEventCursor(auditmodel.AuditEvent{ID: "audit-1", CreatedAt: "2026-08-18T12:00:00Z"}), Limit: 10},
			want:  []string{"`workspace_id` = ?", "`actor_id` = ?", "(`created_at` < ? OR (`created_at` = ? AND `id` < ?))", "ORDER BY `created_at` DESC, `id` DESC LIMIT ?"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &auditDBState{}
			db := sql.OpenDB(auditConnector{state: state})
			defer db.Close()
			store := NewAuditStore(base)
			store.db = db
			if _, err := store.ListAuditEvents(t.Context(), "workspace-1", test.query); err != nil {
				t.Fatal(err)
			}
			if len(state.queries) != 1 {
				t.Fatalf("queries=%v", state.queries)
			}
			generated := state.queries[0]
			position := -1
			for _, fragment := range test.want {
				next := strings.Index(generated, fragment)
				if next < 0 || next <= position {
					t.Fatalf("query does not preserve exact prefix/order fragment %q: %s", fragment, generated)
				}
				position = next
			}
			if strings.Contains(generated, "record_id`(") || strings.Contains(generated, "actor_id`(") {
				t.Fatalf("query unexpectedly relies on prefix matching: %s", generated)
			}
		})
	}
}

func TestAuditFilterSQLUsesDialectConcatAndPortableLikeEscape(t *testing.T) {
	for _, test := range []struct {
		driver       string
		requiredSQL  []string
		forbiddenSQL string
	}{
		{driver: "mysql", requiredSQL: []string{"LOWER(CONCAT(COALESCE(`event`, ?), ?, COALESCE(`object_key`, ?)))", "ESCAPE '~'"}, forbiddenSQL: " || "},
		{driver: "sqlite", requiredSQL: []string{"LOWER((COALESCE(\"event\", ?)", " || ", "ESCAPE '~'"}, forbiddenSQL: "CONCAT("},
		{driver: "postgres", requiredSQL: []string{"LOWER((COALESCE(\"event\", $", " || ", "ESCAPE '~'"}, forbiddenSQL: "CONCAT("},
	} {
		t.Run(test.driver, func(t *testing.T) {
			base := openAuditEdgeStore(t)
			if err := base.SetDialectForTesting(test.driver); err != nil {
				t.Fatal(err)
			}
			state := &auditDBState{}
			db := sql.OpenDB(auditConnector{state: state})
			defer db.Close()
			store := NewAuditStore(base)
			store.db = db
			if _, err := store.ListAuditEvents(t.Context(), "workspace-1", auditmodel.AuditEventQuery{
				Class: auditmodel.AuditEventClassBusiness, RequestID: `req-%_\literal~`, Limit: 10,
			}); err != nil {
				t.Fatal(err)
			}
			if len(state.queries) != 1 {
				t.Fatalf("queries=%v", state.queries)
			}
			generated := state.queries[0]
			for _, required := range test.requiredSQL {
				if !strings.Contains(generated, required) {
					t.Fatalf("dialect-safe audit filter %q missing from query: %s", required, generated)
				}
			}
			if strings.Contains(generated, test.forbiddenSQL) || strings.Contains(generated, "ESCAPE '\\\\'") {
				t.Fatalf("query contains incompatible SQL: %s", generated)
			}
		})
	}
}

func TestAuditSubjectLifecycleStagedRowFailures(t *testing.T) {
	base := openAuditEdgeStore(t)
	wantErr := errors.New("injected audit lifecycle failure")
	columns := []string{"id", "event", "object_key", "record_id", "summary", "created_at"}
	for _, step := range []auditQueryStep{
		{columns: append(columns, "extra"), rows: [][]driver.Value{{"id", "event", "object", "record", "summary", "created", "extra"}}},
		{columns: columns, nextErr: wantErr},
	} {
		db := sql.OpenDB(auditConnector{state: &auditDBState{querySteps: []auditQueryStep{step}}})
		lifecycle := &AuditSubjectLifecycleStore{db: db, renderer: base.SQLRenderer}
		if _, err := lifecycle.ExportSubject(t.Context(), "default", "subject"); err == nil {
			t.Fatal("lifecycle row failure ignored")
		}
		_ = db.Close()
	}
	db := sql.OpenDB(auditConnector{state: &auditDBState{execStep: auditExecStep{rowsErr: wantErr}}})
	lifecycle := &AuditSubjectLifecycleStore{db: db, renderer: base.SQLRenderer}
	if _, err := lifecycle.EraseSubject(t.Context(), "default", "subject", nil); !errors.Is(err, wantErr) {
		t.Fatalf("rows affected error=%v", err)
	}
	_ = db.Close()
}

type auditQueryStep struct {
	columns []string
	rows    [][]driver.Value
	err     error
	nextErr error
}
type auditExecStep struct {
	rows    int64
	err     error
	rowsErr error
}
type auditDBState struct {
	querySteps []auditQueryStep
	execStep   auditExecStep
	queries    []string
}
type auditConnector struct{ state *auditDBState }

func (c auditConnector) Connect(context.Context) (driver.Conn, error) {
	return &auditConn{state: c.state}, nil
}
func (auditConnector) Driver() driver.Driver { return auditDriver{} }

type auditDriver struct{}

func (auditDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type auditConn struct{ state *auditDBState }

func (*auditConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*auditConn) Close() error                        { return nil }
func (*auditConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }
func (c *auditConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.state.queries = append(c.state.queries, query)
	if len(c.state.querySteps) == 0 {
		return &auditRows{}, nil
	}
	step := c.state.querySteps[0]
	c.state.querySteps = c.state.querySteps[1:]
	if step.err != nil {
		return nil, step.err
	}
	return &auditRows{columns: step.columns, rows: step.rows, nextErr: step.nextErr}, nil
}
func (c *auditConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return auditResult{rows: c.state.execStep.rows, err: c.state.execStep.rowsErr}, c.state.execStep.err
}

type auditResult struct {
	rows int64
	err  error
}

func (r auditResult) LastInsertId() (int64, error) { return 0, nil }
func (r auditResult) RowsAffected() (int64, error) { return r.rows, r.err }

type auditRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
	nextErr error
}

func (r *auditRows) Columns() []string { return r.columns }
func (*auditRows) Close() error        { return nil }
func (r *auditRows) Next(values []driver.Value) error {
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
