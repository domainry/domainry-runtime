package record

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func recordObjectFixture() definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{Key: "records", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}, {Key: "amount", Type: "number"}, {Key: "active", Type: "boolean"}}}
}

func TestCurrencyDatabaseCodecIsExactAndSQLiteSortable(t *testing.T) {
	field := definitionmodel.FieldSchema{Key: "amount", Type: "currency", Config: map[string]any{"precision": 8, "scale": 2, "currency_code": "USD"}}
	values := []string{"-2.00", "0.00", "2.00", "10.00"}
	encoded := make([]string, 0, len(values))
	for _, value := range values {
		stored, ok := dbFieldValue(testEngineProfile("sqlite"), field, value).(string)
		if !ok {
			t.Fatalf("stored %s has type %T", value, stored)
		}
		encoded = append(encoded, stored)
		if decoded := normalizeDBValue(testEngineProfile("sqlite"), field, stored); decoded != value {
			t.Fatalf("round trip %s -> %s -> %v", value, stored, decoded)
		}
	}
	if !sort.StringsAreSorted(encoded) {
		t.Fatalf("SQLite encoding is not numerically sortable: %v", encoded)
	}
	if stored := dbFieldValue(testEngineProfile("postgres"), field, "1.2"); stored != "1.20" {
		t.Fatalf("postgres decimal=%v", stored)
	}
	invalidConfig := definitionmodel.FieldSchema{Type: "currency", Config: map[string]any{"scale": -1}}
	if got := dbFieldValue(testEngineProfile("sqlite"), invalidConfig, "1"); got != "1" {
		t.Fatalf("invalid config value=%v", got)
	}
	if got := dbFieldValue(testEngineProfile("sqlite"), field, 1.5); got != 1.5 {
		t.Fatalf("binary float should remain rejected, got=%v", got)
	}
	if got := normalizeDBValue(testEngineProfile("sqlite"), invalidConfig, "1"); got != "1" {
		t.Fatalf("invalid read config=%v", got)
	}
	malformed := strings.Repeat("x", 9)
	if got := normalizeDBValue(testEngineProfile("sqlite"), field, malformed); got != malformed {
		t.Fatalf("malformed storage=%v", got)
	}
	if got := normalizeDBValue(testEngineProfile("postgres"), field, "1.2"); got != "1.20" {
		t.Fatalf("postgres read=%v", got)
	}
	if got := normalizeDBValue(testEngineProfile("sqlite"), field, "1.2"); got != "1.20" {
		t.Fatalf("legacy SQLite read=%v", got)
	}
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{field}}
	if got := recordConditionDBValue(testEngineProfile("sqlite"), object, "amount", "1.2"); got != dbFieldValue(testEngineProfile("sqlite"), field, "1.2") {
		t.Fatalf("condition value=%v", got)
	}
	if got := recordConditionDBValue(testEngineProfile("sqlite"), object, "status", 1); got != int64(1) {
		t.Fatalf("fallback condition=%v", got)
	}
	if got := recordQueryDBValues(testEngineProfile("postgres"), object, recordmodel.RecordListQuery{Filters: map[string]any{"amount": "1.2"}}); got.Filters["amount"] != "1.2" {
		t.Fatalf("postgres query=%v", got.Filters)
	}
	query := recordmodel.RecordListQuery{Filters: map[string]any{
		"amount__gte": "1.2", "amount__in": []any{"1.2", "2.3"}, "amount__in_strings": []string{"1.2"}, "status": "open",
	}}
	got := recordQueryDBValues(testEngineProfile("sqlite"), object, query)
	if got.Filters["amount__gte"] == "1.2" || got.Filters["status"] != "open" {
		t.Fatalf("sqlite query=%v", got.Filters)
	}
	if values, ok := got.Filters["amount__in"].([]any); !ok || len(values) != 2 || values[0] == "1.2" {
		t.Fatalf("any values=%#v", got.Filters["amount__in"])
	}
	if values, ok := got.Filters["amount__in_strings"].([]string); !ok || len(values) != 1 || values[0] == "1.2" {
		t.Fatalf("string values=%#v", got.Filters["amount__in_strings"])
	}
}

