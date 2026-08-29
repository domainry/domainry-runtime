package record

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func TestListDueRecordTimerWorkspacesValidatesAndStreamsDistinctWorkspaces(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "record_timer"}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (RecordStore{}).ListDueRecordTimerWorkspaces(cancelled, object, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error=%v", err)
	}
	if _, err := (RecordStore{}).ListDueRecordTimerWorkspaces(t.Context(), definitionmodel.ObjectSchema{}, time.Now()); err == nil {
		t.Fatal("blank object key accepted")
	}

	store := scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{{err: errRecordSQL}}})
	if _, err := store.ListDueRecordTimerWorkspaces(t.Context(), object, time.Now()); !errors.Is(err, errRecordSQL) {
		t.Fatalf("query error=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{{columns: []string{"workspace_id", "extra"}, rows: [][]driver.Value{{"workspace", "extra"}}}}})
	if _, err := store.ListDueRecordTimerWorkspaces(t.Context(), object, time.Now()); err == nil {
		t.Fatal("scan type error not returned")
	}
	store = scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{{columns: []string{"workspace_id"}, rows: [][]driver.Value{{" alpha "}, {" "}, {"beta"}}}}})
	workspaces, err := store.ListDueRecordTimerWorkspaces(t.Context(), object, time.Now())
	if err != nil || !reflect.DeepEqual(workspaces, []string{"alpha", "beta"}) {
		t.Fatalf("workspaces=%v err=%v", workspaces, err)
	}
	store = scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{{columns: []string{"workspace_id"}, nextErr: errRecordSQL}}})
	if _, err := store.ListDueRecordTimerWorkspaces(t.Context(), object, time.Now()); !errors.Is(err, errRecordSQL) {
		t.Fatalf("terminal rows error=%v", err)
	}
}

func TestRecordAggregateHelpersCoverExactDecimalAndNumericBoundaries(t *testing.T) {
	currency := definitionmodel.FieldSchema{Key: "amount", Type: "currency", Config: map[string]any{"precision": 8, "scale": 2, "currency_code": "USD"}}
	number := definitionmodel.FieldSchema{Key: "quantity", Type: "number"}
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{currency, number}}
	if got := recordAggregateValueField(object, recordvalidation.RecordRelatedAggregateInvariant{Aggregate: "count", LimitField: "capacity"}); got.Key != "capacity" || got.Type != "number" {
		t.Fatalf("count field=%#v", got)
	}
	if got := recordAggregateValueField(object, recordvalidation.RecordRelatedAggregateInvariant{Aggregate: "sum", ValueField: "amount"}); got.Type != "currency" {
		t.Fatalf("currency field=%#v", got)
	}
	if got := recordAggregateValueField(object, recordvalidation.RecordRelatedAggregateInvariant{Aggregate: "sum", ValueField: "missing"}); got.Key != "missing" || got.Type != "number" {
		t.Fatalf("fallback field=%#v", got)
	}

	if value, err := recordAggregateZero(number, "count"); err != nil || value != float64(0) {
		t.Fatalf("count zero=%v err=%v", value, err)
	}
	if value, err := recordAggregateZero(number, "sum"); err != nil || value != float64(0) {
		t.Fatalf("number zero=%v err=%v", value, err)
	}
	if value, err := recordAggregateZero(currency, "sum"); err != nil || value != "0.00" {
		t.Fatalf("currency zero=%v err=%v", value, err)
	}
	invalidCurrency := currency
	invalidCurrency.Config = map[string]any{"scale": -1}
	if _, err := recordAggregateZero(invalidCurrency, "sum"); err == nil {
		t.Fatal("invalid currency zero accepted")
	}

	if value, err := recordAggregateAdd("1.20", "2.30", currency, "sum"); err != nil || value != "3.50" {
		t.Fatalf("currency add=%v err=%v", value, err)
	}
	for _, test := range []struct {
		name           string
		total, operand any
		field          definitionmodel.FieldSchema
	}{
		{"config", "1", "2", invalidCurrency},
		{"left", true, "2", currency},
		{"right", "1", true, currency},
	} {
		if _, err := recordAggregateAdd(test.total, test.operand, test.field, "sum"); err == nil {
			t.Fatalf("%s currency add accepted", test.name)
		}
	}
	if value, err := recordAggregateAdd("1.5", 2, number, "sum"); err != nil || value != 3.5 {
		t.Fatalf("number add=%v err=%v", value, err)
	}
	if _, err := recordAggregateAdd("bad", 2, number, "sum"); err == nil {
		t.Fatal("invalid numeric left accepted")
	}
	if _, err := recordAggregateAdd(1, "bad", number, "sum"); err == nil {
		t.Fatal("invalid numeric right accepted")
	}

	operators := []string{"lt", "lte", "eq", "gte", "gt"}
	for _, operator := range operators {
		if _, err := recordAggregateCompare("1.00", "2.00", currency, operator); err != nil {
			t.Fatalf("currency %s: %v", operator, err)
		}
		if _, err := recordAggregateCompare(1, 2, number, operator); err != nil {
			t.Fatalf("number %s: %v", operator, err)
		}
		if _, err := recordAggregateCompareFloat(1, 2, operator); err != nil {
			t.Fatalf("float %s: %v", operator, err)
		}
	}
	for _, test := range []struct {
		name         string
		total, limit any
		field        definitionmodel.FieldSchema
	}{
		{"config", "1", "2", invalidCurrency},
		{"left", true, "2", currency},
		{"right", "1", true, currency},
	} {
		if _, err := recordAggregateCompare(test.total, test.limit, test.field, "eq"); err == nil {
			t.Fatalf("%s currency comparison accepted", test.name)
		}
	}
	if _, err := recordAggregateCompare("bad", 2, number, "eq"); err == nil {
		t.Fatal("invalid numeric comparison left accepted")
	}
	if _, err := recordAggregateCompare(1, "bad", number, "eq"); err == nil {
		t.Fatal("invalid numeric comparison right accepted")
	}
	if _, err := recordAggregateCompare(1, 2, number, "unknown"); err == nil {
		t.Fatal("unknown numeric operator accepted")
	}
	if _, err := recordAggregateCompare("1.00", "2.00", currency, "unknown"); err == nil {
		t.Fatal("unknown currency operator accepted")
	}
}

