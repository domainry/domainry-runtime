package report

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/domainry/domainry-orm/query"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type reportDatasetSQLStep struct {
	columns []string
	rows    [][]driver.Value
	err     error
	rowsErr error
}

type reportDatasetSQLExecStep struct {
	rows    int64
	err     error
	rowsErr error
}

type reportDatasetSQLState struct {
	steps     []reportDatasetSQLStep
	execSteps []reportDatasetSQLExecStep
	commitErr error
}

type reportDatasetConnector struct{ state *reportDatasetSQLState }

func (c reportDatasetConnector) Connect(context.Context) (driver.Conn, error) {
	return &reportDatasetConn{state: c.state}, nil
}
func (reportDatasetConnector) Driver() driver.Driver { return reportDatasetDriver{} }

type reportDatasetDriver struct{}

func (reportDatasetDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type reportDatasetConn struct{ state *reportDatasetSQLState }

func (*reportDatasetConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare unsupported")
}
func (*reportDatasetConn) Close() error                { return nil }
func (c *reportDatasetConn) Begin() (driver.Tx, error) { return &reportDatasetTx{state: c.state}, nil }
func (c *reportDatasetConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return &reportDatasetTx{state: c.state}, nil
}
func (c *reportDatasetConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if len(c.state.steps) == 0 {
		return nil, errors.New("unexpected query")
	}
	step := c.state.steps[0]
	c.state.steps = c.state.steps[1:]
	if step.err != nil {
		return nil, step.err
	}
	return &reportDatasetRows{columns: step.columns, rows: step.rows, rowsErr: step.rowsErr}, nil
}

func (c *reportDatasetConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	step := reportDatasetSQLExecStep{rows: 1}
	if len(c.state.execSteps) > 0 {
		step, c.state.execSteps = c.state.execSteps[0], c.state.execSteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return reportDatasetResult{rows: step.rows, err: step.rowsErr}, nil
}

type reportDatasetResult struct {
	rows int64
	err  error
}

func (reportDatasetResult) LastInsertId() (int64, error)   { return 0, nil }
func (r reportDatasetResult) RowsAffected() (int64, error) { return r.rows, r.err }

type reportDatasetTx struct{ state *reportDatasetSQLState }

func (t *reportDatasetTx) Commit() error { return t.state.commitErr }
func (*reportDatasetTx) Rollback() error { return nil }

type reportDatasetRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
	rowsErr error
}

func (r *reportDatasetRows) Columns() []string { return r.columns }
func (*reportDatasetRows) Close() error        { return nil }
func (r *reportDatasetRows) Next(destination []driver.Value) error {
	if r.index < len(r.rows) {
		copy(destination, r.rows[r.index])
		r.index++
		return nil
	}
	if r.rowsErr != nil {
		err := r.rowsErr
		r.rowsErr = nil
		return err
	}
	return io.EOF
}

func reportDatasetScriptedStore(t *testing.T, runtimeStore *database.RuntimeStore, state *reportDatasetSQLState) *ReportDatasetStore {
	t.Helper()
	db := sql.OpenDB(reportDatasetConnector{state: state})
	t.Cleanup(func() { _ = db.Close() })
	store := NewReportDatasetStore(runtimeStore)
	store.beginTx = db.BeginTx
	return store
}

func reportDatasetEdgeStore(t *testing.T) *database.RuntimeStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "report-edges.db"), IntegrationSecretKey: "report-edges"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestReportDatasetStoreAvailabilityAndBeginEdges(t *testing.T) {
	request := reportcontract.ReportDatasetRowReadRequest{}
	versionRequest := reportcontract.ReportSnapshotSourceVersionRequest{}
	var nilStore *ReportDatasetStore
	if _, err := nilStore.ReadReportDatasetRows(t.Context(), request); err == nil {
		t.Fatal("nil row store accepted")
	}
	if _, err := nilStore.ReadReportSnapshotSourceVersion(t.Context(), versionRequest); err == nil {
		t.Fatal("nil version store accepted")
	}
	for _, store := range []*ReportDatasetStore{NewReportDatasetStore(nil), NewReportDatasetStore(&database.RuntimeStore{})} {
		if _, err := store.ReadReportDatasetRows(t.Context(), request); err == nil {
			t.Fatal("unavailable row store accepted")
		}
		if _, err := store.ReadReportSnapshotSourceVersion(t.Context(), versionRequest); err == nil {
			t.Fatal("unavailable version store accepted")
		}
	}
	store := NewReportDatasetStore(reportDatasetEdgeStore(t))
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.ReadReportDatasetRows(cancelled, request); err == nil {
		t.Fatal("cancelled row transaction accepted")
	}
	if _, err := store.ReadReportSnapshotSourceVersion(cancelled, versionRequest); err == nil {
		t.Fatal("cancelled version transaction accepted")
	}
}