func TestStructuredFieldDatabaseCodecAndQueryValuesAreCanonical(t *testing.T) {
	multi := definitionmodel.FieldSchema{Key: "tags", Type: recordmodel.RecordMultiSelectFieldType}
	jsonField := definitionmodel.FieldSchema{Key: "payload", Type: recordmodel.RecordJSONFieldType}
	for _, profile := range []string{"sqlite", "postgres", "mysql"} {
		stored := dbFieldValue(testEngineProfile(profile), multi, []any{"z", "a", "z"})
		if stored != `["a","z"]` {
			t.Fatalf("%s multi stored=%#v", profile, stored)
		}
		if decoded := normalizeDBValue(testEngineProfile(profile), multi, stored); !reflect.DeepEqual(decoded, []string{"a", "z"}) {
			t.Fatalf("%s multi decoded=%#v", profile, decoded)
		}
		stored = dbFieldValue(testEngineProfile(profile), jsonField, map[string]any{"b": 2, "a": 1})
		if stored != `{"a":1,"b":2}` {
			t.Fatalf("%s json stored=%#v", profile, stored)
		}
		decoded, ok := normalizeDBValue(testEngineProfile(profile), jsonField, []byte(stored.(string))).(map[string]any)
		if !ok || fmt.Sprint(decoded["a"]) != "1" {
			t.Fatalf("%s json decoded=%#v", profile, decoded)
		}
	}
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{multi, jsonField}}
	query := recordmodel.RecordListQuery{
		Filters:          map[string]any{"tags": []any{"z", "a"}},
		FilterExpression: &recordmodel.RecordFilterExpression{Operator: "in", Field: "tags", Values: []any{[]any{"b", "a"}, []any{"c"}}},
	}
	got := recordQueryDBValues(testEngineProfile("postgres"), object, query)
	if got.Filters["tags"] != `["a","z"]` || !reflect.DeepEqual(got.FilterExpression.Values, []any{`["a","b"]`, `["c"]`}) {
		t.Fatalf("structured query=%#v", got)
	}
}

func recordFixture() recordmodel.Record {
	return recordmodel.Record{ID: "record", CreatedAt: "created", UpdatedAt: "updated", Data: map[string]any{"name": "Acme", "amount": 2, "active": true}}
}

