package query

import (
	"reflect"
	"strings"
	"testing"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestTenantWhereScopeAndFilterContract(t *testing.T) {
	store := fuzzQueryStore{}
	for _, scope := range []string{"owned_records"} {
		where, args, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{Scope: scope, OwnerField: "owner_id", PrincipalUserID: "user-a"})
		if err != nil || !strings.Contains(where, `"owner_id" = $2`) || !reflect.DeepEqual(args, []any{"workspace-a", "user-a"}) {
			t.Fatalf("owned scope %q where=%s args=%#v err=%v", scope, where, args, err)
		}
	}
	if where, args, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{Scope: "owned_records", OwnerField: " "}); err != nil || strings.Contains(where, "owner_id") || len(args) != 1 {
		t.Fatalf("blank owner where=%s args=%#v err=%v", where, args, err)
	}
	for _, query := range []recordmodel.RecordListQuery{
		{Scope: "subordinates", OwnerField: ""},
		{Scope: "subordinates", OwnerField: "owner_id"},
	} {
		where, args, err := BuildTenantWhere(store, "workspace-a", query)
		if err != nil || len(args) != 1 || (strings.TrimSpace(query.OwnerField) != "" && !strings.Contains(where, "1 = 0")) {
			t.Fatalf("incomplete subordinate where=%s args=%#v err=%v", where, args, err)
		}
	}
	where, args, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{Scope: "subordinates", OwnerField: "owner_id", PrincipalReportingUserIDs: []string{"user-b", " ", "user-c"}})
	if err != nil || !strings.Contains(where, `"owner_id" IN ($2, $3)`) || !reflect.DeepEqual(args, []any{"workspace-a", "user-b", "user-c"}) {
		t.Fatalf("workforce subordinate where=%s args=%#v err=%v", where, args, err)
	}

	for _, test := range []struct {
		scope string
		query recordmodel.RecordListQuery
		term  string
	}{
		{scope: "department", query: recordmodel.RecordListQuery{DepartmentPathField: "department_path", PrincipalDepartmentPath: "/sales"}, term: `"department_path" = $2`},
		{scope: "department_and_children", query: recordmodel.RecordListQuery{DepartmentPathField: "department_path", PrincipalDepartmentPath: "/sales/"}, term: `LIKE $3`},
	} {
		test.query.Scope = test.scope
		where, _, err := BuildTenantWhere(store, "workspace-a", test.query)
		if err != nil || !strings.Contains(where, test.term) {
			t.Fatalf("scope %q where=%s err=%v", test.scope, where, err)
		}
		test.query.DepartmentPathField = ""
		if _, args, err := BuildTenantWhere(store, "workspace-a", test.query); err != nil || len(args) != 1 {
			t.Fatalf("scope %q blank field args=%#v err=%v", test.scope, args, err)
		}
		test.query.DepartmentPathField, test.query.PrincipalDepartmentPath = "department_path", ""
		if _, args, err := BuildTenantWhere(store, "workspace-a", test.query); err != nil || len(args) != 1 {
			t.Fatalf("scope %q blank path args=%#v err=%v", test.scope, args, err)
		}
	}

	teamQuery := recordmodel.RecordListQuery{Scope: "team", TeamField: "team_id", PrincipalTeamIDs: []string{"one", " ", "two"}}
	where, args, err = BuildTenantWhere(store, "workspace-a", teamQuery)
	if err != nil || !strings.Contains(where, `"team_id" IN`) || len(args) != 3 {
		t.Fatalf("team scope where=%s args=%#v err=%v", where, args, err)
	}
	for _, invalid := range []string{"team_records", "store", "territory", "warehouse", "filtered_records"} {
		if _, _, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{Scope: invalid, TeamField: "team_id", PrincipalTeamIDs: []string{"one"}}); err == nil {
			t.Fatalf("legacy scope %q accepted", invalid)
		}
	}

	query := recordmodel.RecordListQuery{
		Search: " Needle ", SearchFields: []string{"name", "code"},
		Filters: map[string]any{"workspace_id": "other", "empty": "", "plain": 2, "minimum__gte": int64(3), "maximum__lte": float32(4), "ids__in": []any{"a", "", 5}},
	}
	where, args, err = BuildTenantWhere(store, "workspace-a", query)
	if err != nil || !strings.Contains(where, "LOWER") || !strings.Contains(where, `"minimum" >=`) || !strings.Contains(where, `"maximum" <=`) || !strings.Contains(where, `"ids" IN`) || len(args) != 8 {
		t.Fatalf("search/filter where=%s args=%#v err=%v", where, args, err)
	}
	if _, args, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{Search: "needle"}); err != nil || len(args) != 1 {
		t.Fatalf("search without fields args=%#v err=%v", args, err)
	}
}

func TestTenantWhereCompilesOwnedDepartmentUnionWithoutAllRecordsFallback(t *testing.T) {
	expression := recordmodel.RecordScopeExpression{Operator: "or", Children: []recordmodel.RecordScopeExpression{
		{Operator: "eq", FieldKey: "owner", Values: []string{"user-1"}},
		{Operator: "eq", FieldKey: "owner_department_path", Values: []string{"/company/sales"}},
	}}
	where, args, err := BuildTenantWhere(fuzzQueryStore{}, "workspace-a", recordmodel.RecordListQuery{
		Scope: "custom", RootObjectKey: "case", ScopeExpression: &expression,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(where, `("case"."owner" = $2 OR "case"."owner_department_path" = $3)`) ||
		!reflect.DeepEqual(args, []any{"workspace-a", "user-1", "/company/sales"}) {
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
	if where, _, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{Scope: "none"}); err != nil || !strings.Contains(where, "1 = 0") {
		t.Fatalf("deny-all where=%s err=%v", where, err)
	}
	for _, query := range []recordmodel.RecordListQuery{
		{Scope: "team", TeamField: "", PrincipalTeamIDs: []string{"team-1"}},
		{Scope: "team", TeamField: "team_id"},
	} {
		if _, args, err := BuildTenantWhere(store, "workspace-a", query); err != nil || len(args) != 1 {
			t.Fatalf("incomplete team query=%#v args=%#v err=%v", query, args, err)
		}
	}
	if _, _, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{Scope: "custom", RootObjectKey: "object"}); err == nil {
		t.Fatal("custom scope without expression accepted")
	}
	expression := &recordmodel.RecordScopeExpression{Operator: "eq", FieldKey: "id", Values: []string{"one"}}
	if _, _, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{Scope: "custom", ScopeExpression: expression}); err == nil {
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
	where, args, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{FilterExpression: filter})
	if err != nil || !strings.Contains(where, `("status" = $2 AND ("priority" >= $3 OR NOT ("owner_id" IN ($4, $5))) AND "started_at" IS NOT NULL)`) || !reflect.DeepEqual(args, []any{"workspace-a", "ready", float64(10), "a", "b"}) {
		t.Fatalf("where=%s args=%#v err=%v", where, args, err)
	}
	invalid := &recordmodel.RecordFilterExpression{Operator: "and"}
	if _, _, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{FilterExpression: invalid}); err == nil {
		t.Fatal("invalid canonical filter accepted")
	}
}
