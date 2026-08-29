package query

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestResolveScopeMembershipQueriesPermissionIDsBeforeRootFieldIN(t *testing.T) {
	store := fuzzQueryStore{}
	expression := &recordmodel.RecordScopeExpression{Operator: "and", Children: []recordmodel.RecordScopeExpression{
		{Operator: "in", FieldKey: "status", Values: []string{"posted", "settled"}},
		{Operator: "eq", Path: []recordmodel.RecordScopePathSegment{
			{SourceObjectKey: "ledger", Direction: "forward", RelationFieldKey: "account_id", TargetObjectKey: "account"},
			{SourceObjectKey: "account", Direction: "forward", RelationFieldKey: "card_id", TargetObjectKey: "card"},
			{SourceObjectKey: "card", Direction: "forward", RelationFieldKey: "member_id", TargetObjectKey: "member"},
		}, FieldKey: "id", Values: []string{"member-1"}},
	}}
	var lookupSQL string
	var lookupArgs []any
	resolved, err := ResolveScopeMembership(store, "workspace-a", *expression, 100, func(statement string, args ...any) ([]string, error) {
		lookupSQL = statement
		lookupArgs = append([]any(nil), args...)
		return []string{"account-1", "account-2", "account-1"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`SELECT DISTINCT "permission_0"."id" FROM "member" AS "permission_2"`, `CROSS JOIN "card" AS "permission_1"`, `CROSS JOIN "account" AS "permission_0"`, `"permission_2"."id" = $2`, `LIMIT $3`} {
		if !strings.Contains(lookupSQL, fragment) {
			t.Fatalf("permission lookup missing %q in %s", fragment, lookupSQL)
		}
	}
	if !reflect.DeepEqual(lookupArgs, []any{"workspace-a", "member-1", 101}) {
		t.Fatalf("lookup args=%#v", lookupArgs)
	}
	where, args, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{Scope: "custom", RootObjectKey: "ledger", ScopeExpression: &resolved})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"workspace_id" = $1`, `"ledger"."status" IN ($2, $3)`, `"ledger"."account_id" IN ($4, $5)`} {
		if !strings.Contains(where, fragment) {
			t.Fatalf("record query missing %q in %s", fragment, where)
		}
	}
	if strings.Contains(where, "EXISTS") || strings.Contains(where, "JOIN") {
		t.Fatalf("record query must consume permission IDs only: %s", where)
	}
	if !reflect.DeepEqual(args, []any{"workspace-a", "posted", "settled", "account-1", "account-2"}) {
		t.Fatalf("args=%#v", args)
	}
}

func TestBuildTenantWhereCompilesReverseExistenceAndDenyAllClaim(t *testing.T) {
	store := fuzzQueryStore{}
	reverse := &recordmodel.RecordScopeExpression{Operator: "eq", Path: []recordmodel.RecordScopePathSegment{{SourceObjectKey: "member", Direction: "reverse", RelationFieldKey: "member_id", TargetObjectKey: "package"}}, FieldKey: "coach_id", Values: []string{"coach-1"}}
	resolved, err := ResolveScopeMembership(store, "workspace-a", *reverse, 100, func(statement string, args ...any) ([]string, error) {
		if !strings.Contains(statement, `SELECT DISTINCT "permission_0"."member_id" FROM "package" AS "permission_0"`) || !strings.Contains(statement, `"permission_0"."coach_id" = $2`) {
			t.Fatalf("reverse permission lookup=%s", statement)
		}
		return []string{"member-1"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	where, _, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{Scope: "custom", RootObjectKey: "member", ScopeExpression: &resolved})
	if err != nil || !strings.Contains(where, `"member"."id" IN ($2)`) || strings.Contains(where, "package") {
		t.Fatalf("reverse root where=%s err=%v", where, err)
	}
	deny := &recordmodel.RecordScopeExpression{Operator: "in", FieldKey: "id"}
	where, args, err := BuildTenantWhere(store, "workspace-a", recordmodel.RecordListQuery{Scope: "custom", RootObjectKey: "member", ScopeExpression: deny})
	if err != nil || !strings.Contains(where, "1 = 0") || !reflect.DeepEqual(args, []any{"workspace-a"}) {
		t.Fatalf("deny where=%s args=%#v err=%v", where, args, err)
	}
}

func TestBuildTenantWhereRejectsUnresolvedRelationPath(t *testing.T) {
	expression := &recordmodel.RecordScopeExpression{Operator: "eq", Path: []recordmodel.RecordScopePathSegment{{Direction: "forward", RelationFieldKey: "account_id", TargetObjectKey: "account"}}, FieldKey: "id", Values: []string{"account-1"}}
	if _, _, err := BuildTenantWhere(fuzzQueryStore{}, "workspace-a", recordmodel.RecordListQuery{Scope: "custom", RootObjectKey: "ledger", ScopeExpression: expression}); err == nil || !strings.Contains(err.Error(), "resolved to permission IDs") {
		t.Fatalf("unresolved relation path err=%v", err)
	}
}

func TestCandidateScopeMatchesConstrainsRelationLookupToUnpersistedCandidate(t *testing.T) {
	expression := recordmodel.RecordScopeExpression{Operator: "and", Children: []recordmodel.RecordScopeExpression{
		{Operator: "eq", FieldKey: "status", Values: []string{"active"}},
		{Operator: "eq", Path: []recordmodel.RecordScopePathSegment{{SourceObjectKey: "reservation", Direction: "forward", RelationFieldKey: "order_id", TargetObjectKey: "order"}}, FieldKey: "warehouse_id", Values: []string{"warehouse-north"}},
	}}
	candidate := recordmodel.Record{ID: "reservation-new", Data: map[string]any{"status": "active", "order_id": "order-north"}}
	var statement string
	var args []any
	matched, err := CandidateScopeMatches(fuzzQueryStore{}, "workspace-a", candidate, expression, func(query string, queryArgs ...any) (bool, error) {
		statement = query
		args = append([]any(nil), queryArgs...)
		return true, nil
	})
	if err != nil || !matched {
		t.Fatalf("candidate match=%v err=%v", matched, err)
	}
	for _, fragment := range []string{`FROM "order" AS "permission_0"`, `"permission_0"."warehouse_id" = $2`, `"permission_0"."id" = $3`, `LIMIT $4`} {
		if !strings.Contains(statement, fragment) {
			t.Fatalf("candidate relation lookup missing %q in %s", fragment, statement)
		}
	}
	if !reflect.DeepEqual(args, []any{"workspace-a", "warehouse-north", "order-north", 1}) {
		t.Fatalf("candidate lookup args=%#v", args)
	}

	candidate.Data["status"] = "inactive"
	called := false
	matched, err = CandidateScopeMatches(fuzzQueryStore{}, "workspace-a", candidate, expression, func(string, ...any) (bool, error) {
		called = true
		return true, nil
	})
	if err != nil || matched || called {
		t.Fatalf("direct deny must short-circuit relation lookup matched=%v called=%v err=%v", matched, called, err)
	}
}

func TestCandidateScopeMatchesUsesCandidateIDForReverseRelation(t *testing.T) {
	expression := recordmodel.RecordScopeExpression{Operator: "eq", Path: []recordmodel.RecordScopePathSegment{{SourceObjectKey: "order", Direction: "reverse", RelationFieldKey: "order_id", TargetObjectKey: "grant"}}, FieldKey: "warehouse_id", Values: []string{"warehouse-north"}}
	matched, err := CandidateScopeMatches(fuzzQueryStore{}, "workspace-a", recordmodel.Record{ID: "order-new"}, expression, func(statement string, args ...any) (bool, error) {
		if !strings.Contains(statement, `"permission_0"."order_id" = $3`) || args[2] != "order-new" || args[3] != 1 {
			t.Fatalf("reverse candidate lookup=%s args=%#v", statement, args)
		}
		return false, nil
	})
	if err != nil || matched {
		t.Fatalf("reverse candidate match=%v err=%v", matched, err)
	}
}

func TestResolveScopeMembershipFallsBackToSingleExistsAboveThreshold(t *testing.T) {
	expression := recordmodel.RecordScopeExpression{Operator: "eq", Path: []recordmodel.RecordScopePathSegment{
		{SourceObjectKey: "ledger", Direction: "forward", RelationFieldKey: "account_id", TargetObjectKey: "account"},
		{SourceObjectKey: "account", Direction: "forward", RelationFieldKey: "card_id", TargetObjectKey: "card"},
		{SourceObjectKey: "card", Direction: "forward", RelationFieldKey: "member_id", TargetObjectKey: "member"},
	}, FieldKey: "id", Values: []string{"member-1"}}
	resolved, err := ResolveScopeMembership(fuzzQueryStore{}, "workspace-a", expression, 2, func(statement string, args ...any) ([]string, error) {
		if !strings.HasSuffix(statement, "LIMIT $3") || args[2] != 3 {
			t.Fatalf("permission probe must stop at threshold+1: %s", statement)
		}
		return []string{"account-1", "account-2", "account-3"}, nil
	})
	if err != nil || !resolved.RelationExists || len(resolved.Path) != 3 {
		t.Fatalf("overflow resolution=%#v err=%v", resolved, err)
	}
	where, args, err := BuildTenantWhere(fuzzQueryStore{}, "workspace-a", recordmodel.RecordListQuery{Scope: "custom", RootObjectKey: "ledger", ScopeExpression: &resolved})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`EXISTS (SELECT * FROM "member" AS "permission_2"`, `CROSS JOIN "card" AS "permission_1"`, `CROSS JOIN "account" AS "permission_0"`, `"permission_0"."id" = "ledger"."account_id"`} {
		if !strings.Contains(where, fragment) {
			t.Fatalf("overflow EXISTS missing %q in %s", fragment, where)
		}
	}
	if strings.Count(where, "EXISTS (") != 1 || !reflect.DeepEqual(args, []any{"workspace-a", "member-1"}) {
		t.Fatalf("overflow where=%s args=%#v", where, args)
	}
}

func TestResolveScopeMembershipUsesINAtOneThousand(t *testing.T) {
	expression := recordmodel.RecordScopeExpression{Operator: "eq", Path: []recordmodel.RecordScopePathSegment{{SourceObjectKey: "member", Direction: "reverse", RelationFieldKey: "member_id", TargetObjectKey: "package"}}, FieldKey: "coach_id", Values: []string{"coach-1"}}
	resolved, err := ResolveScopeMembership(fuzzQueryStore{}, "workspace-a", expression, 1000, func(statement string, args ...any) ([]string, error) {
		if !strings.HasSuffix(statement, "LIMIT $3") || args[2] != 1001 {
			t.Fatalf("fixed threshold probe=%s", statement)
		}
		ids := make([]string, 1000)
		for index := range ids {
			ids[index] = fmt.Sprintf("member-%04d", index)
		}
		return ids, nil
	})
	if err != nil || resolved.RelationExists || len(resolved.Values) != 1000 {
		t.Fatalf("threshold boundary resolution values=%d exists=%v err=%v", len(resolved.Values), resolved.RelationExists, err)
	}
	where, args, err := BuildTenantWhere(fuzzQueryStore{}, "workspace-a", recordmodel.RecordListQuery{Scope: "custom", RootObjectKey: "member", ScopeExpression: &resolved})
	if err != nil || strings.Contains(where, "EXISTS") || strings.Count(where, `"member"."id" IN (`) != 1 || len(args) != 1001 {
		t.Fatalf("threshold boundary where=%s args=%d err=%v", where, len(args), err)
	}
}

func TestBuildTenantWhereChunksLargePermissionIDSets(t *testing.T) {
	values := make([]string, 1001)
	for index := range values {
		values[index] = "account"
	}
	expression := &recordmodel.RecordScopeExpression{Operator: "in", FieldKey: "account_id", Values: values}
	where, args, err := BuildTenantWhere(fuzzQueryStore{}, "workspace-a", recordmodel.RecordListQuery{Scope: "custom", RootObjectKey: "ledger", ScopeExpression: expression})
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(where, `"ledger"."account_id" IN (`); count != 2 {
		t.Fatalf("expected two 1000-ID-bounded IN groups, got %d", count)
	}
	if len(args) != 1002 {
		t.Fatalf("args=%d", len(args))
	}
}

func TestScopeMembershipRejectsInvalidInputsAndCoversEveryRelationDirection(t *testing.T) {
	store := fuzzQueryStore{}
	lookup := func(string, ...any) ([]string, error) { return []string{"root-1"}, nil }
	leaf := func(path []recordmodel.RecordScopePathSegment) recordmodel.RecordScopeExpression {
		return recordmodel.RecordScopeExpression{Operator: "eq", Path: path, FieldKey: "id", Values: []string{"allowed-1"}}
	}

	if _, err := ResolveScopeMembership(store, "workspace-a", leaf(nil), 0, lookup); err == nil || !strings.Contains(err.Error(), "threshold must be positive") {
		t.Fatalf("invalid threshold err=%v", err)
	}
	unsupported := recordmodel.RecordScopeExpression{Operator: "contains"}
	if _, err := ResolveScopeMembership(store, "workspace-a", unsupported, 1, lookup); err == nil || !strings.Contains(err.Error(), "unsupported compiled scope operator") {
		t.Fatalf("unsupported operator err=%v", err)
	}
	if _, err := ResolveScopeMembership(store, "workspace-a", recordmodel.RecordScopeExpression{Operator: "and", Children: []recordmodel.RecordScopeExpression{unsupported}}, 1, lookup); err == nil {
		t.Fatal("nested unsupported operator was accepted")
	}
	invalidFirst := leaf([]recordmodel.RecordScopePathSegment{{Direction: "sideways", TargetObjectKey: "permission"}})
	if _, err := ResolveScopeMembership(store, "workspace-a", invalidFirst, 1, lookup); err == nil || !strings.Contains(err.Error(), "unsupported compiled relation direction") {
		t.Fatalf("invalid first lookup direction err=%v", err)
	}
	wantLookupErr := errors.New("lookup unavailable")
	valid := leaf([]recordmodel.RecordScopePathSegment{{Direction: "forward", RelationFieldKey: "permission_id", TargetObjectKey: "permission"}})
	emptyValues := valid
	emptyValues.Values = nil
	resolvedEmpty, err := ResolveScopeMembership(store, "workspace-a", emptyValues, 1, lookup)
	if err != nil || resolvedEmpty.Operator != "in" || resolvedEmpty.FieldKey != "permission_id" || len(resolvedEmpty.Values) != 0 {
		t.Fatalf("empty membership values resolution=%#v err=%v", resolvedEmpty, err)
	}
	if _, err := ResolveScopeMembership(store, "workspace-a", valid, 1, func(string, ...any) ([]string, error) { return nil, wantLookupErr }); !errors.Is(err, wantLookupErr) {
		t.Fatalf("lookup error=%v", err)
	}
	withoutRelation := recordmodel.RecordScopeExpression{Operator: "eq", FieldKey: "id"}
	if !ScopeExpressionHasRelation(valid) || !ScopeExpressionHasRelation(recordmodel.RecordScopeExpression{Operator: "or", Children: []recordmodel.RecordScopeExpression{withoutRelation, valid}}) || ScopeExpressionHasRelation(withoutRelation) {
		t.Fatal("relation-path detection did not cover direct, nested, and absent paths")
	}

	reverseInner := leaf([]recordmodel.RecordScopePathSegment{
		{Direction: "forward", RelationFieldKey: "account_id", TargetObjectKey: "account"},
		{Direction: "reverse", RelationFieldKey: "account_id", TargetObjectKey: "grant"},
	})
	if statement, _, err := buildScopeMembershipLookup(store, "workspace-a", reverseInner, 2); err != nil || !strings.Contains(statement, `"permission_0"."id" = "permission_1"."account_id"`) {
		t.Fatalf("reverse inner lookup statement=%s err=%v", statement, err)
	}
	invalidInner := reverseInner
	invalidInner.Path = append([]recordmodel.RecordScopePathSegment(nil), reverseInner.Path...)
	invalidInner.Path[1].Direction = "sideways"
	if _, _, err := buildScopeMembershipLookup(store, "workspace-a", invalidInner, 2); err == nil {
		t.Fatal("invalid inner lookup direction was accepted")
	}

	if got := uniqueScopeIDs([]string{"", "  ", "one", "one"}); !reflect.DeepEqual(got, []string{"one"}) {
		t.Fatalf("unique scope IDs=%v", got)
	}
	args := []any{}
	if _, err := buildScopeRelationExists(store, `root`, recordmodel.RecordScopeExpression{}, &args); err == nil || !strings.Contains(err.Error(), "requires a relation path") {
		t.Fatalf("empty EXISTS path err=%v", err)
	}
	reverseFirst := leaf([]recordmodel.RecordScopePathSegment{{Direction: "reverse", RelationFieldKey: "root_id", TargetObjectKey: "grant"}})
	if statement, err := buildScopeRelationExists(store, `root`, reverseFirst, &args); err != nil || !strings.Contains(statement, `"permission_0"."root_id" = "root"."id"`) {
		t.Fatalf("reverse root EXISTS statement=%s err=%v", statement, err)
	}
	if _, err := buildScopeRelationExists(store, `root`, invalidFirst, &args); err == nil {
		t.Fatal("invalid first EXISTS direction was accepted")
	}
	args = nil
	if statement, err := buildScopeRelationExists(store, `root`, reverseInner, &args); err != nil || !strings.Contains(statement, `"permission_0"."id" = "permission_1"."account_id"`) {
		t.Fatalf("reverse inner EXISTS statement=%s err=%v", statement, err)
	}
	if _, err := buildScopeRelationExists(store, `root`, invalidInner, &args); err == nil {
		t.Fatal("invalid inner EXISTS direction was accepted")
	}
}