func TestReportDatasetStoreQueryConstructionAndFailureEdges(t *testing.T) {
	runtimeStore := reportDatasetEdgeStore(t)
	store := NewReportDatasetStore(runtimeStore)
	object := definitionmodel.ObjectSchema{Key: "event", Fields: []definitionmodel.FieldSchema{{Key: "owner_id", Type: "relation"}, {Key: "kind", Type: "text"}}}
	base := reportcontract.ReportDatasetRowReadRequest{
		WorkspaceID: "workspace",
		Plan:        reportmodel.ReportDatasetPlan{Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{Alias: "events", ObjectKey: "event"}}},
		Objects:     map[string]definitionmodel.ObjectSchema{"events": object},
		Queries:     map[string]recordmodel.RecordListQuery{"events": {AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, SelectFields: []string{"kind"}}},
	}
	if _, err := store.ReadReportDatasetRows(t.Context(), base); err == nil || !strings.Contains(err.Error(), "query report dataset rows") {
		t.Fatalf("missing table query error=%v", err)
	}
	badScope := base
	badScope.Queries = map[string]recordmodel.RecordListQuery{"events": {AuthorizationMode: "invalid"}}
	if _, err := store.ReadReportDatasetRows(t.Context(), badScope); err == nil {
		t.Fatal("invalid source scope accepted")
	}
	badFilter := base
	badFilter.Plan.Dataset.Filters = []reportmodel.ReportDatasetFilter{{Field: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "kind"}, Operator: "invalid"}}
	if _, err := store.ReadReportDatasetRows(t.Context(), badFilter); err == nil {
		t.Fatal("invalid global filter accepted")
	}
	relation := recordmodel.RecordScopeExpression{Operator: "eq", FieldKey: "id", Values: []string{"allowed"}, Path: []recordmodel.RecordScopePathSegment{{SourceObjectKey: "event", TargetObjectKey: "permission", Direction: "forward", RelationFieldKey: "owner_id"}}}
	relationRequest := base
	relationRequest.Queries = map[string]recordmodel.RecordListQuery{"events": {AuthorizationMode: recordmodel.RecordQueryAuthorizationPredicate, RootObjectKey: "event", ScopeExpression: &relation}}
	if _, err := store.ReadReportDatasetRows(t.Context(), relationRequest); err == nil {
		t.Fatal("relation lookup query failure lost")
	}
	versionBase := reportcontract.ReportSnapshotSourceVersionRequest{WorkspaceID: "workspace", Objects: base.Objects, Queries: base.Queries}
	if _, err := store.ReadReportSnapshotSourceVersion(t.Context(), versionBase); err == nil {
		t.Fatal("missing version table query accepted")
	}
	versionBadScope := versionBase
	versionBadScope.Queries = badScope.Queries
	if _, err := store.ReadReportSnapshotSourceVersion(t.Context(), versionBadScope); err == nil {
		t.Fatal("invalid version scope accepted")
	}
	versionRelation := versionBase
	versionRelation.Queries = relationRequest.Queries
	if _, err := store.ReadReportSnapshotSourceVersion(t.Context(), versionRelation); err == nil {
		t.Fatal("version relation lookup failure lost")
	}
}

