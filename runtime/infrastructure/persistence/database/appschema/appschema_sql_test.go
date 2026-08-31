package appschema

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"

	metadatapersistence "github.com/domainry/domainry-metadata-sdk/persistence"
	metadatamodule "github.com/domainry/domainry-metadata/module"
)

var errMetadataSQL = errors.New("scripted metadata SQL failure")

type metadataSQLState struct {
	execSteps                        []metadataSQLExecStep
	querySteps                       []metadataSQLQueryStep
	queryLog                         []string
	execLog                          []string
	beginErr, commitErr, rollbackErr error
	queryHook                        func()
}

type metadataSQLExecStep struct {
	rows         int64
	err, rowsErr error
}
type metadataSQLQueryStep struct {
	columns      []string
	rows         [][]driver.Value
	err, nextErr error
	closeErr     error
}

func openMetadataScriptedDB(state *metadataSQLState) *sql.DB {
	return sql.OpenDB(metadataSQLConnector{state})
}

type metadataSQLConnector struct{ state *metadataSQLState }

func (c metadataSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &metadataSQLConn{c.state}, nil
}
func (metadataSQLConnector) Driver() driver.Driver { return metadataSQLDriver{} }

type metadataSQLDriver struct{}

func (metadataSQLDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type metadataSQLConn struct{ state *metadataSQLState }

func (*metadataSQLConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*metadataSQLConn) Close() error                        { return nil }
func (c *metadataSQLConn) Begin() (driver.Tx, error) {
	if c.state.beginErr != nil {
		return nil, c.state.beginErr
	}
	return metadataSQLTx{c.state}, nil
}
func (c *metadataSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (c *metadataSQLConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.state.execLog = append(c.state.execLog, query)
	step := metadataSQLExecStep{rows: 1}
	if len(c.state.execSteps) > 0 {
		step, c.state.execSteps = c.state.execSteps[0], c.state.execSteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return metadataSQLResult{step.rows, step.rowsErr}, nil
}
func (c *metadataSQLConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.state.queryLog = append(c.state.queryLog, query)
	if c.state.queryHook != nil {
		c.state.queryHook()
	}
	step := metadataSQLQueryStep{}
	if len(c.state.querySteps) > 0 {
		step, c.state.querySteps = c.state.querySteps[0], c.state.querySteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return &metadataSQLRows{columns: step.columns, rows: step.rows, nextErr: step.nextErr, closeErr: step.closeErr}, nil
}

type metadataSQLResult struct {
	rows int64
	err  error
}

func (metadataSQLResult) LastInsertId() (int64, error)   { return 0, nil }
func (r metadataSQLResult) RowsAffected() (int64, error) { return r.rows, r.err }

type metadataSQLTx struct{ state *metadataSQLState }

func (t metadataSQLTx) Commit() error   { return t.state.commitErr }
func (t metadataSQLTx) Rollback() error { return t.state.rollbackErr }

type metadataSQLRows struct {
	columns  []string
	rows     [][]driver.Value
	index    int
	nextErr  error
	closeErr error
}

func (r *metadataSQLRows) Columns() []string { return r.columns }
func (r *metadataSQLRows) Close() error      { return r.closeErr }
func (r *metadataSQLRows) Next(values []driver.Value) error {
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

func scriptedApplicationSchemaStore(t *testing.T, state *metadataSQLState, store ApplicationSchemaStore) ApplicationSchemaStore {
	t.Helper()
	store.db = openMetadataScriptedDB(state)
	store.schemaDB = store.db
	moduleRepository := metadatamodule.NewDefinitionRepository(store.db, store.store.SQLRenderer)
	store.metadataDefinitions = scriptedMetadataRepository{delegate: store.metadataDefinitions, crud: moduleRepository.(metadatapersistence.ExecutorDefinitionRepository)}
	t.Cleanup(func() { _ = store.db.Close() })
	return store
}

type scriptedMetadataRepository struct {
	delegate metadatapersistence.DefinitionRepository
	crud     metadatapersistence.ExecutorDefinitionRepository
}

func (r scriptedMetadataRepository) SyncDefinitions(ctx context.Context, snapshot metadatapersistence.Snapshot) error {
	if r.delegate != nil {
		return r.delegate.SyncDefinitions(ctx, snapshot)
	}
	return nil
}
func (r scriptedMetadataRepository) DefinitionSnapshot(ctx context.Context) (metadatapersistence.Snapshot, error) {
	if r.delegate != nil {
		return r.delegate.DefinitionSnapshot(ctx)
	}
	return metadatapersistence.Snapshot{}, nil
}
func (scriptedMetadataRepository) DefinitionSnapshotWithExecutor(context.Context, metadatapersistence.QueryExecutor) (metadatapersistence.Snapshot, error) {
	return metadatapersistence.Snapshot{}, nil
}
func (r scriptedMetadataRepository) GetDefinitionWithExecutor(ctx context.Context, executor metadatapersistence.QueryExecutor, resourceType, key string) (metadatapersistence.StoredDefinition, bool, error) {
	return r.crud.GetDefinitionWithExecutor(ctx, executor, resourceType, key)
}
func (r scriptedMetadataRepository) ListDefinitionsWithExecutor(ctx context.Context, executor metadatapersistence.QueryExecutor, resourceType, sourceID string) ([]metadatapersistence.StoredDefinition, error) {
	return r.crud.ListDefinitionsWithExecutor(ctx, executor, resourceType, sourceID)
}
func (r scriptedMetadataRepository) ReplaceDefinitionWithExecutor(ctx context.Context, executor metadatapersistence.ExecutionExecutor, value metadatapersistence.StoredDefinition, expected *string) (metadatapersistence.ReplaceResult, error) {
	return r.crud.ReplaceDefinitionWithExecutor(ctx, executor, value, expected)
}
func (r scriptedMetadataRepository) DisableDefinitionWithExecutor(ctx context.Context, executor metadatapersistence.ExecutionExecutor, resourceType, key, at string, expected *string) (bool, error) {
	return r.crud.DisableDefinitionWithExecutor(ctx, executor, resourceType, key, at, expected)
}

func (r scriptedMetadataRepository) CountDefinitionVersionsWithExecutor(ctx context.Context, executor metadatapersistence.QueryExecutor, resourceType, key string) (int, error) {
	return r.crud.CountDefinitionVersionsWithExecutor(ctx, executor, resourceType, key)
}

func (r scriptedMetadataRepository) InsertDefinitionVersionWithExecutor(ctx context.Context, executor metadatapersistence.ExecutionExecutor, value metadatapersistence.DefinitionVersion) error {
	return r.crud.InsertDefinitionVersionWithExecutor(ctx, executor, value)
}

func (r scriptedMetadataRepository) ListDefinitionVersionsWithExecutor(ctx context.Context, executor metadatapersistence.QueryExecutor, resourceType, key string) ([]metadatapersistence.DefinitionVersion, error) {
	return r.crud.ListDefinitionVersionsWithExecutor(ctx, executor, resourceType, key)
}

func (r scriptedMetadataRepository) GetDefinitionVersionWithExecutor(ctx context.Context, executor metadatapersistence.QueryExecutor, resourceType, key, version string) (metadatapersistence.DefinitionVersion, bool, error) {
	return r.crud.GetDefinitionVersionWithExecutor(ctx, executor, resourceType, key, version)
}
