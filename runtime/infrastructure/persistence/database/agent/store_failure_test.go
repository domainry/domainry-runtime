package agent

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestAgentStateStoreValidationSchemaAndWriteStages(t *testing.T) {
	base := openAgentStateBaseStore(t)
	record := agentmodel.AgentStateRecord{Kind: "session", Key: "key", WorkspaceID: "default", UserID: "user", RoleKey: "role", Payload: []byte(`{"ok":true}`), UpdatedAt: 1}
	wantErr := errors.New("injected agent state failure")

	repository := NewAgentStateStore(base)
	if err := repository.PutBatch(t.Context(), "", nil); err == nil {
		t.Fatal("empty workspace put accepted")
	}
	if _, _, err := repository.Get(t.Context(), "", "session", "key"); err == nil {
		t.Fatal("empty workspace get accepted")
	}
	if _, err := repository.List(t.Context(), "", "session", "", ""); err == nil {
		t.Fatal("empty workspace list accepted")
	}

	t.Run("mysql schema", func(t *testing.T) {
		state := &agentStateDBState{execErrors: []error{nil}}
		candidate, closeDB := scriptedAgentStateStore(base, state)
		defer closeDB()
		candidate.driver = "mysql"
		if err := candidate.EnsureSchema(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(state.queries) != 1 || !strings.Contains(state.queries[0], "VARCHAR(255)") {
			t.Fatalf("schema queries=%v", state.queries)
		}
	})

	for _, operation := range []string{"put", "get", "list"} {
		t.Run(operation+" schema", func(t *testing.T) {
			candidate, closeDB := scriptedAgentStateStore(base, &agentStateDBState{execErrors: []error{wantErr}})
			defer closeDB()
			switch operation {
			case "put":
				if err := candidate.Put(t.Context(), "default", record); err == nil || !strings.Contains(err.Error(), "ensure agent state table") {
					t.Fatalf("put schema error=%v", err)
				}
			case "get":
				if _, _, err := candidate.Get(t.Context(), "default", "session", "key"); !errors.Is(err, wantErr) {
					t.Fatalf("get schema error=%v", err)
				}
			case "list":
				if _, err := candidate.List(t.Context(), "default", "session", "", ""); !errors.Is(err, wantErr) {
					t.Fatalf("list schema error=%v", err)
				}
			}
		})
	}

	tests := []struct {
		name  string
		state *agentStateDBState
		value []agentmodel.AgentStateRecord
	}{
		{name: "begin", state: &agentStateDBState{execErrors: []error{nil}, beginErrors: []error{wantErr}}, value: []agentmodel.AgentStateRecord{record}},
		{name: "upsert", state: &agentStateDBState{execErrors: []error{nil, wantErr}}, value: []agentmodel.AgentStateRecord{record}},
		{name: "commit", state: &agentStateDBState{execErrors: []error{nil, nil}, commitErrors: []error{wantErr}}, value: []agentmodel.AgentStateRecord{record}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate, closeDB := scriptedAgentStateStore(base, test.state)
			defer closeDB()
			if err := candidate.PutBatch(t.Context(), "default", test.value); !errors.Is(err, wantErr) {
				t.Fatalf("write stage error=%v", err)
			}
		})
	}

	candidate, closeDB := scriptedAgentStateStore(base, &agentStateDBState{execErrors: []error{nil}})
	defer closeDB()
	if err := candidate.PutBatch(t.Context(), "default", nil); err != nil {
		t.Fatalf("empty batch=%v", err)
	}
}

func TestAgentStateStorePutBatchUsesChunkedMultiRowUpserts(t *testing.T) {
	base := openAgentStateBaseStore(t)
	state := &agentStateDBState{}
	repository, closeDB := scriptedAgentStateStore(base, state)
	defer closeDB()
	values := make([]agentmodel.AgentStateRecord, 101)
	for index := range values {
		values[index] = agentmodel.AgentStateRecord{Kind: "session", Key: fmt.Sprintf("key-%03d", index), WorkspaceID: "default", Payload: []byte(`{}`), UpdatedAt: int64(index)}
	}
	if err := repository.PutBatch(t.Context(), "default", values); err != nil {
		t.Fatal(err)
	}
	upserts := 0
	for _, query := range state.queries {
		if strings.HasPrefix(query, "INSERT INTO") {
			upserts++
		}
	}
	if upserts != 3 {
		t.Fatalf("upsert statements=%d queries=%v", upserts, state.queries)
	}
}

