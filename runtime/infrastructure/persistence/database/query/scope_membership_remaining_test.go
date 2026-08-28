package query

import (
	"errors"
	"strings"
	"testing"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestCandidateScopeMatchesRemainingBooleanBranches(t *testing.T) {
	store := fuzzQueryStore{}
	candidate := recordmodel.Record{ID: "record-1", Data: map[string]any{"status": "active"}}
	direct := func(field string, values ...string) recordmodel.RecordScopeExpression {
		return recordmodel.RecordScopeExpression{Operator: "in", FieldKey: field, Values: values}
	}
	unsupported := recordmodel.RecordScopeExpression{Operator: "contains"}
	lookup := func(string, ...any) (bool, error) { return false, nil }

	for _, expression := range []recordmodel.RecordScopeExpression{
		{Operator: "and"},
		{Operator: "and", Children: []recordmodel.RecordScopeExpression{direct("id", "record-1")}},
		{Operator: "or"},
		{Operator: "or", Children: []recordmodel.RecordScopeExpression{direct("id", "record-1")}},
		{Operator: "not"},
		{Operator: "not", Children: []recordmodel.RecordScopeExpression{direct("id", "record-1"), direct("id", "record-2")}},
	} {
		if _, err := CandidateScopeMatches(store, "workspace-a", candidate, expression, lookup); err == nil {
			t.Fatalf("expected invalid boolean shape for %#v", expression)
		}
	}

	andError := recordmodel.RecordScopeExpression{
		Operator: "and",
		Children: []recordmodel.RecordScopeExpression{
			unsupported,
			direct("id", "record-1"),
		},
	}
	if _, err := CandidateScopeMatches(store, "workspace-a", candidate, andError, lookup); err == nil {
		t.Fatal("expected nested and child error")
	}

	orError := recordmodel.RecordScopeExpression{
		Operator: "or",
		Children: []recordmodel.RecordScopeExpression{
			unsupported,
			direct("id", "record-1"),
		},
	}
	if _, err := CandidateScopeMatches(store, "workspace-a", candidate, orError, lookup); err == nil {
		t.Fatal("expected nested or child error")
	}
	orMatch := recordmodel.RecordScopeExpression{
		Operator: "or",
		Children: []recordmodel.RecordScopeExpression{
			direct("id", "missing"),
			direct("id", "record-1"),
		},
	}
	if matched, err := CandidateScopeMatches(store, "workspace-a", candidate, orMatch, lookup); err != nil || !matched {
		t.Fatalf("or match=%v err=%v", matched, err)
	}
	orMiss := recordmodel.RecordScopeExpression{
		Operator: "or",
		Children: []recordmodel.RecordScopeExpression{
			direct("id", "missing-1"),
			direct("id", "missing-2"),
		},
	}
	if matched, err := CandidateScopeMatches(store, "workspace-a", candidate, orMiss, lookup); err != nil || matched {
		t.Fatalf("or miss=%v err=%v", matched, err)
	}
	notMatch := recordmodel.RecordScopeExpression{
		Operator: "not",
		Children: []recordmodel.RecordScopeExpression{
			direct("id", "record-1"),
		},
	}
	if matched, err := CandidateScopeMatches(store, "workspace-a", candidate, notMatch, lookup); err != nil || matched {
		t.Fatalf("not match=%v err=%v", matched, err)
	}
	if matched, err := CandidateScopeMatches(store, "workspace-a", candidate, direct("id", "record-1"), lookup); err != nil || !matched {
		t.Fatalf("direct id match=%v err=%v", matched, err)
	}
	if _, err := CandidateScopeMatches(store, "workspace-a", candidate, unsupported, lookup); err == nil {
		t.Fatal("expected unsupported candidate scope operator")
	}
}

func TestCandidateRelationScopeMatchesRemainingFailureBranches(t *testing.T) {
	store := fuzzQueryStore{}
	candidate := recordmodel.Record{ID: "record-1", Data: map[string]any{}}
	leaf := func(path []recordmodel.RecordScopePathSegment) recordmodel.RecordScopeExpression {
		return recordmodel.RecordScopeExpression{
			Operator: "eq",
			Path:     path,
			FieldKey: "permission",
			Values:   []string{"allowed"},
		}
	}
	forward := recordmodel.RecordScopePathSegment{
		Direction:        "forward",
		RelationFieldKey: "account_id",
		TargetObjectKey:  "account",
	}

	empty := leaf([]recordmodel.RecordScopePathSegment{forward})
	empty.Values = nil
	called := false
	if matched, err := CandidateScopeMatches(store, "workspace-a", candidate, empty, func(string, ...any) (bool, error) {
		called = true
		return true, nil
	}); err != nil || matched || called {
		t.Fatalf("empty values matched=%v called=%v err=%v", matched, called, err)
	}

	invalid := leaf([]recordmodel.RecordScopePathSegment{{
		Direction:       "sideways",
		TargetObjectKey: "account",
	}})
	if _, err := CandidateScopeMatches(store, "workspace-a", candidate, invalid, nil); err == nil || !strings.Contains(err.Error(), "unsupported compiled relation direction") {
		t.Fatalf("invalid direction err=%v", err)
	}

	for name, relationID := range map[string]any{"missing": nil, "nil": nil, "blank": "  "} {
		if name == "missing" {
			delete(candidate.Data, "account_id")
		} else {
			candidate.Data["account_id"] = relationID
		}
		called = false
		if matched, err := CandidateScopeMatches(store, "workspace-a", candidate, leaf([]recordmodel.RecordScopePathSegment{forward}), func(string, ...any) (bool, error) {
			called = true
			return true, nil
		}); err != nil || matched || called {
			t.Fatalf("%s forward anchor matched=%v called=%v err=%v", name, matched, called, err)
		}
	}

	invalidInner := leaf([]recordmodel.RecordScopePathSegment{
		forward,
		{
			Direction:        "sideways",
			RelationFieldKey: "member_id",
			TargetObjectKey:  "member",
		},
	})
	candidate.Data["account_id"] = "account-1"
	if _, err := CandidateScopeMatches(store, "workspace-a", candidate, invalidInner, nil); err == nil {
		t.Fatal("expected invalid inner relation direction")
	}

	wantLookupErr := errors.New("candidate lookup unavailable")
	if _, err := CandidateScopeMatches(store, "workspace-a", candidate, leaf([]recordmodel.RecordScopePathSegment{forward}), func(string, ...any) (bool, error) {
		return false, wantLookupErr
	}); !errors.Is(err, wantLookupErr) {
		t.Fatalf("lookup error=%v", err)
	}
}