func TestRecordMutationPredicateAndQueryProjectionBoundaries(t *testing.T) {
	store := scriptedRecordStore(t, &recordSQLState{})
	currency := definitionmodel.FieldSchema{Key: "amount", Type: "currency", Config: map[string]any{"precision": 8, "scale": 2, "currency_code": "USD"}}
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}, currency}}
	if _, _, err := recordMutationPredicateSQL(store.store, object, transactionmodel.MutationPredicate{Field: "missing", Operator: "eq", Value: 1}, 1); err == nil {
		t.Fatal("unknown predicate field accepted")
	}
	if _, _, err := recordMutationPredicateSQL(store.store, object, transactionmodel.MutationPredicate{Field: "status", Operator: "contains", Value: 1}, 1); err == nil {
		t.Fatal("unknown predicate operator accepted")
	}
	for _, operator := range []string{"eq", "ne", "lt", "lte", "gt", "gte"} {
		clause, values, err := recordMutationPredicateSQL(store.store, object, transactionmodel.MutationPredicate{Field: "amount", Operator: operator, Value: "1.20"}, 3)
		if err != nil || !strings.Contains(clause, store.store.Placeholder(3)) || len(values) != 1 {
			t.Fatalf("predicate %s clause=%q values=%v err=%v", operator, clause, values, err)
		}
	}
	for operator, fragment := range map[string]string{"eq": "IS NULL", "ne": "IS NOT NULL"} {
		clause, values, err := recordMutationPredicateSQL(store.store, object, transactionmodel.MutationPredicate{Field: "updated_at", Operator: operator}, 1)
		if err != nil || !strings.Contains(clause, fragment) || len(values) != 0 {
			t.Fatalf("nil predicate %s clause=%q values=%v err=%v", operator, clause, values, err)
		}
	}
	if _, _, err := recordMutationPredicateSQL(store.store, object, transactionmodel.MutationPredicate{Field: "status", Operator: "lt"}, 1); err == nil {
		t.Fatal("ordered nil predicate accepted")
	}

	expression := recordmodel.RecordFilterExpression{Operator: "and", Children: []recordmodel.RecordFilterExpression{
		{Operator: "eq", Field: "amount", Value: "1.20", Values: []any{"2.30"}},
		{Operator: "eq", Field: "status", Value: "open"},
	}}
	query := RecordQueryDatabaseValues(testEngineProfile("sqlite"), object, recordmodel.RecordListQuery{FilterExpression: &expression})
	if query.FilterExpression == nil || query.FilterExpression.Children[0].Value == "1.20" || query.FilterExpression.Children[0].Values[0] == "2.30" || query.FilterExpression.Children[1].Value != "open" {
		t.Fatalf("encoded expression=%#v", query.FilterExpression)
	}
	nilValue := recordFilterDBValues(testEngineProfile("sqlite"), map[string]definitionmodel.FieldSchema{"amount": currency}, recordmodel.RecordFilterExpression{Field: "amount", Value: nil, Values: []any{"1.00"}})
	if nilValue.Value != nil || nilValue.Values[0] == "1.00" {
		t.Fatalf("nil currency expression=%#v", nilValue)
	}

	if projection := recordListProjection(store.store, nil); projection != "*" {
		t.Fatalf("default projection=%q", projection)
	}
	projection := recordListProjection(store.store, []string{"id", "status", "status", "updated_at"})
	if strings.Count(projection, store.store.Identifier("status")) != 1 {
		t.Fatalf("deduplicated projection=%q", projection)
	}
	for _, column := range []string{"workspace_id", "id", "created_at", "updated_at", "deleted", "ext_info", "create_by", "update_by"} {
		if strings.Count(projection, store.store.Identifier(column)) != 1 {
			t.Fatalf("Record system projection %s missing or duplicated: %q", column, projection)
		}
	}
	if options := recordScopeReadTxOptions(testEngineProfile("postgres")); options == nil || options.Isolation.String() == "Default" {
		t.Fatalf("postgres read tx options=%#v", options)
	}
	if options := recordScopeReadTxOptions(testEngineProfile("sqlite")); options == nil {
		t.Fatal("sqlite read tx options missing")
	}
}