func TestReportDatasetStoreLeftJoinDropsAbsentRecord(t *testing.T) {
	store := reportDatasetEdgeStore(t)
	for _, statement := range []string{
		`CREATE TABLE event (workspace_id TEXT, id TEXT, created_at TEXT, updated_at TEXT, kind TEXT)`,
		`CREATE TABLE detail (workspace_id TEXT, id TEXT, created_at TEXT, updated_at TEXT, event_id TEXT, note TEXT)`,
		`INSERT INTO event(workspace_id,id,created_at,updated_at,kind) VALUES ('workspace','event-1','created','updated','kind')`,
	} {
		if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	dataset := reportmodel.ReportDatasetSchema{
		Source: reportmodel.ReportDatasetSource{Alias: "events", ObjectKey: "event"},
		Joins:  []reportmodel.ReportDatasetJoin{{Alias: "details", ObjectKey: "detail", Type: "left", LeftAlias: "events", LeftField: "id", RightField: "event_id"}},
	}
	request := reportcontract.ReportDatasetRowReadRequest{
		WorkspaceID: "workspace", Plan: reportmodel.ReportDatasetPlan{Dataset: dataset},
		Objects: map[string]definitionmodel.ObjectSchema{
			"events":  {Key: "event", Fields: []definitionmodel.FieldSchema{{Key: "kind", Type: "text"}}},
			"details": {Key: "detail", Fields: []definitionmodel.FieldSchema{{Key: "event_id", Type: "relation"}, {Key: "note", Type: "text"}}},
		},
		Queries: map[string]recordmodel.RecordListQuery{
			"events": {AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, SelectFields: []string{"kind"}}, "details": {AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, SelectFields: []string{"event_id", "note"}},
		},
	}
	rows, err := NewReportDatasetStore(store).ReadReportDatasetRows(t.Context(), request)
	if err != nil || len(rows) != 1 || rows[0].Records["events"].ID != "event-1" {
		t.Fatalf("rows=%#v err=%v", rows, err)
	}
	if _, found := rows[0].Records["details"]; found {
		t.Fatalf("absent left record retained: %#v", rows[0])
	}
}

func TestReportDatasetStoreResolvesRelationScopes(t *testing.T) {
	store := reportDatasetEdgeStore(t)
	for _, statement := range []string{
		`CREATE TABLE event (workspace_id TEXT, id TEXT, created_at TEXT, updated_at TEXT, owner_id TEXT)`,
		`CREATE TABLE permission (workspace_id TEXT, id TEXT, created_at TEXT, updated_at TEXT)`,
		`INSERT INTO permission(workspace_id,id,created_at,updated_at) VALUES ('workspace','owner-1','created','updated')`,
		`INSERT INTO event(workspace_id,id,created_at,updated_at,owner_id) VALUES ('workspace','event-1','created','updated','owner-1')`,
	} {
		if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	expression := recordmodel.RecordScopeExpression{Operator: "eq", FieldKey: "id", Values: []string{"owner-1"}, Path: []recordmodel.RecordScopePathSegment{{SourceObjectKey: "event", TargetObjectKey: "permission", Direction: "forward", RelationFieldKey: "owner_id"}}}
	object := definitionmodel.ObjectSchema{Key: "event", Fields: []definitionmodel.FieldSchema{{Key: "owner_id", Type: "relation"}}}
	queryValue := recordmodel.RecordListQuery{AuthorizationMode: recordmodel.RecordQueryAuthorizationPredicate, RootObjectKey: "event", ScopeExpression: &expression, SelectFields: []string{"owner_id"}}
	rows, err := NewReportDatasetStore(store).ReadReportDatasetRows(t.Context(), reportcontract.ReportDatasetRowReadRequest{
		WorkspaceID: "workspace", Plan: reportmodel.ReportDatasetPlan{Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{Alias: "events", ObjectKey: "event"}}},
		Objects: map[string]definitionmodel.ObjectSchema{"events": object}, Queries: map[string]recordmodel.RecordListQuery{"events": queryValue},
	})
	if err != nil || len(rows) != 1 || rows[0].Records["events"].ID != "event-1" {
		t.Fatalf("rows=%#v err=%v", rows, err)
	}
	version, err := NewReportDatasetStore(store).ReadReportSnapshotSourceVersion(t.Context(), reportcontract.ReportSnapshotSourceVersionRequest{
		WorkspaceID: "workspace", Objects: map[string]definitionmodel.ObjectSchema{"events": object}, Queries: map[string]recordmodel.RecordListQuery{"events": queryValue},
	})
	if err != nil || version.SourceVersions["events"] == "" {
		t.Fatalf("version=%#v err=%v", version, err)
	}
	direct := recordmodel.RecordScopeExpression{Operator: "eq", FieldKey: "id", Values: []string{"event-1"}}
	directQuery := queryValue
	directQuery.ScopeExpression = &direct
	if rows, err := NewReportDatasetStore(store).ReadReportDatasetRows(t.Context(), reportcontract.ReportDatasetRowReadRequest{
		WorkspaceID: "workspace", Plan: reportmodel.ReportDatasetPlan{Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{Alias: "events", ObjectKey: "event"}}},
		Objects: map[string]definitionmodel.ObjectSchema{"events": object}, Queries: map[string]recordmodel.RecordListQuery{"events": directQuery},
	}); err != nil || len(rows) != 1 {
		t.Fatalf("direct rows=%#v err=%v", rows, err)
	}
	if version, err := NewReportDatasetStore(store).ReadReportSnapshotSourceVersion(t.Context(), reportcontract.ReportSnapshotSourceVersionRequest{
		WorkspaceID: "workspace", Objects: map[string]definitionmodel.ObjectSchema{"events": object}, Queries: map[string]recordmodel.RecordListQuery{"events": directQuery},
	}); err != nil || version.SourceVersions["events"] == "" {
		t.Fatalf("direct version=%#v err=%v", version, err)
	}
}