func TestAgentStoresUseSchemaDatabaseForDDL(t *testing.T) {
	base := openAgentStateBaseStore(t)
	appState := &agentStateDBState{}
	schemaState := &agentStateDBState{execErrors: []error{nil, nil, nil, nil, nil, nil}}
	appDB := sql.OpenDB(agentStateConnector{state: appState})
	schemaDB := sql.OpenDB(agentStateConnector{state: schemaState})
	t.Cleanup(func() {
		_ = appDB.Close()
		_ = schemaDB.Close()
	})

	stateStore := NewAgentStateStore(base)
	stateStore.db = appDB
	stateStore.schema = schemaDB
	if err := stateStore.EnsureSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	taskStore := NewAgentTaskRunStore(base)
	taskStore.db = appDB
	taskStore.schema = schemaDB
	if err := taskStore.EnsureSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := taskStore.ensureInteractiveRunSchema(t.Context()); err != nil {
		t.Fatal(err)
	}

	if len(appState.queries) != 0 {
		t.Fatalf("application database received DDL: %v", appState.queries)
	}
	if len(schemaState.queries) != 6 {
		t.Fatalf("schema database queries=%v", schemaState.queries)
	}
	for _, query := range schemaState.queries {
		if !strings.HasPrefix(query, "CREATE ") {
			t.Fatalf("schema database received non-DDL query %q", query)
		}
	}
}

func TestAgentStateStoreReadAndListStages(t *testing.T) {
	base := openAgentStateBaseStore(t)
	wantErr := errors.New("injected agent state failure")
	row := []driver.Value{"default", "user", "role", []byte(`{"ok":true}`), int64(1)}

	for _, test := range []struct {
		name      string
		queryStep agentStateQueryStep
		wantFound bool
		wantErr   bool
	}{
		{name: "missing", queryStep: agentStateQueryStep{columns: agentGetColumns()}},
		{name: "query", queryStep: agentStateQueryStep{err: wantErr}, wantErr: true},
		{name: "scan", queryStep: agentStateQueryStep{columns: append(agentGetColumns(), "extra"), rows: [][]driver.Value{append(row, "extra")}}, wantErr: true},
		{name: "success", queryStep: agentStateQueryStep{columns: agentGetColumns(), rows: [][]driver.Value{row}}, wantFound: true},
	} {
		t.Run("get "+test.name, func(t *testing.T) {
			candidate, closeDB := scriptedAgentStateStore(base, &agentStateDBState{execErrors: []error{nil}, querySteps: []agentStateQueryStep{test.queryStep}})
			defer closeDB()
			value, found, err := candidate.Get(t.Context(), "default", "session", "key")
			if test.wantErr && err == nil || !test.wantErr && err != nil || found != test.wantFound {
				t.Fatalf("value=%#v found=%v err=%v", value, found, err)
			}
			if found && string(value.Payload) != `{"ok":true}` {
				t.Fatalf("payload=%s", value.Payload)
			}
		})
	}

	listRow := []driver.Value{"key", "default", "user", "role", []byte(`{"ok":true}`), int64(1)}
	for _, test := range []struct {
		name      string
		queryStep agentStateQueryStep
		user      string
		role      string
		wantLen   int
		wantErr   bool
	}{
		{name: "query", queryStep: agentStateQueryStep{err: wantErr}, wantErr: true},
		{name: "scan", queryStep: agentStateQueryStep{columns: append(agentListColumns(), "extra"), rows: [][]driver.Value{append(listRow, "extra")}}, wantErr: true},
		{name: "rows", queryStep: agentStateQueryStep{columns: agentListColumns(), nextErr: wantErr}, wantErr: true},
		{name: "unfiltered", queryStep: agentStateQueryStep{columns: agentListColumns(), rows: [][]driver.Value{listRow}}, wantLen: 1},
		{name: "user only", queryStep: agentStateQueryStep{columns: agentListColumns(), rows: [][]driver.Value{listRow}}, user: " user ", wantLen: 1},
		{name: "role only", queryStep: agentStateQueryStep{columns: agentListColumns(), rows: [][]driver.Value{listRow}}, role: " role ", wantLen: 1},
	} {
		t.Run("list "+test.name, func(t *testing.T) {
			state := &agentStateDBState{execErrors: []error{nil}, querySteps: []agentStateQueryStep{test.queryStep}}
			candidate, closeDB := scriptedAgentStateStore(base, state)
			defer closeDB()
			values, err := candidate.List(t.Context(), "default", "session", test.user, test.role)
			if test.wantErr && err == nil || !test.wantErr && err != nil || len(values) != test.wantLen {
				t.Fatalf("values=%#v err=%v", values, err)
			}
		})
	}
}