func recordStoreRelationScope() *recordmodel.RecordScopeExpression {
	return &recordmodel.RecordScopeExpression{
		Operator: "eq", FieldKey: "id", Values: []string{"member-1"},
		Path: []recordmodel.RecordScopePathSegment{{Direction: "forward", RelationFieldKey: "member_id", TargetObjectKey: "member"}},
	}
}

func TestListRecordsRejectsInvalidContractsAndPropagatesRelationSnapshotFailures(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "records", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}
	store := scriptedRecordStore(t, &recordSQLState{})
	invalidFilter := recordmodel.RecordFilterExpression{Operator: "eq", Field: "missing", Value: "x"}
	if _, err := store.ListRecords(t.Context(), "workspace", object, recordmodel.RecordListQuery{FilterExpression: &invalidFilter}); err == nil || !strings.Contains(err.Error(), "normalize record filter") {
		t.Fatalf("invalid filter error=%v", err)
	}
	if _, err := store.ListRecords(t.Context(), "workspace", object, recordmodel.RecordListQuery{SelectFields: []string{"missing"}}); err == nil || !strings.Contains(err.Error(), "normalize record projection") {
		t.Fatalf("invalid projection error=%v", err)
	}
	if _, err := store.ListRecords(t.Context(), "workspace", object, recordmodel.RecordListQuery{ScopeDiagnostic: &recordmodel.RecordScopeDiagnostic{Code: "auth.scope_denied", ObjectKey: object.Key, Detail: "denied"}}); err == nil {
		t.Fatal("scope diagnostic accepted")
	}

	store = scriptedRecordStore(t, &recordSQLState{beginErr: errRecordSQL})
	if _, err := store.ListRecords(t.Context(), "workspace", object, recordmodel.RecordListQuery{ScopeExpression: recordStoreRelationScope()}); !errors.Is(err, errRecordSQL) {
		t.Fatalf("begin relation snapshot error=%v", err)
	}
	for name, step := range map[string]recordSQLQueryStep{
		"query": {err: errRecordSQL},
		"scan":  {columns: []string{"id", "extra"}, rows: [][]driver.Value{{"member-1", "extra"}}},
		"next":  {columns: []string{"id"}, nextErr: errRecordSQL},
	} {
		t.Run(name, func(t *testing.T) {
			store := scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{step}})
			if _, err := store.ListRecords(t.Context(), "workspace", object, recordmodel.RecordListQuery{ScopeExpression: recordStoreRelationScope()}); !errors.Is(err, errRecordSQL) && name != "scan" {
				t.Fatalf("relation %s error=%v", name, err)
			} else if name == "scan" && err == nil {
				t.Fatal("relation scan mismatch accepted")
			}
		})
	}

	badScope := &recordmodel.RecordScopeExpression{Operator: "unsupported"}
	store = scriptedRecordStore(t, &recordSQLState{})
	if _, err := store.ListRecords(t.Context(), "workspace", object, recordmodel.RecordListQuery{Scope: "custom", RootObjectKey: object.Key, ScopeExpression: badScope}); err == nil {
		t.Fatal("invalid persisted scope expression accepted")
	}

	store = scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{
		{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}},
		{columns: []string{"id", "created_at", "updated_at", "status"}, closeErr: errRecordSQL},
	}})
	if _, err := store.ListRecords(t.Context(), "workspace", object, recordmodel.RecordListQuery{Page: 1, PageSize: 10}); !errors.Is(err, errRecordSQL) {
		t.Fatalf("close rows error=%v", err)
	}

	store = scriptedRecordStore(t, &recordSQLState{commitErr: errRecordSQL, querySteps: []recordSQLQueryStep{
		{columns: []string{"id"}, rows: [][]driver.Value{{"member-1"}}},
		{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}},
		{columns: []string{"id", "created_at", "updated_at", "status"}},
	}})
	if _, err := store.ListRecords(t.Context(), "workspace", object, recordmodel.RecordListQuery{Page: 1, PageSize: 10, ScopeExpression: recordStoreRelationScope()}); !errors.Is(err, errRecordSQL) {
		t.Fatalf("commit relation snapshot error=%v", err)
	}
}

