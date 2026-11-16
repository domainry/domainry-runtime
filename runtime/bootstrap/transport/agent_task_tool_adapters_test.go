package transport

import (
	"context"
	"reflect"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityscope "github.com/domainry/domainry-identity-sdk/application"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agenthost"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestAgentTaskHostCarriesApplicationScopeIntoBackgroundWorker(t *testing.T) {
	host := runtimeAgentTaskHost{application: identitysdk.ApplicationScope{WorkspaceID: "identity-workspace", ApplicationKey: "aurora"}}
	ctx := host.scopedContext(context.Background(), "business-workspace")
	scope, ok := identityscope.ScopeFromContext(ctx)
	if !ok || scope != host.application || requestcontext.WorkspaceID(ctx) != "business-workspace" {
		t.Fatalf("background task host lost Identity or business Workspace scope: scope=%+v found=%t workspace=%s", scope, ok, requestcontext.WorkspaceID(ctx))
	}
}

type agentCursorRecordService struct {
	agentTaskRecordStub
	list func(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error)
}

func (s agentCursorRecordService) ListRecords(ctx context.Context, object string, query recordmodel.RecordListQuery, principal principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	return s.list(ctx, object, query, principal)
}

func TestAgentQueryForwardsCursorWithoutChangingRecordAuthorization(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "agent-a"}}
	scope := agentapplication.AgentToolFieldScope{VisibleFields: map[string][]string{"evidence": {"status"}}}
	filters := map[string]any{"support_case": "case-a", "case_investigation": "investigation-a"}
	calls := 0
	adapter := agentTaskToolQueryAdapter{records: agentCursorRecordService{list: func(_ context.Context, object string, query recordmodel.RecordListQuery, actor principalmodel.Principal) (recordmodel.RecordPageResult, error) {
		calls++
		if object != "evidence" || !reflect.DeepEqual(actor, principal) || !reflect.DeepEqual(query.Filters, filters) || !reflect.DeepEqual(query.SelectFields, []string{"status"}) || query.AuthorizationMode != "" || query.ScopeExpression != nil || query.OwnerOrganizationScopeID != "" || query.PageSize != 1 || len(query.Sort) != 1 || query.Sort[0] != (recordmodel.RecordSortRule{Field: "id", Direction: "asc"}) {
			t.Fatalf("record query lost its principal, scope or filters: query=%#v actor=%#v", query, actor)
		}
		if calls == 1 && query.AfterID != "" || calls == 2 && query.AfterID != "41" {
			t.Fatalf("call %d cursor=%q", calls, query.AfterID)
		}
		next := ""
		if calls == 1 {
			next = "41"
		}
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{{RecordID: "row", Data: map[string]any{"status": "succeeded", "private": "hidden"}}}, PageSize: 1, HasNext: calls == 1, NextAfterID: next}, nil
	}}}
	raw := map[string]any{"page_size": 1, "filters": filters, "sort": []any{map[string]any{"field": "id", "direction": "asc"}}, "select_fields": []any{"private"}, "authorization_mode": "unrestricted", "owner_organization_scope_id": "another-org"}
	first, err := adapter.QueryAgentRecords(t.Context(), "evidence", raw, scope, principal)
	if err != nil {
		t.Fatal(err)
	}
	page := first.(recordmodel.RecordPageResult)
	if !page.HasNext || page.NextAfterID != "41" || len(page.Items[0].Data) != 1 {
		t.Fatalf("first page=%#v", page)
	}
	raw["after_id"] = page.NextAfterID
	last, err := adapter.QueryAgentRecords(t.Context(), "evidence", raw, scope, principal)
	if err != nil || last.(recordmodel.RecordPageResult).HasNext || last.(recordmodel.RecordPageResult).NextAfterID != "" || calls != 2 {
		t.Fatalf("last page=%#v calls=%d err=%v", last, calls, err)
	}
}

func TestAgentQueryRejectsNonStringCursorsBeforeRecordAccess(t *testing.T) {
	for _, value := range []any{42, float64(42), true, []any{"42"}, map[string]any{"cursor": "42"}} {
		adapter := agentTaskToolQueryAdapter{records: agentCursorRecordService{list: func(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error) {
			t.Fatal("malformed cursor reached Record service")
			return recordmodel.RecordPageResult{}, nil
		}}}
		if _, err := adapter.QueryAgentRecords(t.Context(), "evidence", map[string]any{"after_id": value}, agentapplication.AgentToolFieldScope{}, principalmodel.Principal{}); apperror.CodeOf(err) != "agent.tool.query_invalid" {
			t.Fatalf("cursor type %T err=%v", value, err)
		}
	}
	for _, code := range []string{"backend.record.pagination_cursor_invalid", "backend.record.pagination_cursor_sort_invalid", "backend.role.permission_denied"} {
		want := apperror.New(apperror.KindForbidden, code, nil, nil)
		adapter := agentTaskToolQueryAdapter{records: agentTaskRecordStub{err: want}}
		if _, err := adapter.QueryAgentRecords(t.Context(), "evidence", map[string]any{"after_id": "42"}, agentapplication.AgentToolFieldScope{}, principalmodel.Principal{}); err != want {
			t.Fatalf("Record service refusal changed: %v", err)
		}
	}
}

