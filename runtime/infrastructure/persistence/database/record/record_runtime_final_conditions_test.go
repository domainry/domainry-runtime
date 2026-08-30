package record

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	ormbuilder "github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func TestRecordActionTransactionContextAndLockSQLMatrix(t *testing.T) {
	store := scriptedRecordStore(t, &recordSQLState{})
	ctx := WithActionExecutionTransaction(t.Context(), store.database())
	if actionExecutionTransaction(ctx) == nil || ActionExecutionTransaction(ctx) == nil || store.queryExecutor(ctx) == nil {
		t.Fatal("action transaction context was not propagated")
	}
	for _, test := range []struct {
		driver string
		skip   bool
		want   string
	}{
		{"sqlite", false, ""},
		{"mysql", false, " FOR UPDATE"},
		{"postgres", true, " FOR UPDATE SKIP LOCKED"},
	} {
		builder, err := testEngineProfile(test.driver).ApplyClaimLock(ormbuilder.NewSelectBuilder(store.store.SQLRenderer, "records").Columns("id"), test.skip)
		if err != nil {
			t.Fatal(err)
		}
		statement, _, err := builder.Build()
		if err != nil || !strings.HasSuffix(statement, test.want) {
			t.Fatalf("driver=%s statement=%q err=%v", test.driver, statement, err)
		}
	}
}

func TestRecordCandidateScopeLogicalAndPlannedRelationMatrix(t *testing.T) {
	store := RecordStore{}
	candidate := recordmodel.Record{ID: "order-1", Data: map[string]any{"status": "open"}}
	equals := func(value string) recordmodel.RecordScopeExpression {
		return recordmodel.RecordScopeExpression{Operator: "eq", FieldKey: "status", Values: []string{value}}
	}
	for _, expression := range []recordmodel.RecordScopeExpression{
		{Operator: "and", Children: []recordmodel.RecordScopeExpression{equals("open")}},
		{Operator: "or", Children: []recordmodel.RecordScopeExpression{equals("open")}},
		{Operator: "not"},
	} {
		if _, err := store.candidateScopeMatches(t.Context(), "workspace", candidate, expression, nil); err == nil {
			t.Fatalf("invalid logical expression accepted: %#v", expression)
		}
	}
	and := recordmodel.RecordScopeExpression{Operator: "and", Children: []recordmodel.RecordScopeExpression{equals("open"), equals("closed")}}
	if matched, err := store.candidateScopeMatches(t.Context(), "workspace", candidate, and, nil); err != nil || matched {
		t.Fatalf("and matched=%v err=%v", matched, err)
	}
	or := recordmodel.RecordScopeExpression{Operator: "or", Children: []recordmodel.RecordScopeExpression{equals("closed"), equals("open")}}
	if matched, err := store.candidateScopeMatches(t.Context(), "workspace", candidate, or, nil); err != nil || !matched {
		t.Fatalf("or matched=%v err=%v", matched, err)
	}
	or = recordmodel.RecordScopeExpression{Operator: "or", Children: []recordmodel.RecordScopeExpression{equals("closed"), equals("pending")}}
	if matched, err := store.candidateScopeMatches(t.Context(), "workspace", candidate, or, nil); err != nil || matched {
		t.Fatalf("false or matched=%v err=%v", matched, err)
	}
	negated := recordmodel.RecordScopeExpression{Operator: "not", Children: []recordmodel.RecordScopeExpression{equals("closed")}}
	if matched, err := store.candidateScopeMatches(t.Context(), "workspace", candidate, negated, nil); err != nil || !matched {
		t.Fatalf("not matched=%v err=%v", matched, err)
	}
	errorChild := recordmodel.RecordScopeExpression{Operator: "not"}
	for _, expression := range []recordmodel.RecordScopeExpression{
		{Operator: "and", Children: []recordmodel.RecordScopeExpression{equals("open"), errorChild}},
		{Operator: "or", Children: []recordmodel.RecordScopeExpression{equals("closed"), errorChild}},
	} {
		if _, err := store.candidateScopeMatches(t.Context(), "workspace", candidate, expression, nil); err == nil {
			t.Fatalf("child error ignored: %#v", expression)
		}
	}

	planned := map[string]map[string]recordmodel.Record{"line": {
		"line-1": {ID: "line-1", Data: map[string]any{"order_id": "order-1"}},
		"line-2": {ID: "line-2", Data: map[string]any{"order_id": "other"}},
	}}
	reverse := recordmodel.RecordScopePathSegment{Direction: "reverse", RelationFieldKey: "order_id", TargetObjectKey: "line"}
	if got := plannedRelationCandidates(candidate, reverse, planned); len(got) != 1 || got[0].ID != "line-1" {
		t.Fatalf("reverse candidates=%#v", got)
	}
	forward := recordmodel.RecordScopePathSegment{Direction: "forward", RelationFieldKey: "line_id", TargetObjectKey: "line"}
	if got := plannedRelationCandidates(candidate, forward, planned); got != nil {
		t.Fatalf("missing forward candidate=%#v", got)
	}
	if got := plannedRelationCandidates(candidate, recordmodel.RecordScopePathSegment{Direction: "unknown", TargetObjectKey: "line"}, planned); got != nil {
		t.Fatalf("unknown direction candidates=%#v", got)
	}
	withRelation := candidate
	withRelation.Data["line_id"] = "line-1"
	badRemaining := recordmodel.RecordScopeExpression{Operator: "unsupported", Path: []recordmodel.RecordScopePathSegment{forward}}
	if _, err := store.candidateScopeMatches(t.Context(), "workspace", withRelation, badRemaining, planned); err == nil {
		t.Fatal("planned relation child error ignored")
	}
}