func TestRecordStoreSQLFailureAndProjectionEdges(t *testing.T) {
	object, value := recordObjectFixture(), recordFixture()
	if (RecordStore{}).database() != nil {
		t.Fatal("zero store exposed database")
	}
	realStore := scriptedRecordStore(t, &recordSQLState{})
	for name, call := range map[string]func() error{
		"list": func() error {
			_, err := realStore.ListRecords(t.Context(), "", object, recordmodel.RecordListQuery{})
			return err
		},
		"get":          func() error { _, _, err := realStore.GetRecord(t.Context(), "", object, value.ID); return err },
		"insert":       func() error { return realStore.InsertRecord(t.Context(), "", object, value) },
		"update":       func() error { return realStore.UpdateRecord(t.Context(), "", object, value) },
		"update-where": func() error { _, err := realStore.UpdateRecordWhere(t.Context(), "", object, value, nil); return err },
		"delete":       func() error { return realStore.DeleteRecord(t.Context(), "", object, value.ID) },
		"unique": func() error {
			_, err := realStore.UniqueExists(t.Context(), "", object.Key, "name", "", "Acme")
			return err
		},
		"commit": func() error {
			return realStore.CommitRecordMutationBatch(t.Context(), "", []transactionmodel.RecordMutationCommit{{Operation: "create"}})
		},
	} {
		if err := call(); err == nil {
			t.Fatalf("%s accepted empty workspace", name)
		}
	}

	store := scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{{err: errRecordSQL}}})
	if _, err := store.ListRecords(t.Context(), "workspace", object, recordmodel.RecordListQuery{Page: 0, PageSize: 10, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted}); !errors.Is(err, errRecordSQL) {
		t.Fatalf("count error=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}, {err: errRecordSQL}}})
	if _, err := store.ListRecords(t.Context(), "workspace", object, recordmodel.RecordListQuery{Page: 1, PageSize: 10, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted}); !errors.Is(err, errRecordSQL) {
		t.Fatalf("list error=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}, {columns: []string{"id"}, nextErr: errRecordSQL}}})
	if _, err := store.ListRecords(t.Context(), "workspace", object, recordmodel.RecordListQuery{Page: 1, PageSize: 10, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted}); !errors.Is(err, errRecordSQL) {
		t.Fatalf("list terminal error=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{{err: errRecordSQL}}})
	if _, _, err := store.GetRecord(t.Context(), "workspace", object, value.ID); !errors.Is(err, errRecordSQL) {
		t.Fatalf("get error=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{{columns: []string{"id"}, nextErr: errRecordSQL}}})
	if _, _, err := store.GetRecord(t.Context(), "workspace", object, value.ID); !errors.Is(err, errRecordSQL) {
		t.Fatalf("get terminal error=%v", err)
	}

	for name, call := range map[string]func(RecordStore) error{
		"insert": func(store RecordStore) error { return store.InsertRecord(t.Context(), "workspace", object, value) },
		"update": func(store RecordStore) error { return store.UpdateRecord(t.Context(), "workspace", object, value) },
		"delete": func(store RecordStore) error { return store.DeleteRecord(t.Context(), "workspace", object, value.ID) },
	} {
		t.Run(name, func(t *testing.T) {
			store := scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{err: errRecordSQL}}})
			if err := call(store); !errors.Is(err, errRecordSQL) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{rowsErr: errRecordSQL}}})
	if _, err := store.UpdateRecordWhere(t.Context(), "workspace", object, value, map[string]any{"status": "open"}); !errors.Is(err, errRecordSQL) {
		t.Fatalf("conditional rows error=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{err: errRecordSQL}}})
	if _, err := store.UpdateRecordWhere(t.Context(), "workspace", object, value, nil); !errors.Is(err, errRecordSQL) {
		t.Fatalf("conditional exec error=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{rowsErr: errRecordSQL}}})
	if err := store.DeleteRecord(t.Context(), "workspace", object, value.ID); !errors.Is(err, errRecordSQL) {
		t.Fatalf("delete rows error=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{rows: 0}}})
	if err := store.DeleteRecord(t.Context(), "workspace", object, value.ID); err == nil {
		t.Fatal("missing delete accepted")
	}
	emptyValue := value
	emptyValue.Data = map[string]any{}
	store = scriptedRecordStore(t, &recordSQLState{})
	if err := store.UpdateRecord(t.Context(), "workspace", object, emptyValue); err != nil {
		t.Fatalf("empty-data update=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{})
	if _, err := store.UpdateRecordWhere(t.Context(), "workspace", object, emptyValue, nil); err != nil {
		t.Fatalf("empty-data conditional update=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{{err: errRecordSQL}}})
	if _, err := store.UniqueExists(t.Context(), "workspace", object.Key, "name", "current", "Acme"); !errors.Is(err, errRecordSQL) {
		t.Fatalf("unique error=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{{columns: []string{"id"}, rows: [][]driver.Value{{"other"}}}}})
	if exists, err := store.UniqueExists(t.Context(), "workspace", object.Key, "name", "", "Acme"); err != nil || !exists {
		t.Fatalf("unique exists=%v err=%v", exists, err)
	}
	store = scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{{columns: []string{"id"}}}})
	if exists, err := store.UniqueExists(t.Context(), "workspace", object.Key, "name", "", "Acme"); err != nil || exists {
		t.Fatalf("missing unique exists=%v err=%v", exists, err)
	}

	if got := dbValue(float32(1.5)); got != float64(1.5) {
		t.Fatalf("float32 db value=%v", got)
	}
	for input, want := range map[any]any{int(2): int64(2), int64(9007199254740993): int64(9007199254740993), "plain": "plain"} {
		if got := dbValue(input); got != want {
			t.Fatalf("dbValue(%T)=%v", input, got)
		}
	}
	boolField := definitionmodel.FieldSchema{Type: "boolean"}
	for _, test := range []struct{ input, want any }{{true, true}, {int64(1), true}, {int(0), false}, {float64(1), true}, {" YES ", true}, {[]byte("on"), true}, {time.Time{}, time.Time{}}} {
		if got := normalizeDBValue(testEngineProfile("sqlite"), boolField, test.input); got != test.want {
			t.Fatalf("boolean normalize(%T)=%v want=%v", test.input, got, test.want)
		}
	}
	numberField := definitionmodel.FieldSchema{Type: "number"}
	for _, input := range []any{int64(1), int(2), float32(3), "4", "invalid", float64(5)} {
		_ = normalizeDBValue(testEngineProfile("sqlite"), numberField, input)
	}
	if got := normalizeDBValue(testEngineProfile("sqlite"), definitionmodel.FieldSchema{Type: "text"}, "text"); got != "text" {
		t.Fatalf("text=%v", got)
	}
	timestamp := time.Date(2026, 8, 29, 8, 30, 0, 123456000, time.FixedZone("UTC+8", 8*60*60))
	if got, want := recordTimestampValue(timestamp), timestamp.UTC().Format(time.RFC3339Nano); got != want {
		t.Fatalf("timestamp=%q want=%q", got, want)
	}
	if got := recordTimestampValue("version-1"); got != "version-1" {
		t.Fatalf("text timestamp=%q", got)
	}
	for name, rows := range map[string]recordRows{
		"columns":  fakeRecordRows{columnsErr: errRecordSQL},
		"scan":     fakeRecordRows{columns: []string{"id"}, next: true, scanErr: errRecordSQL},
		"terminal": fakeRecordRows{columns: []string{"id"}, terminalErr: errRecordSQL},
	} {
		if _, err := recordsFromRows(testEngineProfile("sqlite"), object, rows); !errors.Is(err, errRecordSQL) {
			t.Fatalf("%s row error=%v", name, err)
		}
	}
}

type fakeRecordRows struct {
	columns                          []string
	columnsErr, scanErr, terminalErr error
	next                             bool
}

func (rows fakeRecordRows) Columns() ([]string, error) { return rows.columns, rows.columnsErr }
func (rows fakeRecordRows) Next() bool                 { return rows.next }
func (rows fakeRecordRows) Scan(...any) error          { return rows.scanErr }
func (rows fakeRecordRows) Err() error                 { return rows.terminalErr }

func TestRecordMutationSQLFailureAndSideEffectEdges(t *testing.T) {
	object, value := recordObjectFixture(), recordFixture()
	emptyValue := value
	emptyValue.Data = map[string]any{}
	commit := transactionmodel.RecordMutationCommit{Operation: "create", Object: object, Record: value}
	store := scriptedRecordStore(t, &recordSQLState{})
	if err := store.CommitRecordMutationBatch(t.Context(), "workspace", nil); err != nil {
		t.Fatalf("empty batch=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{beginErr: errRecordSQL})
	if err := store.CommitRecordMutation(t.Context(), "workspace", commit); !errors.Is(err, errRecordSQL) {
		t.Fatalf("begin error=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{err: errRecordSQL}}})
	if err := store.CommitRecordMutation(t.Context(), "workspace", commit); !errors.Is(err, errRecordSQL) {
		t.Fatalf("create error=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{commitErr: errRecordSQL})
	if err := store.CommitRecordMutation(t.Context(), "workspace", commit); !errors.Is(err, errRecordSQL) {
		t.Fatalf("commit error=%v", err)
	}

	for _, operation := range []string{"update", "delete"} {
		commit := transactionmodel.RecordMutationCommit{Operation: operation, Object: object, Record: value, RecordID: value.ID}
		store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{rowsErr: errRecordSQL}}})
		if err := store.CommitRecordMutation(t.Context(), "workspace", commit); !errors.Is(err, errRecordSQL) {
			t.Fatalf("%s rows error=%v", operation, err)
		}
	}
	conditionCommit := transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: value, Conditions: map[string]any{"": "ignored", "id": "ignored", "updated_at": "ignored", "status": "open"}}
	store = scriptedRecordStore(t, &recordSQLState{})
	if err := store.CommitRecordMutation(t.Context(), "workspace", conditionCommit); err != nil {
		t.Fatalf("filtered conditions update=%v", err)
	}
	emptyCreate := transactionmodel.RecordMutationCommit{Operation: "create", Object: object, Record: emptyValue}
	store = scriptedRecordStore(t, &recordSQLState{})
	if err := store.CommitRecordMutation(t.Context(), "workspace", emptyCreate); err != nil {
		t.Fatalf("empty-data create=%v", err)
	}
	emptyUpdate := transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: emptyValue}
	store = scriptedRecordStore(t, &recordSQLState{})
	if err := store.CommitRecordMutation(t.Context(), "workspace", emptyUpdate); err != nil {
		t.Fatalf("empty-data mutation update=%v", err)
	}
	deleteFallback := transactionmodel.RecordMutationCommit{Operation: "delete", Object: object, Record: value}
	store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{err: errRecordSQL}}})
	if err := store.CommitRecordMutation(t.Context(), "workspace", deleteFallback); !errors.Is(err, errRecordSQL) {
		t.Fatalf("delete fallback error=%v", err)
	}

	store = scriptedRecordStore(t, &recordSQLState{})
	tx, err := store.database().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyRecordMutationTx(t.Context(), tx, "", commit); err == nil {
		t.Fatal("empty workspace transaction accepted")
	}
	if err := store.ApplyRecordMutationTx(t.Context(), tx, "workspace", transactionmodel.RecordMutationCommit{Operation: "unsupported"}); err == nil {
		t.Fatal("unsupported transaction mutation accepted")
	}
	if err := store.ApplyAuditTx(t.Context(), tx, auditmodel.AuditEvent{}); err == nil {
		t.Fatal("empty audit accepted")
	}
	_ = tx.Rollback()

	for name, commit := range map[string]transactionmodel.RecordMutationCommit{
		"audit":    {Operation: "create", Object: object, Record: value, Audit: &auditmodel.AuditEvent{ID: "audit", Family: auditmodel.EventFamilyBusinessRecord, Event: "record_created", CreatedAt: "2026-07-19T00:00:00Z"}},
		"audits":   {Operation: "create", Object: object, Record: value, Audits: []auditmodel.AuditEvent{{ID: "audit", Family: auditmodel.EventFamilyBusinessRecord, Event: "record_created", CreatedAt: "2026-07-19T00:00:00Z"}}},
		"outbox":   {Operation: "create", Object: object, Record: value, Outbox: []publicationmodel.Message{{ID: "outbox", WorkspaceID: "workspace", DedupKey: "key"}}},
		"workflow": {Operation: "create", Object: object, Record: value, WorkflowIntents: []workflowmodel.WorkflowExecution{{ID: "workflow"}}},
	} {
		t.Run(name+"-insert", func(t *testing.T) {
			store := scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{rows: 1}, {err: errRecordSQL}}})
			if err := store.CommitRecordMutation(t.Context(), "workspace", commit); err == nil {
				t.Fatal("side effect insert failure swallowed")
			}
		})
	}

	bad := func() {}
	for name, workflow := range map[string]workflowmodel.WorkflowExecution{
		"action":  {ID: "workflow", Action: map[string]any{"bad": bad}},
		"payload": {ID: "workflow", Action: map[string]any{}, Payload: map[string]any{"bad": bad}},
		"result":  {ID: "workflow", Action: map[string]any{}, Payload: map[string]any{}, Result: map[string]any{"bad": bad}},
	} {
		if _, _, err := workflowExecutionInsertValues(workflow); err == nil {
			t.Fatalf("%s workflow encoding accepted", name)
		}
	}
	store = scriptedRecordStore(t, &recordSQLState{})
	tx, _ = store.database().BeginTx(t.Context(), nil)
	if err := store.insertWorkflowIntentTx(t.Context(), tx, workflowmodel.WorkflowExecution{ID: "workflow", Action: map[string]any{"bad": bad}}); err == nil {
		t.Fatal("invalid workflow encoding accepted")
	}
	_ = tx.Rollback()
	for name, event := range map[string]auditmodel.AuditEvent{
		"before":   {ID: "audit", WorkspaceID: "workspace", Family: auditmodel.EventFamilyBusinessRecord, Event: "record_updated", Before: map[string]any{"bad": bad}, CreatedAt: "2026-07-19T00:00:00Z"},
		"after":    {ID: "audit", WorkspaceID: "workspace", Family: auditmodel.EventFamilyBusinessRecord, Event: "record_updated", After: map[string]any{"bad": bad}, CreatedAt: "2026-07-19T00:00:00Z"},
		"metadata": {ID: "audit", WorkspaceID: "workspace", Family: auditmodel.EventFamilyBusinessRecord, Event: "record_updated", Metadata: map[string]any{"bad": bad}, CreatedAt: "2026-07-19T00:00:00Z"},
	} {
		store = scriptedRecordStore(t, &recordSQLState{})
		tx, _ := store.database().BeginTx(t.Context(), nil)
		if err := store.insertAuditEventTx(t.Context(), tx, event); err == nil {
			t.Fatalf("%s audit encoding accepted", name)
		}
		_ = tx.Rollback()
	}
	store = scriptedRecordStore(t, &recordSQLState{})
	tx, _ = store.database().BeginTx(t.Context(), nil)
	if err := store.insertPublicationHandoffTx(t.Context(), tx, publicationmodel.Message{WorkspaceID: "workspace", DedupKey: "key", Payload: map[string]any{"bad": bad}}); err == nil {
		t.Fatal("invalid outbox payload accepted")
	}
	_ = tx.Rollback()
}