func TestRecordMutationRestorePredicateAndOptimisticInspectionFailures(t *testing.T) {
	object, value := recordObjectFixture(), recordFixture()
	restore := transactionmodel.RecordMutationCommit{Operation: "restore", Object: object, Record: value}
	store := scriptedRecordStore(t, &recordSQLState{})
	if err := store.CommitRecordMutation(t.Context(), "workspace", restore); err != nil {
		t.Fatalf("restore mutation=%v", err)
	}

	invalidPredicate := transactionmodel.RecordMutationCommit{
		Operation: "update", Object: object, Record: value,
		Predicates: []transactionmodel.MutationPredicate{{Field: "missing", Operator: "eq", Value: "x"}},
	}
	store = scriptedRecordStore(t, &recordSQLState{})
	if err := store.CommitRecordMutation(t.Context(), "workspace", invalidPredicate); err == nil {
		t.Fatal("invalid mutation predicate accepted")
	}

	optimistic := transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: value, ExpectedUpdatedAt: "expected"}
	for name, state := range map[string]*recordSQLState{
		"missing": {execSteps: []recordSQLExecStep{{rows: 0}}, querySteps: []recordSQLQueryStep{{columns: []string{"updated_at"}}}},
		"query":   {execSteps: []recordSQLExecStep{{rows: 0}}, querySteps: []recordSQLQueryStep{{err: errRecordSQL}}},
		"same":    {execSteps: []recordSQLExecStep{{rows: 0}}, querySteps: []recordSQLQueryStep{{columns: []string{"updated_at"}, rows: [][]driver.Value{{"expected"}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			store := scriptedRecordStore(t, state)
			err := store.CommitRecordMutation(t.Context(), "workspace", optimistic)
			if err == nil {
				t.Fatalf("optimistic %s returned nil", name)
			}
			if name == "query" && !errors.Is(err, errRecordSQL) {
				t.Fatalf("optimistic query error=%v", err)
			}
		})
	}
	noPredicate := transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: value}
	store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{rows: 0}}})
	if err := store.CommitRecordMutation(t.Context(), "workspace", noPredicate); err == nil {
		t.Fatal("zero-row update without predicate accepted")
	}
}