func TestReportDatasetStoreScriptedDriverFailures(t *testing.T) {
	runtimeStore := reportDatasetEdgeStore(t)
	root := definitionmodel.ObjectSchema{Key: "event"}
	detail := definitionmodel.ObjectSchema{Key: "detail", Fields: []definitionmodel.FieldSchema{{Key: "note", Type: "text"}}}
	dataset := reportmodel.ReportDatasetSchema{
		Source: reportmodel.ReportDatasetSource{Alias: "events", ObjectKey: "event"},
		Joins:  []reportmodel.ReportDatasetJoin{{Alias: "details", ObjectKey: "detail", Type: "left", LeftAlias: "events", LeftField: "id", RightField: "event_id"}},
	}
	request := reportcontract.ReportDatasetRowReadRequest{
		WorkspaceID: "workspace", Plan: reportmodel.ReportDatasetPlan{Dataset: dataset},
		Objects: map[string]definitionmodel.ObjectSchema{"events": root, "details": detail},
		Queries: map[string]recordmodel.RecordListQuery{"events": {AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted}, "details": {AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, SelectFields: []string{"note"}}},
	}
	columns := []string{"event_id", "event_created", "event_updated", "detail_id", "detail_created", "detail_updated", "detail_note"}
	state := &reportDatasetSQLState{steps: []reportDatasetSQLStep{{columns: columns, rows: [][]driver.Value{{"event-1", "created", "updated", nil, nil, nil, "orphan"}}}}}
	rows, err := reportDatasetScriptedStore(t, runtimeStore, state).ReadReportDatasetRows(t.Context(), request)
	if err != nil || len(rows) != 1 {
		t.Fatalf("scripted rows=%#v err=%v", rows, err)
	}
	if _, found := rows[0].Records["details"]; found {
		t.Fatalf("id-less scripted record retained: %#v", rows)
	}
	rowsErr := errors.New("rows")
	state = &reportDatasetSQLState{steps: []reportDatasetSQLStep{{columns: columns, rowsErr: rowsErr}}}
	if _, err := reportDatasetScriptedStore(t, runtimeStore, state).ReadReportDatasetRows(t.Context(), request); !errors.Is(err, rowsErr) {
		t.Fatalf("rows error=%v", err)
	}
	expression := recordmodel.RecordScopeExpression{Operator: "eq", FieldKey: "id", Values: []string{"owner"}, Path: []recordmodel.RecordScopePathSegment{{SourceObjectKey: "event", TargetObjectKey: "permission", Direction: "forward", RelationFieldKey: "owner_id"}}}
	relationRequest := reportcontract.ReportDatasetRowReadRequest{
		WorkspaceID: "workspace", Plan: reportmodel.ReportDatasetPlan{Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{Alias: "events", ObjectKey: "event"}}},
		Objects: map[string]definitionmodel.ObjectSchema{"events": root}, Queries: map[string]recordmodel.RecordListQuery{"events": {AuthorizationMode: recordmodel.RecordQueryAuthorizationPredicate, RootObjectKey: "event", ScopeExpression: &expression}},
	}
	state = &reportDatasetSQLState{steps: []reportDatasetSQLStep{{columns: []string{"id"}, rows: [][]driver.Value{{nil}}}}}
	if _, err := reportDatasetScriptedStore(t, runtimeStore, state).ReadReportDatasetRows(t.Context(), relationRequest); err == nil {
		t.Fatal("relation scan error lost")
	}
	versionRequest := reportcontract.ReportSnapshotSourceVersionRequest{WorkspaceID: "workspace", Objects: map[string]definitionmodel.ObjectSchema{"events": root}, Queries: map[string]recordmodel.RecordListQuery{"events": {AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted}}}
	commitErr := errors.New("commit")
	state = &reportDatasetSQLState{steps: []reportDatasetSQLStep{{columns: []string{"count", "watermark"}, rows: [][]driver.Value{{int64(1), "updated"}}}}, commitErr: commitErr}
	if _, err := reportDatasetScriptedStore(t, runtimeStore, state).ReadReportSnapshotSourceVersion(t.Context(), versionRequest); !errors.Is(err, commitErr) {
		t.Fatalf("commit error=%v", err)
	}
	state = &reportDatasetSQLState{steps: []reportDatasetSQLStep{{columns: []string{"id"}, rows: [][]driver.Value{{nil}}}}}
	versionRelation := versionRequest
	versionRelation.Queries = map[string]recordmodel.RecordListQuery{"events": {AuthorizationMode: recordmodel.RecordQueryAuthorizationPredicate, RootObjectKey: "event", ScopeExpression: &expression}}
	if _, err := reportDatasetScriptedStore(t, runtimeStore, state).ReadReportSnapshotSourceVersion(t.Context(), versionRelation); err == nil {
		t.Fatal("version relation scan error lost")
	}
}

