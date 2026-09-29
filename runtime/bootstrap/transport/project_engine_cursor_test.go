package transport

import (
	"reflect"
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimeengine"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestProjectRecordCursorContinuesStableDescendingSort(t *testing.T) {
	principal := principalmodel.Principal{}
	principal.Known, principal.WorkspaceID, principal.UserID, principal.RoleKey = true, "workspace-a", "user-a", "sales_rep"
	query := runtimeengine.Query{
		PageSize: 50, Search: "renewal", SearchFields: []string{"subject"},
		Filters: map[string]any{"provider": "google"}, Sorts: []runtimeengine.Sort{{Field: "last_message_at", Direction: "desc"}},
	}
	sorts := projectRecordCursorSorts(query.Sorts)
	wantSorts := []recordmodel.RecordSortRule{{Field: "last_message_at", Direction: "desc"}, {Field: "id", Direction: "asc"}}
	if !reflect.DeepEqual(sorts, wantSorts) || !projectRecordCursorRequired(sorts) {
		t.Fatalf("sorts=%#v", sorts)
	}
	last := recordmodel.Record{ID: "thread-050", QuerySortValues: map[string]any{"last_message_at": "2026-09-29T08:00:00Z"}}
	raw, err := encodeProjectRecordCursor("runtime-a", "email_thread", query, principal, sorts, 99, 2, last)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := decodeProjectRecordCursor(raw, "runtime-a", "email_thread", query, principal, sorts)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.Page != 2 || cursor.Total != 99 {
		t.Fatalf("cursor=%#v", cursor)
	}
	filter, err := projectRecordCursorFilter(cursor, sorts)
	if err != nil {
		t.Fatal(err)
	}
	want := &recordmodel.RecordFilterExpression{Operator: "or", Children: []recordmodel.RecordFilterExpression{
		{Operator: "or", Children: []recordmodel.RecordFilterExpression{
			{Operator: "lt", Field: "last_message_at", Value: "2026-09-29T08:00:00Z"},
			{Operator: "is_null", Field: "last_message_at"},
		}},
		{Operator: "and", Children: []recordmodel.RecordFilterExpression{
			{Operator: "eq", Field: "last_message_at", Value: "2026-09-29T08:00:00Z"},
			{Operator: "gt", Field: "id", Value: "thread-050"},
		}},
	}}
	if !reflect.DeepEqual(filter, want) {
		t.Fatalf("filter=%#v want=%#v", filter, want)
	}
}

func TestProjectRecordCursorBindsPrincipalAndQuery(t *testing.T) {
	principal := principalmodel.Principal{}
	principal.Known, principal.WorkspaceID, principal.UserID = true, "workspace-a", "user-a"
	query := runtimeengine.Query{PageSize: 25, Sorts: []runtimeengine.Sort{{Field: "updated_at", Direction: "desc"}}}
	sorts := projectRecordCursorSorts(query.Sorts)
	raw, err := encodeProjectRecordCursor("runtime-a", "email_draft", query, principal, sorts, 40, 2, recordmodel.Record{
		ID: "draft-25", QuerySortValues: map[string]any{"updated_at": "2026-09-29T08:00:00Z"},
	})
	if err != nil {
		t.Fatal(err)
	}
	changedQuery := query
	changedQuery.Filters = map[string]any{"status": "draft"}
	if _, err = decodeProjectRecordCursor(raw, "runtime-a", "email_draft", changedQuery, principal, sorts); runtimeengine.ErrorCode(err) != "backend.project_engine.pagination_cursor_invalid" {
		t.Fatalf("changed query error=%v", err)
	}
	other := principal
	other.UserID = "user-b"
	if _, err = decodeProjectRecordCursor(raw, "runtime-a", "email_draft", query, other, sorts); runtimeengine.ErrorCode(err) != "backend.project_engine.pagination_cursor_invalid" {
		t.Fatalf("changed principal error=%v", err)
	}
}

func TestProjectRecordCursorPreservesNullsLastBoundary(t *testing.T) {
	principal := principalmodel.Principal{}
	principal.Known, principal.WorkspaceID, principal.UserID = true, "workspace-a", "user-a"
	query := runtimeengine.Query{PageSize: 2, Sorts: []runtimeengine.Sort{{Field: "due_at", Direction: "asc"}}}
	sorts := projectRecordCursorSorts(query.Sorts)
	raw, err := encodeProjectRecordCursor("runtime-a", "task", query, principal, sorts, 4, 2, recordmodel.Record{
		ID: "task-2", QuerySortValues: map[string]any{"due_at": nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := decodeProjectRecordCursor(raw, "runtime-a", "task", query, principal, sorts)
	if err != nil {
		t.Fatal(err)
	}
	filter, err := projectRecordCursorFilter(cursor, sorts)
	if err != nil {
		t.Fatal(err)
	}
	want := &recordmodel.RecordFilterExpression{Operator: "and", Children: []recordmodel.RecordFilterExpression{
		{Operator: "is_null", Field: "due_at"},
		{Operator: "gt", Field: "id", Value: "task-2"},
	}}
	if !reflect.DeepEqual(filter, want) {
		t.Fatalf("filter=%#v want=%#v", filter, want)
	}
}

func TestProjectRecordCursorKeepsIDAscendingFastPath(t *testing.T) {
	sorts := projectRecordCursorSorts(nil)
	if projectRecordCursorRequired(sorts) || !reflect.DeepEqual(sorts, []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}}) {
		t.Fatalf("sorts=%#v", sorts)
	}
}
