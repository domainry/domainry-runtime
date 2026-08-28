package transport

import (
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

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
	record := recordmodel.Record{ID: "customer-1", Data: map[string]any{"name": "Ada", "email": "visible only to another role", "secret": "never expose"}}
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