func TestAgentStateStoreCompareAndSwapBoundaries(t *testing.T) {
	base := openAgentStateBaseStore(t)
	value := agentmodel.AgentStateRecord{WorkspaceID: "default", Kind: "session", Key: "key", Payload: []byte(`{"ok":true}`), UpdatedAt: 2}
	if ok, err := NewAgentStateStore(base).CompareAndSwap(t.Context(), "", value, 1); err == nil || ok {
		t.Fatalf("workspace ok=%v err=%v", ok, err)
	}
	if ok, err := NewAgentStateStore(base).CompareAndSwap(t.Context(), "default", agentmodel.AgentStateRecord{WorkspaceID: "other"}, 1); err == nil || ok {
		t.Fatalf("mismatch ok=%v err=%v", ok, err)
	}
	wantErr := errors.New("cas")
	for name, state := range map[string]*agentStateDBState{
		"schema":  {execErrors: []error{wantErr}},
		"exec":    {execErrors: []error{nil, wantErr}},
		"rows":    {execErrors: []error{nil, nil}, resultErrors: []error{nil, wantErr}},
		"miss":    {execErrors: []error{nil, nil}, execRows: []int64{1, 0}},
		"success": {execErrors: []error{nil, nil}},
	} {
		candidate, closeDB := scriptedAgentStateStore(base, state)
		ok, err := candidate.CompareAndSwap(t.Context(), "default", value, 1)
		closeDB()
		if name == "success" && (err != nil || !ok) {
			t.Fatalf("success ok=%v err=%v", ok, err)
		}
		if name == "miss" && (err != nil || ok) {
			t.Fatalf("miss ok=%v err=%v", ok, err)
		}
		if name != "success" && name != "miss" && !errors.Is(err, wantErr) {
			t.Fatalf("%s ok=%v err=%v", name, ok, err)
		}
	}
}

func openAgentStateBaseStore(t *testing.T) *database.RuntimeStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "base.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func scriptedAgentStateStore(store *database.RuntimeStore, state *agentStateDBState) (AgentStateStore, func()) {
	db := sql.OpenDB(agentStateConnector{state: state})
	repository := NewAgentStateStore(store)
	repository.db = db
	repository.schema = db
	return repository, func() { _ = db.Close() }
}

func agentGetColumns() []string {
	return []string{"workspace_id", "user_id", "role_key", "payload_json", "updated_at"}
}
func agentListColumns() []string {
	return []string{"state_key", "workspace_id", "user_id", "role_key", "payload_json", "updated_at"}
}

type agentStateQueryStep struct {
	columns []string
	rows    [][]driver.Value
	err     error
	nextErr error
}

type agentStateDBState struct {
	beginErrors  []error
	beginHook    func()
	execErrors   []error
	commitErrors []error
	querySteps   []agentStateQueryStep
	queries      []string
	execRows     []int64
	resultErrors []error
}

type agentStateConnector struct{ state *agentStateDBState }

func (c agentStateConnector) Connect(context.Context) (driver.Conn, error) {
	return &agentStateConn{state: c.state}, nil
}
func (agentStateConnector) Driver() driver.Driver { return agentStateDriver{} }

type agentStateDriver struct{}

func (agentStateDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type agentStateConn struct{ state *agentStateDBState }

func (*agentStateConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*agentStateConn) Close() error                        { return nil }
func (c *agentStateConn) Begin() (driver.Tx, error)         { return c.begin() }
func (c *agentStateConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.begin()
}
func (c *agentStateConn) begin() (driver.Tx, error) {
	if c.state.beginHook != nil {
		c.state.beginHook()
	}
	if len(c.state.beginErrors) > 0 {
		err := c.state.beginErrors[0]
		c.state.beginErrors = c.state.beginErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	return &agentStateTx{state: c.state}, nil
}
func (c *agentStateConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.state.queries = append(c.state.queries, query)
	var err error
	if len(c.state.execErrors) > 0 {
		err, c.state.execErrors = c.state.execErrors[0], c.state.execErrors[1:]
	}
	rows := int64(1)
	if len(c.state.execRows) > 0 {
		rows, c.state.execRows = c.state.execRows[0], c.state.execRows[1:]
	}
	var resultErr error
	if len(c.state.resultErrors) > 0 {
		resultErr, c.state.resultErrors = c.state.resultErrors[0], c.state.resultErrors[1:]
	}
	return agentStateResult{rows: rows, err: resultErr}, err
}

type agentStateResult struct {
	rows int64
	err  error
}

func (r agentStateResult) LastInsertId() (int64, error) { return 0, r.err }
func (r agentStateResult) RowsAffected() (int64, error) { return r.rows, r.err }
func (c *agentStateConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.state.queries = append(c.state.queries, query)
	if len(c.state.querySteps) == 0 {
		return &agentStateRows{columns: []string{"value"}}, nil
	}
	step := c.state.querySteps[0]
	c.state.querySteps = c.state.querySteps[1:]
	if step.err != nil {
		return nil, step.err
	}
	return &agentStateRows{columns: step.columns, rows: step.rows, nextErr: step.nextErr}, nil
}

type agentStateTx struct{ state *agentStateDBState }

func (tx *agentStateTx) Commit() error {
	if len(tx.state.commitErrors) == 0 {
		return nil
	}
	err := tx.state.commitErrors[0]
	tx.state.commitErrors = tx.state.commitErrors[1:]
	return err
}
func (*agentStateTx) Rollback() error { return nil }

type agentStateRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
	nextErr error
}

func (r *agentStateRows) Columns() []string { return r.columns }
func (*agentStateRows) Close() error        { return nil }
func (r *agentStateRows) Next(values []driver.Value) error {
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