func TestReportDatasetStoreHelperEdges(t *testing.T) {
	store := reportDatasetEdgeStore(t)
	dataset := reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{Alias: " root "}, Joins: []reportmodel.ReportDatasetJoin{{Alias: " child "}}}
	if got := reportStoreAliasOrder(dataset); len(got) != 2 || got[0] != "root" || got[1] != "child" || reportStoreCTE(2) != "report_source_2" {
		t.Fatalf("aliases=%#v", got)
	}
	columns := reportStoreSourceColumns([]string{"", "id", " amount ", "amount", "created_at"})
	if len(columns) != 4 || columns[3] != "amount" {
		t.Fatalf("columns=%#v", columns)
	}
	objects := map[string]definitionmodel.ObjectSchema{"root": {Fields: []definitionmodel.FieldSchema{{Key: "amount", Type: "currency"}}}}
	queries := map[string]recordmodel.RecordListQuery{"root": {SelectFields: []string{"amount"}}}
	selected := reportStoreSelectedColumns([]string{"root"}, objects, queries)
	if len(selected) != 4 || selected[3].field.Type != "currency" {
		t.Fatalf("selected=%#v", selected)
	}
	filters := []reportmodel.ReportDatasetFilter{
		{Field: reportmodel.ReportDatasetField{SourceAlias: "root", FieldKey: "amount"}, Operator: "contains"},
		{Field: reportmodel.ReportDatasetField{SourceAlias: "root", FieldKey: "amount"}, Operator: "starts_with"},
		{Field: reportmodel.ReportDatasetField{SourceAlias: "root", FieldKey: "amount"}, Operator: "ends_with"},
	}
	if predicate, err := reportStoreGlobalPredicate(store, reportmodel.ReportDatasetSchema{Filters: filters}, objects); err != nil || predicate != nil {
		t.Fatalf("skipped predicate=%#v err=%v", predicate, err)
	}
	for _, operator := range []string{"eq", "ne", "gt", "gte", "lt", "lte", "is_null", "not_null"} {
		filter := reportmodel.ReportDatasetFilter{Field: reportmodel.ReportDatasetField{SourceAlias: "root", FieldKey: "amount"}, Operator: operator, Value: "1"}
		predicate, err := reportStoreGlobalPredicate(store, reportmodel.ReportDatasetSchema{Filters: []reportmodel.ReportDatasetFilter{filter}}, objects)
		if err != nil || predicate == nil {
			t.Fatalf("operator %s predicate=%#v err=%v", operator, predicate, err)
		}
		if _, _, err := query.PreparePredicate(store.SQLRenderer, predicate, 2); err != nil {
			t.Fatalf("operator %s compile err=%v", operator, err)
		}
	}
	for _, operator := range []string{"in", "not_in"} {
		filter := reportmodel.ReportDatasetFilter{Field: reportmodel.ReportDatasetField{SourceAlias: "root", FieldKey: "amount"}, Operator: operator, Values: []any{"1", "2"}}
		predicate, err := reportStoreGlobalPredicate(store, reportmodel.ReportDatasetSchema{Filters: []reportmodel.ReportDatasetFilter{filter}}, objects)
		if err != nil || predicate == nil {
			t.Fatalf("operator %s predicate=%#v err=%v", operator, predicate, err)
		}
		if _, args, err := query.PreparePredicate(store.SQLRenderer, predicate, 0); err != nil || len(args) != 2 {
			t.Fatalf("operator %s args=%#v err=%v", operator, args, err)
		}
	}
	for _, filter := range []reportmodel.ReportDatasetFilter{
		{Field: reportmodel.ReportDatasetField{SourceAlias: "root", FieldKey: "amount"}, Operator: "in"},
		{Field: reportmodel.ReportDatasetField{SourceAlias: "root", FieldKey: "amount"}, Operator: "between", Values: []any{"1"}},
		{Field: reportmodel.ReportDatasetField{SourceAlias: "root", FieldKey: "amount"}, Operator: "unknown"},
	} {
		if _, err := reportStoreGlobalPredicate(store, reportmodel.ReportDatasetSchema{Filters: []reportmodel.ReportDatasetFilter{filter}}, objects); err == nil {
			t.Fatalf("invalid filter accepted: %#v", filter)
		}
	}
	between := reportmodel.ReportDatasetFilter{Field: reportmodel.ReportDatasetField{SourceAlias: "root", FieldKey: "amount"}, Operator: "between", Values: []any{"1", "2"}}
	predicate, err := reportStoreGlobalPredicate(store, reportmodel.ReportDatasetSchema{Filters: []reportmodel.ReportDatasetFilter{between}}, objects)
	if err != nil || predicate == nil {
		t.Fatalf("between predicate=%#v err=%v", predicate, err)
	}
	if _, args, err := query.PreparePredicate(store.SQLRenderer, predicate, 0); err != nil || len(args) != 2 {
		t.Fatalf("between args=%#v err=%v", args, err)
	}
	for _, key := range []string{"id", "created_at", "updated_at", "amount", "missing"} {
		if field := reportStoreField(objects["root"], key); field.Key == "" {
			t.Fatalf("field %s=%#v", key, field)
		}
	}
	if got := reportStoreSortedKeys(map[string]bool{"b": true, "a": true}); len(got) != 2 || got[0] != "a" {
		t.Fatalf("keys=%#v", got)
	}
}
