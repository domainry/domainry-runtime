package appschema

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"

	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

var errMetadataSQL = errors.New("scripted metadata SQL failure")

func metadataInstallScope() principalmodel.SystemScope {
	return principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "metadata test")
}

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
	if store.metadata == nil {
		store.metadata = newMetadataBindingStub()
	}
	t.Cleanup(func() { _ = store.db.Close() })
	return store
}

type metadataBindingStub struct {
	metadatasdk.Binding
	state *metadataStateStub
}

func (b metadataBindingStub) Descriptor() metadatasdk.Descriptor { return metadatasdk.Descriptor{} }
func (b metadataBindingStub) Definitions() metadatasdk.Definitions {
	return metadataDefinitionsStub{state: b.state}
}
func (b metadataBindingStub) DefinitionStore() metadatasdk.DefinitionStore {
	return metadataDefinitionStoreStub{state: b.state}
}
func (b metadataBindingStub) Localization() metadatasdk.Localization {
	return metadataLocalizationStub{state: b.state}
}
func (metadataBindingStub) Dictionaries() metadatasdk.Dictionaries {
	return metadataDictionariesStub{}
}
func (b metadataBindingStub) Projection() metadatasdk.Projection {
	return metadataProjectionStub{state: b.state}
}
func (metadataBindingStub) Close(context.Context) error { return nil }

type metadataStateStub struct {
	snapshot  metadatasdk.DefinitionSnapshot
	localized []metadatasdk.LocalizedText
	err       error
}

func newMetadataBindingStub() metadataBindingStub {
	return metadataBindingStub{state: &metadataStateStub{}}
}

type metadataDefinitionsStub struct {
	state *metadataStateStub
}

func (s metadataDefinitionsStub) List(_ context.Context, query metadatasdk.DefinitionQuery) ([]metadatasdk.Definition, error) {
	if s.state.err != nil {
		return nil, s.state.err
	}
	values := []metadatasdk.Definition{}
	for _, definition := range s.state.snapshot.Definitions {
		if (query.Owner == "" || definition.Owner == query.Owner) && (query.ResourceType == "" || definition.ResourceType == query.ResourceType) && (query.SourceID == "" || definition.SourceID == query.SourceID) {
			values = append(values, definition)
		}
	}
	return values, nil
}

func (s metadataDefinitionsStub) Get(_ context.Context, owner, resourceType, key string) (metadatasdk.Definition, bool, error) {
	if s.state.err != nil {
		return metadatasdk.Definition{}, false, s.state.err
	}
	for _, definition := range s.state.snapshot.Definitions {
		if definition.Owner == owner && definition.ResourceType == resourceType && definition.ResourceKey == key {
			return definition, true, nil
		}
	}
	return metadatasdk.Definition{}, false, nil
}

func (s metadataDefinitionsStub) Snapshot(ctx context.Context, query metadatasdk.DefinitionQuery) (metadatasdk.DefinitionSnapshot, error) {
	values, err := s.List(ctx, query)
	return metadatasdk.DefinitionSnapshot{Definitions: values}, err
}

type metadataProjectionStub struct{ state *metadataStateStub }

type metadataDefinitionStoreStub struct{ state *metadataStateStub }

func (s metadataDefinitionStoreStub) List(ctx context.Context, query metadatasdk.DefinitionQuery) ([]metadatasdk.Definition, error) {
	return (metadataDefinitionsStub{state: s.state}).List(ctx, query)
}
func (s metadataDefinitionStoreStub) Get(ctx context.Context, owner, resourceType, key string) (metadatasdk.Definition, bool, error) {
	return (metadataDefinitionsStub{state: s.state}).Get(ctx, owner, resourceType, key)
}
func (s metadataDefinitionStoreStub) Snapshot(ctx context.Context, query metadatasdk.DefinitionQuery) (metadatasdk.DefinitionSnapshot, error) {
	return (metadataDefinitionsStub{state: s.state}).Snapshot(ctx, query)
}
func (s metadataDefinitionStoreStub) ReplaceSourceSnapshot(ctx context.Context, snapshot metadatasdk.ProjectionSnapshot) error {
	return (metadataProjectionStub{state: s.state}).Sync(ctx, snapshot)
}
func (s metadataDefinitionStoreStub) Publish(context.Context, metadatasdk.DefinitionPublishCommand) (metadatasdk.DefinitionPublishResult, error) {
	return metadatasdk.DefinitionPublishResult{}, nil
}
func (s metadataDefinitionStoreStub) Disable(context.Context, metadatasdk.DefinitionDisableCommand) error {
	return nil
}
func (s metadataDefinitionStoreStub) GetVersion(context.Context, metadatasdk.DefinitionVersionQuery) (metadatasdk.DefinitionVersion, bool, error) {
	return metadatasdk.DefinitionVersion{}, false, nil
}

func (p metadataProjectionStub) Sync(_ context.Context, snapshot metadatasdk.ProjectionSnapshot) error {
	if p.state.err != nil {
		return p.state.err
	}
	retained := make([]metadatasdk.Definition, 0, len(p.state.snapshot.Definitions)+len(snapshot.Definitions))
	for _, definition := range p.state.snapshot.Definitions {
		if definition.Owner != snapshot.Owner || definition.SourceKind != snapshot.SourceKind || definition.SourceID != snapshot.SourceID {
			retained = append(retained, definition)
		}
	}
	for _, definition := range snapshot.Definitions {
		definition.Owner = snapshot.Owner
		definition.SourceKind = snapshot.SourceKind
		definition.SourceID = snapshot.SourceID
		retained = append(retained, definition)
	}
	p.state.snapshot = metadatasdk.DefinitionSnapshot{Definitions: retained}
	if len(snapshot.LocalizedText) > 0 {
		p.state.localized = append([]metadatasdk.LocalizedText(nil), snapshot.LocalizedText...)
	}
	return nil
}

type metadataLocalizationStub struct{ state *metadataStateStub }

func (s metadataLocalizationStub) List(_ context.Context, query metadatasdk.LocalizedTextQuery) ([]metadatasdk.LocalizedText, error) {
	if s.state.err != nil {
		return nil, s.state.err
	}
	values := []metadatasdk.LocalizedText{}
	for _, value := range s.state.localized {
		if (query.WorkspaceID == "" || value.WorkspaceID == query.WorkspaceID) &&
			(query.EntityType == "" || value.EntityType == query.EntityType) &&
			(query.EntityKey == "" || value.EntityKey == query.EntityKey) &&
			(query.Property == "" || value.Property == query.Property) &&
			(query.Locale == "" || value.Locale == query.Locale) {
			values = append(values, value)
		}
	}
	return values, nil
}

func (metadataLocalizationStub) Coverage(_ context.Context, query metadatasdk.LocalizedTextCoverageQuery) (metadatasdk.LocalizedTextCoverage, error) {
	return metadatasdk.LocalizedTextCoverage{WorkspaceID: query.WorkspaceID, Locale: query.Locale, FallbackLocale: query.FallbackLocale}, nil
}

type metadataDictionariesStub struct{}

func (metadataDictionariesStub) Items(_ context.Context, query metadatasdk.DictionaryItemsQuery) (metadatasdk.DictionaryItems, error) {
	return metadatasdk.DictionaryItems{DictionaryKey: query.DictionaryKey, Locale: query.Locale, Items: []metadatasdk.DictionaryItem{}}, nil
}
