package service

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

type contextualFieldRepositoryProbe struct {
	recordrepository.RecordRepository
	queries int
}

func (r *contextualFieldRepositoryProbe) ListRecords(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	r.queries++
	if query.ScopeExpression == nil || query.ScopeExpression.FieldKey != "owner_id" || len(query.ScopeExpression.Path) != 1 {
		return recordmodel.RecordPageResult{}, nil
	}
	return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "member-1"}}, Total: 1}, nil
}

func TestContextualFieldPolicyBatchesOnePredicateForPageFieldsAndRecords(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "member", Fields: []definitionmodel.FieldSchema{
		{Key: "account_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "account"}},
		{Key: "phone", Type: "phone"},
		{Key: "email", Type: "email"},
	}}
	account := definitionmodel.ObjectSchema{Key: "account", Fields: []definitionmodel.FieldSchema{{Key: "owner_id", Type: "relation"}}}
	ownerPredicate := &accessfixture.PredicateFixture{Operator: "eq", Path: []accessfixture.RelationSegmentFixture{{Direction: "forward", RelationFieldKey: "account_id", TargetObjectKey: "account"}}, FieldKey: "owner_id", ValueSource: "actor_claim", ClaimKey: "user_id"}
	allowOwner := accessfixture.FieldRuleFixture{Key: "owner-clear", Priority: 100, Actions: []string{"read"}, Effect: "allow", Predicate: ownerPredicate}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a", UserID: "user-1"}}, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{
		{ObjectKey: "member", FieldKey: "account_id", Read: true},
		{ObjectKey: "member", FieldKey: "phone", Read: true, Masked: true, Policies: []accessfixture.FieldRuleFixture{allowOwner}},
		{ObjectKey: "member", FieldKey: "email", Read: true, Masked: true, Policies: []accessfixture.FieldRuleFixture{allowOwner}},
	}})
	repository := &contextualFieldRepositoryProbe{}
	service := NewRecordContextualFieldPolicyDomainService(RecordContextualFieldPolicyDependencies{Repository: repository, Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{object, account} }})
	records, _, err := service.ApplyReadPage(t.Context(), principal, object, []recordmodel.Record{
		{ID: "member-1", Data: map[string]any{"account_id": "account-1", "phone": "10000000004", "email": "owner@example.com"}},
		{ID: "member-2", Data: map[string]any{"account_id": "account-2", "phone": "10000000005", "email": "other@example.com"}},
	}, "read")
	if err != nil {
		t.Fatal(err)
	}
	if repository.queries != 1 {
		t.Fatalf("same predicate must be resolved once for the page, queries=%d", repository.queries)
	}
	if records[0].Data["phone"] != "10000000004" || records[0].Data["email"] != "owner@example.com" {
		t.Fatalf("owner fields should be clear: %#v", records[0].Data)
	}
	if records[1].Data["phone"] == "10000000005" || records[1].Data["email"] == "other@example.com" {
		t.Fatalf("other record fields should remain masked: %#v", records[1].Data)
	}
}

func TestContextualFieldPolicyValidatesWriteAgainstCandidateRecord(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "profile", Fields: []definitionmodel.FieldSchema{{Key: "owner_id", Type: "relation"}, {Key: "health_note", Type: "text"}}}
	predicate := &accessfixture.PredicateFixture{Operator: "eq", FieldKey: "owner_id", ValueSource: "actor_claim", ClaimKey: "user_id"}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user-1"}}, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{
		{ObjectKey: "profile", FieldKey: "health_note", Write: false, Policies: []accessfixture.FieldRuleFixture{{Key: "owner-write", Priority: 100, Actions: []string{"write"}, Effect: "allow", Predicate: predicate}}},
	}})
	service := NewRecordContextualFieldPolicyDomainService(RecordContextualFieldPolicyDependencies{Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{object} }})
	if err := service.ValidateWrite(t.Context(), principal, object, recordmodel.Record{ID: "new-profile", Data: map[string]any{"owner_id": "user-1"}}, map[string]any{"health_note": "allowed"}); err != nil {
		t.Fatalf("owner candidate write denied: %v", err)
	}
	if err := service.ValidateWrite(t.Context(), principal, object, recordmodel.Record{ID: "other-profile", Data: map[string]any{"owner_id": "user-2"}}, map[string]any{"health_note": "denied"}); err == nil {
		t.Fatal("non-owner candidate write should be denied")
	}
}

func TestRecordApplyMaskStrategyUsesTypedStableShapes(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		strategy identitysdk.MaskStrategy
		want     string
	}{
		{name: "phone", value: "10000000004", strategy: identitysdk.MaskStrategy{Type: identitysdk.MaskTypePhone}, want: "100****0004"},
		{name: "id number", value: "000000000000000000", strategy: identitysdk.MaskStrategy{Type: identitysdk.MaskTypeIDNumber}, want: "000000********0000"},
		{name: "email", value: "alice@example.com", strategy: identitysdk.MaskStrategy{Type: identitysdk.MaskTypeEmail}, want: "a***@example.com"},
		{name: "year only", value: "1990-01-02", strategy: identitysdk.MaskStrategy{Type: identitysdk.MaskTypeYearOnly}, want: "1990"},
		{name: "last n", value: "ABCDEFGH", strategy: identitysdk.MaskStrategy{Type: identitysdk.MaskTypeLastN, LastN: 3}, want: "*****FGH"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := RecordApplyMaskStrategy(definitionmodel.FieldSchema{}, test.value, &test.strategy); got != test.want {
				t.Fatalf("mask=%q want=%q", got, test.want)
			}
		})
	}
}
