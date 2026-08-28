package validation

import (
	"encoding/json"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func recordQueryFixture() (definitionmodel.ObjectSchema, definitionmodel.ViewSchema) {
	object := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}, {Key: "amount", Type: "number"}, {Key: "status", Type: "select"}, {Key: "active", Type: "boolean"}}}
	view := definitionmodel.ViewSchema{Key: "orders", ObjectKey: "order", Config: map[string]any{"business_view": "list", "page_size": float64(50), "search_fields": []any{"name", "missing"}, "sort": []any{"-amount", map[string]any{"field": "created_at", "direction": "desc"}}, "filters": []any{map[string]any{"key": "mine", "field": "name", "source": "current_user"}, map[string]any{"key": "fixed", "field": "status", "value": "open"}}}}
	return object, view
}

func TestRecordSelectAndNormalizeListQuery(t *testing.T) {
	object, view := recordQueryFixture()
	views := []definitionmodel.ViewSchema{{Key: "other", ObjectKey: "other"}, view, {Key: "fallback", ObjectKey: "order", Config: map[string]any{}}}
	if got := RecordSelectListView(views, "order", "orders"); got.Key != "orders" {
		t.Fatalf("%#v", got)
	}
	if got := RecordSelectListView(views, "order", ""); got.Key != "orders" {
		t.Fatalf("%#v", got)
	}
	if got := RecordSelectListView(views, "order", "missing"); got.Key != "orders" {
		t.Fatalf("%#v", got)
	}
	if got := RecordSelectListView(views, "missing", ""); got.Config == nil {
		t.Fatalf("%#v", got)
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{UserID: "u1", WorkspaceID: "w1", DepartmentPath: "/d", ReportingPath: "/r", OrganizationScopes: identitysdk.OrganizationScopes{TeamIDs: []string{"t"}, StoreIDs: []string{"s"}, TerritoryIDs: []string{"x"}, WarehouseIDs: []string{"wh"}}}}
	query := recordmodel.RecordListQuery{Page: -1, PageSize: 300, Search: "Ada", Filters: map[string]any{"amount__gte": "10", "amount__lte": "bad", "id__in": []any{" 1 ", "1", nil}, "status__in": []string{"open", "open"}, "unknown__in": []string{"x"}, "name": " Ada ", "active": "bad", "mine": true, "fixed": true, "": "x"}}
	got := RecordNormalizeListQuery(object, view, query, principal)
	if got.Page != 1 || got.PageSize != 200 || len(got.SearchFields) != 1 || got.SearchFields[0] != "name" || len(got.Sort) != 3 || got.Sort[2].Field != "id" {
		t.Fatalf("%#v", got)
	}
	if got.Filters["amount__gte"] != float64(10) || len(got.Filters["id__in"].([]any)) != 1 || got.Filters["name"] != "u1" || got.PrincipalWorkspaceID != "w1" {
		t.Fatalf("%#v", got)
	}
	for range 100 {
		if repeated := RecordNormalizeListQuery(object, view, query, principal); repeated.Filters["name"] != "u1" {
			t.Fatalf("view filter precedence is nondeterministic: %#v", repeated.Filters)
		}
	}
	query = recordmodel.RecordListQuery{Search: "x"}
	emptyView := definitionmodel.ViewSchema{Config: map[string]any{"page_size": -1}}
	got = RecordNormalizeListQuery(object, emptyView, query, principal)
	if got.PageSize != 25 || len(got.SearchFields) != 2 || len(got.Sort) != 1 || got.Sort[0].Field != "id" {
		t.Fatalf("%#v", got)
	}
}

func TestRecordQueryHelpers(t *testing.T) {
	object, view := recordQueryFixture()
	for input, want := range map[string]struct {
		base, op string
		ok       bool
	}{"amount__gte": {"amount", "gte", true}, "x": {"x", "", false}} {
		b, o, found := splitFilterOperator(input)
		if b != want.base || o != want.op || found != want.ok {
			t.Fatalf("%s", input)
		}
	}
	values := normalizedListFilterValues(definitionmodel.FieldSchema{Type: "number"}, []any{"1", 1, "bad", nil}, false)
	if len(values) != 1 {
		t.Fatalf("%#v", values)
	}
	identityValues := make([]any, 205)
	for i := range identityValues {
		identityValues[i] = i
	}
	if got := normalizedListFilterValues(definitionmodel.FieldSchema{}, identityValues, true); len(got) != 200 {
		t.Fatalf("%d", len(got))
	}
	if normalizedListFilterValues(definitionmodel.FieldSchema{}, "bad", true) != nil {
		t.Fatal("scalar list")
	}
	if key, value, ok := resolveViewFilter(view, "fixed", principalmodel.Principal{}); !ok || key != "status" || value != "open" {
		t.Fatalf("%s %#v %v", key, value, ok)
	}
	badView := definitionmodel.ViewSchema{Config: map[string]any{"filters": []any{"bad", map[string]any{"key": "x", "field": "name"}}}}
	if _, _, ok := resolveViewFilter(badView, "missing", principalmodel.Principal{}); ok {
		t.Fatal("missing filter")
	}
	if _, _, ok := resolveViewFilter(definitionmodel.ViewSchema{Config: map[string]any{"filters": "bad"}}, "x", principalmodel.Principal{}); ok {
		t.Fatal("invalid filters")
	}
	if got := allowedFieldKeys(object, []string{" name ", "missing"}); len(got) != 1 {
		t.Fatalf("%#v", got)
	}
	if got := defaultSearchFields(object); len(got) != 2 {
		t.Fatalf("%#v", got)
	}
	sorts := allowedSortRules(object, []recordmodel.RecordSortRule{{Field: " amount ", Direction: "DESC"}, {Field: "updated_at"}, {Field: "missing"}})
	if len(sorts) != 2 || sorts[0].Direction != "desc" || sorts[1].Direction != "asc" {
		t.Fatalf("%#v", sorts)
	}
	if !RecordFieldExists(object, "name") || RecordFieldExists(object, "missing") || !metaFieldExists("id") || metaFieldExists("missing") {
		t.Fatal("field exists")
	}
	for value, want := range map[any]int{int(1): 1, int64(2): 2, float64(3): 3, json.Number("4"): 4, "bad": 9} {
		if got := RecordIntFromAny(value, 9); got != want {
			t.Fatalf("%#v=%d", value, got)
		}
	}
	if got := RecordIntFromAny(json.Number("bad"), 7); got != 7 {
		t.Fatalf("%d", got)
	}
	if got := sortRulesFromAny([]any{map[string]any{"field": "name", "direction": "desc"}, "-amount", "created_at desc", 1}); len(got) != 3 {
		t.Fatalf("%#v", got)
	}
	if got := sortRulesFromAny("bad"); len(got) != 0 {
		t.Fatalf("%#v", got)
	}
	for input, want := range map[string]struct{ field, direction string }{"": {"", "asc"}, "-amount": {"amount", "desc"}, "name DESC": {"name", "desc"}, "name asc": {"name", "asc"}} {
		f, d := splitSortRule(input)
		if f != want.field || d != want.direction {
			t.Fatalf("%q => %s %s", input, f, d)
		}
	}
}
