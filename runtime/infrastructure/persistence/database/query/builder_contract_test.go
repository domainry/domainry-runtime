package query

import (
	"reflect"
	"strings"
	"testing"

	ormquery "github.com/domainry/domainry-orm/query"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestWorkspaceSetPredicateRequiresAnExplicitValidatedSet(t *testing.T) {
	store := fuzzQueryStore{}
	empty, err := BuildWorkspaceSetPredicate(store, nil, recordmodel.RecordListQuery{AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted})
	if err != nil {
		t.Fatal(err)
	}
	where, args, err := ormquery.PreparePredicate(storeRenderer{store}, empty, 0)
	if err != nil || !strings.Contains(where, "1 = 0") || len(args) != 0 {
		t.Fatalf("empty where=%s args=%#v err=%v", where, args, err)
	}
	set, err := BuildWorkspaceSetPredicate(store, []string{"workspace-b", "workspace-a", "workspace-b"}, recordmodel.RecordListQuery{AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, Filters: map[string]any{"workspace_id": "outside", "status": "paid"}})
	if err != nil {
		t.Fatal(err)
	}
	where, args, err = ormquery.PreparePredicate(storeRenderer{store}, set, 0)
	if err != nil || strings.Count(where, "workspace_id") != 1 || !reflect.DeepEqual(args, []any{"workspace-b", "workspace-a", "paid"}) {
		t.Fatalf("set where=%s args=%#v err=%v", where, args, err)
	}
	if _, err := BuildWorkspaceSetPredicate(store, []string{" "}, recordmodel.RecordListQuery{AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted}); err == nil {
		t.Fatal("invalid Workspace ID was accepted")
	}
}

func TestTenantWhereScopeAndFilterContract(t *testing.T) {
	store := fuzzQueryStore{}
	for _, invalid := range []string{"owned_records", "organization", "organization_and_children", "team", "department", "department_and_children", "subordinates", "store", "territory", "warehouse", "filtered_records"} {
		if _, _, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{AuthorizationMode: recordmodel.RecordQueryAuthorizationMode(invalid)}); err == nil {
			t.Fatalf("legacy scope %q accepted", invalid)
		}
	}

	query := recordmodel.RecordListQuery{
		AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted,
		Search:            " Needle ", SearchFields: []string{"name", "code"},
		Filters: map[string]any{"workspace_id": "other", "empty": "", "plain": 2, "minimum__gte": int64(3), "maximum__lte": float32(4), "ids__in": []any{"a", "", 5}},
	}
	where, args, err := BuildTenantWhere(store, "workspace-a", query)
	if err != nil || !strings.Contains(where, "LOWER") || !strings.Contains(where, `"minimum" >=`) || !strings.Contains(where, `"maximum" <=`) || !strings.Contains(where, `"ids" IN`) || len(args) != 8 {
		t.Fatalf("search/filter where=%s args=%#v err=%v", where, args, err)
	}
	if _, args, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, Search: "needle"}); err != nil || len(args) != 1 {
		t.Fatalf("search without fields args=%#v err=%v", args, err)
	}
	where, args, err = BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, Search: `50%_off~today`, SearchFields: []string{"name"}})
	if err != nil || !strings.Contains(where, "ESCAPE '~'") || !reflect.DeepEqual(args, []any{"workspace-a", `%50~%~_off~~today%`}) {
		t.Fatalf("literal contains search where=%s args=%#v err=%v", where, args, err)
	}
}