func relatedAggregateCountObject() definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{
		Key:    "booking",
		Fields: []definitionmodel.FieldSchema{{Key: "class_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "class_limit"}}},
		Validations: []definitionmodel.ValidationSchema{{Key: "capacity", Type: "related_aggregate_invariant", Config: map[string]any{
			"relation_field": "class_id", "aggregate": "count", "limit_field": "capacity", "operator": "lte",
		}}},
	}
}

func relatedAggregateSumObject(currency definitionmodel.FieldSchema) definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{
		Key:    "refund",
		Fields: []definitionmodel.FieldSchema{{Key: "payment_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "payment_limit"}}, currency},
		Validations: []definitionmodel.ValidationSchema{{Key: "refund_limit", Type: "related_aggregate_invariant", Config: map[string]any{
			"relation_field": "payment_id", "aggregate": "sum", "value_field": currency.Key, "limit_field": "paid_amount", "operator": "lte",
		}}},
	}
}

func callRelatedAggregateValidation(t *testing.T, state *recordSQLState, object definitionmodel.ObjectSchema, data map[string]any) error {
	t.Helper()
	store := scriptedRecordStore(t, state)
	tx, err := store.database().BeginTx(t.Context(), recordMutationTxOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	return store.validateRelatedAggregateInvariantsTx(t.Context(), tx, "workspace", transactionmodel.RecordMutationCommit{
		Operation: "create", Object: object, Record: recordmodel.Record{ID: "candidate", Data: data},
	}, "create")
}

func TestRelatedAggregateTransactionFailureWindows(t *testing.T) {
	invalidPolicy := definitionmodel.ObjectSchema{Key: "invalid", Validations: []definitionmodel.ValidationSchema{{Key: "bad", Type: "related_aggregate_invariant"}}}
	if err := callRelatedAggregateValidation(t, &recordSQLState{}, invalidPolicy, nil); err == nil {
		t.Fatal("invalid aggregate policy accepted")
	}
	countObject := relatedAggregateCountObject()
	if err := callRelatedAggregateValidation(t, &recordSQLState{}, countObject, map[string]any{}); err == nil {
		t.Fatal("aggregate candidate without relation accepted")
	}

	for name, state := range map[string]*recordSQLState{
		"missing related": {querySteps: []recordSQLQueryStep{{columns: []string{"capacity"}}}},
		"lock error":      {querySteps: []recordSQLQueryStep{{err: errRecordSQL}}},
		"read error": {querySteps: []recordSQLQueryStep{
			{columns: []string{"capacity"}, rows: [][]driver.Value{{int64(2)}}}, {err: errRecordSQL},
		}},
		"scan error": {querySteps: []recordSQLQueryStep{
			{columns: []string{"capacity"}, rows: [][]driver.Value{{int64(2)}}},
			{columns: []string{"id", "extra"}, rows: [][]driver.Value{{"existing", "extra"}}},
		}},
		"iterate error": {querySteps: []recordSQLQueryStep{
			{columns: []string{"capacity"}, rows: [][]driver.Value{{int64(2)}}},
			{columns: []string{"id"}, nextErr: errRecordSQL},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			err := callRelatedAggregateValidation(t, state, countObject, map[string]any{"class_id": "class-1"})
			if err == nil {
				t.Fatalf("%s returned nil", name)
			}
		})
	}
	statusWithoutInclusion := relatedAggregateCountObject()
	statusWithoutInclusion.Fields = append(statusWithoutInclusion.Fields, definitionmodel.FieldSchema{Key: "status", Type: "select"})
	statusWithoutInclusion.Validations[0].Config["status_field"] = "status"
	if err := callRelatedAggregateValidation(t, &recordSQLState{querySteps: []recordSQLQueryStep{
		{columns: []string{"capacity"}, rows: [][]driver.Value{{int64(2)}}},
		{columns: []string{"id", "status"}},
	}}, statusWithoutInclusion, map[string]any{"class_id": "class-1", "status": "reserved"}); err != nil {
		t.Fatalf("status field without inclusion filter: %v", err)
	}

	validCurrency := definitionmodel.FieldSchema{Key: "amount", Type: "currency", Config: map[string]any{"precision": 8, "scale": 2, "currency_code": "USD"}}
	invalidCurrency := validCurrency
	invalidCurrency.Config = map[string]any{"scale": -1}
	for _, test := range []struct {
		name   string
		object definitionmodel.ObjectSchema
		data   map[string]any
		state  *recordSQLState
	}{
		{"zero", relatedAggregateSumObject(invalidCurrency), map[string]any{"payment_id": "payment-1", "amount": "1.00"}, &recordSQLState{querySteps: []recordSQLQueryStep{{columns: []string{"paid_amount"}, rows: [][]driver.Value{{"10.00"}}}, {columns: []string{"id", "amount"}}}}},
		{"existing add", relatedAggregateSumObject(validCurrency), map[string]any{"payment_id": "payment-1", "amount": "1.00"}, &recordSQLState{querySteps: []recordSQLQueryStep{{columns: []string{"paid_amount"}, rows: [][]driver.Value{{"10.00"}}}, {columns: []string{"id", "amount"}, rows: [][]driver.Value{{"existing", true}}}}}},
		{"candidate add", relatedAggregateSumObject(validCurrency), map[string]any{"payment_id": "payment-1", "amount": true}, &recordSQLState{querySteps: []recordSQLQueryStep{{columns: []string{"paid_amount"}, rows: [][]driver.Value{{"10.00"}}}, {columns: []string{"id", "amount"}}}}},
		{"compare", relatedAggregateSumObject(validCurrency), map[string]any{"payment_id": "payment-1", "amount": "1.00"}, &recordSQLState{querySteps: []recordSQLQueryStep{{columns: []string{"paid_amount"}, rows: [][]driver.Value{{true}}}, {columns: []string{"id", "amount"}}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := callRelatedAggregateValidation(t, test.state, test.object, test.data); err == nil {
				t.Fatalf("%s failure accepted", test.name)
			}
		})
	}
}

func temporalExclusionObject() definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{
		Key:    "booking",
		Fields: []definitionmodel.FieldSchema{{Key: "owner", Type: "relation"}, {Key: "starts_at", Type: "datetime"}, {Key: "ends_at", Type: "datetime"}},
		Validations: []definitionmodel.ValidationSchema{{Key: "owner_busy", Type: "temporal_exclusion", Config: map[string]any{
			"start_field": "starts_at", "end_field": "ends_at", "scope_fields": []any{"owner"},
		}}},
	}
}

func callTemporalValidation(t *testing.T, state *recordSQLState, object definitionmodel.ObjectSchema, data map[string]any) error {
	t.Helper()
	store := scriptedRecordStore(t, state)
	tx, err := store.database().BeginTx(t.Context(), recordMutationTxOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	return store.validateTemporalExclusionTx(t.Context(), tx, "workspace", transactionmodel.RecordMutationCommit{
		Operation: "create", Object: object, Record: recordmodel.Record{ID: "candidate", Data: data},
	}, "create")
}

func TestTemporalExclusionTransactionFailureWindows(t *testing.T) {
	invalidPolicy := definitionmodel.ObjectSchema{Key: "invalid", Validations: []definitionmodel.ValidationSchema{{Key: "bad", Type: "temporal_exclusion"}}}
	if err := callTemporalValidation(t, &recordSQLState{}, invalidPolicy, nil); err == nil {
		t.Fatal("invalid temporal policy accepted")
	}
	object := temporalExclusionObject()
	if err := callTemporalValidation(t, &recordSQLState{}, object, map[string]any{"owner": "coach", "starts_at": "bad", "ends_at": "bad"}); err == nil {
		t.Fatal("invalid temporal candidate accepted")
	}
	data := map[string]any{"owner": "coach", "starts_at": "2026-07-22T10:00:00Z", "ends_at": "2026-07-22T11:00:00Z"}
	if err := callTemporalValidation(t, &recordSQLState{querySteps: []recordSQLQueryStep{{columns: []string{"id"}}}}, object, data); err != nil {
		t.Fatalf("no temporal conflict: %v", err)
	}
	if err := callTemporalValidation(t, &recordSQLState{querySteps: []recordSQLQueryStep{{err: errRecordSQL}}}, object, data); !errors.Is(err, errRecordSQL) {
		t.Fatalf("temporal query error=%v", err)
	}
	if err := callTemporalValidation(t, &recordSQLState{querySteps: []recordSQLQueryStep{{columns: []string{"id"}, rows: [][]driver.Value{{"existing"}}}}}, object, data); err == nil {
		t.Fatal("temporal conflict accepted")
	}
	statusWithoutExclusion := temporalExclusionObject()
	statusWithoutExclusion.Fields = append(statusWithoutExclusion.Fields, definitionmodel.FieldSchema{Key: "status", Type: "select"})
	statusWithoutExclusion.Validations[0].Config["status_field"] = "status"
	data["status"] = "confirmed"
	if err := callTemporalValidation(t, &recordSQLState{querySteps: []recordSQLQueryStep{{columns: []string{"id"}}}}, statusWithoutExclusion, data); err != nil {
		t.Fatalf("status field without exclusion filter: %v", err)
	}
}