func TestAgentQueryRetainsCursorWhenExpandingRelations(t *testing.T) {
	page := recordmodel.RecordPageResult{Items: []recordmodel.Record{{RecordID: "customer-1", Data: map[string]any{"name": "Ada", "secret": "hidden"}}}, HasNext: true, NextAfterID: "42"}
	adapter := agentTaskToolQueryAdapter{records: agentTaskRecordStub{page: page}}
	scope := agentapplication.AgentToolFieldScope{VisibleFields: map[string][]string{"customer": {"name"}, "order": {"number"}}}
	result, err := adapter.QueryAgentRecords(t.Context(), "customer", map[string]any{"relations": []any{map[string]any{"object_key": "order", "limit": 1}}}, scope, principalmodel.Principal{})
	if err != nil || result.(map[string]any)["next_after_id"] != "42" {
		t.Fatalf("relation expansion lost root cursor: result=%#v err=%v", result, err)
	}
}

func TestDecodeAgentRecordQueryBoundsSortAndRelations(t *testing.T) {
	scope := agentapplication.AgentToolFieldScope{VisibleFields: map[string][]string{"customer": {"name"}, "order": {"number"}}}
	query, relations, err := decodeAgentRecordQuery(map[string]any{
		"page": 2, "page_size": 25, "search": " Ada ", "filters": map[string]any{"active": true},
		"sort":      []any{map[string]any{"field": "name", "direction": "DESC"}},
		"relations": []any{map[string]any{"object_key": "order", "field_key": "customer_id", "limit": 10}},
	}, scope)
	if err != nil || query.Page != 2 || query.PageSize != 25 || query.Search != "Ada" || len(query.Sort) != 1 || query.Sort[0].Direction != "desc" || len(relations) != 1 || relations[0].ObjectKey != "order" {
		t.Fatalf("query=%#v relations=%#v err=%v", query, relations, err)
	}

	tests := []struct {
		name string
		raw  map[string]any
		code string
	}{
		{name: "page size", raw: map[string]any{"page_size": 26}, code: "agent.tool.query_invalid"},
		{name: "sort direction", raw: map[string]any{"sort": []any{map[string]any{"field": "name", "direction": "sideways"}}}, code: "agent.tool.query_invalid"},
		{name: "relation denied", raw: map[string]any{"relations": []any{map[string]any{"object_key": "secret"}}}, code: "agent.tool.relation_denied"},
		{name: "relation row limit", raw: map[string]any{"relations": []any{map[string]any{"object_key": "order", "limit": 11}}}, code: "agent.tool.relation_invalid"},
	}
	tooMany := []any{}
	for index := 0; index < 6; index++ {
		tooMany = append(tooMany, map[string]any{"object_key": "order"})
	}
	tests = append(tests, struct {
		name string
		raw  map[string]any
		code string
	}{name: "relation type limit", raw: map[string]any{"relations": tooMany}, code: "agent.tool.relation_limit_exceeded"})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := decodeAgentRecordQuery(test.raw, scope); apperror.CodeOf(err) != test.code {
				t.Fatalf("code=%q err=%v", apperror.CodeOf(err), err)
			}
		})
	}
}

func TestProjectAgentRecordRemovesEveryNonVisibleField(t *testing.T) {
	record := recordmodel.Record{RecordID: "customer-1", Data: map[string]any{"name": "Ada", "email": "visible only to another role", "secret": "never expose"}}
	projectAgentRecord(&record, []string{"name"})
	if len(record.Data) != 1 || record.Data["name"] != "Ada" {
		t.Fatalf("projected record=%#v", record)
	}
	page := recordmodel.RecordPageResult{Items: []recordmodel.Record{{Data: map[string]any{"number": "SO-1", "margin": 42}}}}
	projectAgentRecordPage(&page, []string{"number"})
	if len(page.Items[0].Data) != 1 || page.Items[0].Data["number"] != "SO-1" {
		t.Fatalf("projected page=%#v", page)
	}
}

func TestAgentToolPrimitiveDecodersRejectShapeConfusion(t *testing.T) {
	if got := agentToolInt(float64(4), 1); got != 4 {
		t.Fatalf("int=%d", got)
	}
	if got := agentToolInt("4", 1); got != 1 || len(agentToolMapValue([]any{})) != 0 || agentToolList(map[string]any{}) != nil {
		t.Fatalf("shape coercion int=%d map=%#v list=%#v", got, agentToolMapValue([]any{}), agentToolList(map[string]any{}))
	}
}
