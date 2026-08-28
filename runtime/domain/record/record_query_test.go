package record

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"reflect"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestNormalizeListQueryUsesViewAndProjectsPrincipalScope(t *testing.T) {
	object := definitionmodel.ObjectSchema{
		Key: "customer",
		Fields: []definitionmodel.FieldSchema{
			{Key: "name", Type: "text"},
			{Key: "owner_id", Type: "user"},
			{Key: "amount", Type: "number"},
		},
	}
	view := definitionmodel.ViewSchema{
		Key:       "customers",
		ObjectKey: "customer",
		Config: map[string]any{
			"page_size":     40,
			"search_fields": []any{"name", "missing"},
			"sort":          []any{"-amount"},
			"filters":       []any{map[string]any{"key": "mine", "field": "owner_id", "source": "current_user"}},
		},
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user-1",
		WorkspaceID:    "workspace-1",
		DepartmentPath: "sales/east", OrganizationScopes: identitysdk.OrganizationScopes{TeamIDs: []string{"team-1"}}},
	}, accessfixture.Bundle{
		RecordScope:  "department",
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "department", Read: true}},
	},
	)

	query := recordvalidation.RecordNormalizeListQuery(object, view, recordmodel.RecordListQuery{
		Filters: map[string]any{"mine": true},
	}, principal)

	if query.Page != 1 || query.PageSize != 40 {
		t.Fatalf("pagination = page %d size %d", query.Page, query.PageSize)
	}
	if !reflect.DeepEqual(query.SearchFields, []string{"name"}) {
		t.Fatalf("search fields = %#v", query.SearchFields)
	}
	if got := query.Filters["owner_id"]; got != "user-1" {
		t.Fatalf("current-user filter = %#v", got)
	}
	if !reflect.DeepEqual(query.Sort, []recordmodel.RecordSortRule{{Field: "amount", Direction: "desc"}, {Field: "id", Direction: "asc"}}) {
		t.Fatalf("sort = %#v", query.Sort)
	}
	if query.Scope != "identity_policy" || query.PrincipalUserID != "user-1" || query.PrincipalWorkspaceID != "workspace-1" {
		t.Fatalf("principal scope projection = %#v", query)
	}
	if query.PrincipalDepartmentPath != "sales/east" || !reflect.DeepEqual(query.PrincipalTeamIDs, []string{"team-1"}) {
		t.Fatalf("principal organization projection = %#v", query)
	}
}

func TestNormalizeListQueryBoundsAndSanitizesFilters(t *testing.T) {
	object := definitionmodel.ObjectSchema{
		Key: "employee_profile",
		Fields: []definitionmodel.FieldSchema{
			{Key: "identity_user", Type: "relation", Config: map[string]any{"object_key": "identity_user"}},
			{Key: "score", Type: "number"},
		},
	}
	query := recordvalidation.RecordNormalizeListQuery(object, definitionmodel.ViewSchema{Config: map[string]any{}}, recordmodel.RecordListQuery{
		PageSize: 999,
		Filters: map[string]any{
			"identity_user__in": []any{"u2", "u1", "u1", ""},
			"id__in":            []string{"p2", "p1"},
			"score__gte":        "10",
			"unknown__in":       []any{"leak"},
		},
	}, accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{RecordScope: "all_records"}))

	if query.PageSize != 200 {
		t.Fatalf("bounded page size = %d", query.PageSize)
	}
	if !reflect.DeepEqual(query.Filters["identity_user__in"], []any{"u2", "u1"}) ||
		!reflect.DeepEqual(query.Filters["id__in"], []any{"p2", "p1"}) {
		t.Fatalf("normalized in filters = %#v", query.Filters)
	}
	if _, exists := query.Filters["unknown__in"]; exists {
		t.Fatalf("unknown filter survived: %#v", query.Filters)
	}
	if query.Filters["score__gte"] != float64(10) {
		t.Fatalf("normalized range filter = %#v", query.Filters["score__gte"])
	}
}

func TestSelectListViewPrefersRequestedThenBusinessList(t *testing.T) {
	views := []definitionmodel.ViewSchema{
		{Key: "board", ObjectKey: "customer", Config: map[string]any{"business_view": "kanban"}},
		{Key: "list", ObjectKey: "customer", Config: map[string]any{"business_view": "list"}},
	}
	if got := recordvalidation.RecordSelectListView(views, "customer", "board"); got.Key != "board" {
		t.Fatalf("requested view = %q", got.Key)
	}
	if got := recordvalidation.RecordSelectListView(views, "customer", ""); got.Key != "list" {
		t.Fatalf("default list view = %q", got.Key)
	}
}