func TestRecordListWithActionTransactionCoversLockAndRelationSnapshotBypass(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "records", Fields: []definitionmodel.FieldSchema{{Key: "member_id", Type: "text"}}}
	state := &recordSQLState{querySteps: []recordSQLQueryStep{
		{columns: []string{"id"}, rows: [][]driver.Value{{"member-1"}}},
		{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}},
		{columns: []string{"id", "created_at", "updated_at", "member_id"}},
	}}
	store := scriptedRecordStore(t, state)
	ctx := WithActionExecutionTransaction(t.Context(), store.database())
	page, err := store.ListRecords(ctx, "workspace", object, recordmodel.RecordListQuery{Page: 1, PageSize: 10, LockIntent: recordmodel.RecordQueryLockForUpdate, ScopeExpression: recordStoreRelationScope()})
	if err != nil || page.Total != 0 {
		t.Fatalf("transactional relation page=%#v err=%v", page, err)
	}
}

func TestRecordRowCodecIntegerAndPublicWrapperMatrix(t *testing.T) {
	field := definitionmodel.FieldSchema{Key: "count", Type: "integer"}
	for input, want := range map[any]any{int64(1): int64(1), int(2): int64(2), float64(3): int64(3), "4": int64(4), "bad": "bad", true: true} {
		if got := NormalizeRecordDatabaseValue(testEngineProfile("sqlite"), field, input); got != want {
			t.Fatalf("integer %T=%v want=%v", input, got, want)
		}
	}
	if got := RecordDatabaseFieldValue(testEngineProfile("sqlite"), definitionmodel.FieldSchema{Type: "text"}, "value"); got != "value" {
		t.Fatalf("database field value=%v", got)
	}
}

func TestRecordMutationNotificationPersistenceFailureIsReturned(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	store := scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{rows: 1}, {err: errors.New("notification")}}})
	commit := transactionmodel.RecordMutationCommit{
		Operation: "create",
		Object:    definitionmodel.ObjectSchema{Key: "order"},
		Record:    recordmodel.Record{ID: "order-1", CreatedAt: now, UpdatedAt: now, Data: map[string]any{}},
		NotificationEvents: []notificationmodel.NotificationEvent{{
			ID: "notification-1", Source: "record", SourceEventID: "order-1-created", EventType: "record.created", Category: "business", Severity: "info", Surface: "business_workspace", RecipientUserIDs: []string{"user"}, ActionState: "none", OccurredAt: now, Snapshot: notificationmodel.NotificationInboxSnapshot{Title: "Created", Body: "Created"}, Status: "queued", CreatedAt: now, UpdatedAt: now,
		}},
	}
	if err := store.ApplyRecordMutationTx(t.Context(), store.database(), "workspace", commit); err == nil {
		t.Fatal("notification persistence failure ignored")
	}
}