func TestTenantWhereAllKeepsTenantAndBusinessFiltersWithoutScopePredicate(t *testing.T) {
	where, args, err := BuildTenantWhere(fuzzQueryStore{}, "workspace-a", recordmodel.RecordListQuery{
		AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted,
		Filters:           map[string]any{"id__in": []any{"candidate"}, "status": "open"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(where, `"workspace_id" = $1`) || !strings.Contains(where, `"id" IN ($2)`) || !strings.Contains(where, `"status" = $3`) {
		t.Fatalf("all lost tenant or business filters: where=%s args=%#v", where, args)
	}
	if strings.Contains(where, "owner_user_id") || strings.Contains(where, "owner_org_id") || strings.Contains(where, "EXISTS (") {
		t.Fatalf("all unexpectedly added a data-scope predicate: where=%s", where)
	}
	if !reflect.DeepEqual(args, []any{"workspace-a", "candidate", "open"}) {
		t.Fatalf("all args=%#v", args)
	}
}

func TestTenantWhereCatalogOwnedRecordLookupOnlyNarrowsWorkspaceAndDataScope(t *testing.T) {
	where, args, err := BuildTenantWhere(fuzzQueryStore{}, "workspace-a", recordmodel.RecordListQuery{
		AuthorizationMode:        recordmodel.RecordQueryAuthorizationUnrestricted,
		OwnerOrganizationScopeID: "store-north",
		Filters:                  map[string]any{"status": "active"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(where, `"workspace_id" = $1`) || !strings.Contains(where, `"owner_org_id" = $2`) || !strings.Contains(where, `"status" = $3`) {
		t.Fatalf("catalog owner scope did not remain conjunctive: where=%s", where)
	}
	if !reflect.DeepEqual(args, []any{"workspace-a", "store-north", "active"}) {
		t.Fatalf("args=%#v", args)
	}
}

func TestTenantWhereCompilesStableOwnershipIDUnion(t *testing.T) {
	expression := recordmodel.RecordScopeExpression{Operator: "or", Children: []recordmodel.RecordScopeExpression{
		{Operator: "eq", FieldKey: "owner_user_id", Values: []string{"user-1"}},
		{Operator: "in", FieldKey: "owner_org_id", Values: []string{"sales", "store-1"}},
	}}
	where, args, err := BuildTenantWhere(fuzzQueryStore{}, "workspace-a", recordmodel.RecordListQuery{
		AuthorizationMode: recordmodel.RecordQueryAuthorizationPredicate, RootObjectKey: "case", ScopeExpression: &expression,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(where, `("case"."owner_user_id" = $2 OR "case"."owner_org_id" IN ($3, $4))`) ||
		!reflect.DeepEqual(args, []any{"workspace-a", "user-1", "sales", "store-1"}) {
		t.Fatalf("where=%s args=%#v", where, args)
	}
}

func TestQueryBuilderHelperContracts(t *testing.T) {
	store := fuzzQueryStore{}
	args := []any{}
	if clause := buildScopeComparison(store, "r", "path", "eq", nil, &args); clause != "1 = 0" {
		t.Fatalf("empty scope clause=%s", clause)
	}
	if clause := buildScopeComparison(store, "r", "path", "prefix", []string{" "}, &args); clause != "1 = 0" {
		t.Fatalf("blank prefix scope clause=%s", clause)
	}
	args = nil
	if clause := buildScopeComparison(store, "r", "path", "prefix", []string{" /company "}, &args); !strings.Contains(clause, "LIKE") || len(args) != 2 {
		t.Fatalf("prefix scope clause=%s args=%v", clause, args)
	}
	args = nil
	if clause := inAnyClause(store, "id", []string{"a", "b"}, &args); clause != `"id" IN ($1, $2)` || !reflect.DeepEqual(args, []any{"a", "b"}) {
		t.Fatalf("string in clause=%s args=%#v", clause, args)
	}
	args = nil
	if clause := inAnyClause(store, "id", []any{"", nil}, &args); clause != "1 = 0" || len(args) != 0 {
		t.Fatalf("empty any clause=%s args=%#v", clause, args)
	}
	if clause := inAnyClause(store, "id", 42, &args); clause != "1 = 0" {
		t.Fatalf("unsupported any clause=%s", clause)
	}
	args = nil
	if clause := inClause(store, "id", []string{" ", "a"}, &args); clause != `"id" IN ($1)` || !reflect.DeepEqual(args, []any{"a"}) {
		t.Fatalf("string clause=%s args=%#v", clause, args)
	}
	args = nil
	if clause := inClause(store, "id", []string{" "}, &args); clause != "1 = 0" {
		t.Fatalf("blank string clause=%s", clause)
	}

	if order := BuildOrder(store, recordmodel.RecordListQuery{}); order != ` ORDER BY "id" ASC` {
		t.Fatalf("default order=%s", order)
	}
	order := BuildOrder(store, recordmodel.RecordListQuery{Sort: []recordmodel.RecordSortRule{{Field: "created_at", Direction: "desc"}, {Field: "name", Direction: "invalid"}}})
	if order != ` ORDER BY "created_at" DESC, "name" ASC, "id" ASC` {
		t.Fatalf("explicit order=%s", order)
	}
	if order := BuildOrder(store, recordmodel.RecordListQuery{Sort: []recordmodel.RecordSortRule{{Field: "created_at", Direction: "desc"}, {Field: "id", Direction: "desc"}}}); order != ` ORDER BY "created_at" DESC, "id" DESC` {
		t.Fatalf("explicit stable order=%s", order)
	}
	for _, test := range []struct {
		key, base, operator string
		ok                  bool
	}{
		{key: "age__gte", base: "age", operator: "gte", ok: true},
		{key: "age__lte", base: "age", operator: "lte", ok: true},
		{key: "id__in", base: "id", operator: "in", ok: true},
		{key: "plain", base: "plain"},
	} {
		base, operator, ok := splitFilterKey(test.key)
		if base != test.base || operator != test.operator || ok != test.ok {
			t.Fatalf("split %q = %q %q %v", test.key, base, operator, ok)
		}
	}
	if got := escapeLikePattern(`a~b%c_d`); got != `a~~b~%c~_d` {
		t.Fatalf("escaped pattern=%q", got)
	}
	if dbValue(float32(1)) != float64(1) || dbValue(2) != float64(2) || dbValue(int64(3)) != float64(3) || dbValue("4") != "4" {
		t.Fatal("database value normalization mismatch")
	}
}

func TestQueryBuilderRemainingScopeAndExpressionConditions(t *testing.T) {
	store := fuzzQueryStore{}
	if _, _, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{}); err == nil {
		t.Fatal("missing authorization mode accepted")
	}
	if where, _, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{AuthorizationMode: recordmodel.RecordQueryAuthorizationDeny}); err != nil || !strings.Contains(where, "1 = 0") {
		t.Fatalf("deny-all where=%s err=%v", where, err)
	}
	if _, _, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{AuthorizationMode: recordmodel.RecordQueryAuthorizationPredicate, RootObjectKey: "object"}); err == nil {
		t.Fatal("custom scope without expression accepted")
	}
	expression := &recordmodel.RecordScopeExpression{Operator: "eq", FieldKey: "id", Values: []string{"one"}}
	if _, _, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{AuthorizationMode: recordmodel.RecordQueryAuthorizationPredicate, ScopeExpression: expression}); err == nil {
		t.Fatal("custom scope without root object accepted")
	}

	deepRecord := recordmodel.RecordFilterExpression{Operator: "eq", Field: "id", Value: "one"}
	deepScope := recordmodel.RecordScopeExpression{Operator: "eq", FieldKey: "id", Values: []string{"one"}}
	for range 18 {
		deepRecord = recordmodel.RecordFilterExpression{Operator: "not", Children: []recordmodel.RecordFilterExpression{deepRecord}}
		deepScope = recordmodel.RecordScopeExpression{Operator: "not", Children: []recordmodel.RecordScopeExpression{deepScope}}
	}
	recordArgs := []any{}
	for _, candidate := range []recordmodel.RecordFilterExpression{
		deepRecord,
		{Operator: "and", Children: []recordmodel.RecordFilterExpression{{Operator: "unsupported"}, {Operator: "is_null", Field: "id"}}},
		{Operator: "not"},
		{Operator: "not", Children: []recordmodel.RecordFilterExpression{{Operator: "unsupported"}}},
		{Operator: "in", Field: "id"},
	} {
		if _, err := buildRecordFilterExpression(store, candidate, &recordArgs, 0); err == nil {
			t.Fatalf("invalid record expression accepted: %#v", candidate)
		}
	}
	recordArgs = nil
	if clause, err := buildRecordFilterExpression(store, recordmodel.RecordFilterExpression{Operator: "not_in", Field: "id", Values: []any{"one"}}, &recordArgs, 0); err != nil || !strings.Contains(clause, "NOT IN") {
		t.Fatalf("not-in clause=%s err=%v", clause, err)
	}

	scopeArgs := []any{}
	for _, candidate := range []recordmodel.RecordScopeExpression{
		deepScope,
		{Operator: "and", Children: []recordmodel.RecordScopeExpression{{Operator: "eq", FieldKey: "id", Values: []string{"one"}}}},
		{Operator: "and", Children: []recordmodel.RecordScopeExpression{{Operator: "unsupported"}, {Operator: "eq", FieldKey: "id", Values: []string{"one"}}}},
		{Operator: "not"},
		{Operator: "not", Children: []recordmodel.RecordScopeExpression{{Operator: "unsupported"}}},
	} {
		if _, err := buildScopeExpression(store, "object", candidate, &scopeArgs, 0); err == nil {
			t.Fatalf("invalid scope expression accepted: %#v", candidate)
		}
	}
	scopeArgs = nil
	if clause, err := buildScopeExpression(store, "object", recordmodel.RecordScopeExpression{Operator: "not", Children: []recordmodel.RecordScopeExpression{{Operator: "eq", FieldKey: "id", Values: []string{"one"}}}}, &scopeArgs, 0); err != nil || !strings.Contains(clause, "NOT (") {
		t.Fatalf("valid scope not clause=%s err=%v", clause, err)
	}
}

func TestTenantWhereCompilesCanonicalFilterAST(t *testing.T) {
	store := fuzzQueryStore{}
	filter := &recordmodel.RecordFilterExpression{Operator: "and", Children: []recordmodel.RecordFilterExpression{
		{Operator: "eq", Field: "status", Value: "ready"},
		{Operator: "or", Children: []recordmodel.RecordFilterExpression{{Operator: "gte", Field: "priority", Value: 10}, {Operator: "not", Children: []recordmodel.RecordFilterExpression{{Operator: "in", Field: "owner_id", Values: []any{"a", "b"}}}}}},
		{Operator: "is_not_null", Field: "started_at"},
	}}
	where, args, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, FilterExpression: filter})
	if err != nil || !strings.Contains(where, `("status" = $2 AND ("priority" >= $3 OR NOT ("owner_id" IN ($4, $5))) AND "started_at" IS NOT NULL)`) || !reflect.DeepEqual(args, []any{"workspace-a", "ready", float64(10), "a", "b"}) {
		t.Fatalf("where=%s args=%#v err=%v", where, args, err)
	}
	invalid := &recordmodel.RecordFilterExpression{Operator: "and"}
	if _, _, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, FilterExpression: invalid}); err == nil {
		t.Fatal("invalid canonical filter accepted")
	}
}
