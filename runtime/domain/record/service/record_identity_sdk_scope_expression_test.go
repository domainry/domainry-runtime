package service

import (
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestSDKDataScopeCompilerTreatsAllAsNoAdditionalPredicate(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "case"}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{
		Known: true, UserID: "user-1", WorkspaceID: "workspace-1",
	}}, accessfixture.Bundle{
		Permissions:  []string{"case.read", "case.update"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "case", Scope: "all", Read: true, Write: true}},
	})

	for _, action := range []string{"read", "update"} {
		expression, err, handled := RecordCompileSDKDataScopeExpression(object, []definitionmodel.ObjectSchema{object}, principal, action)
		if err != nil || !handled || expression != nil {
			t.Fatalf("all action=%s expression=%#v handled=%v err=%v", action, expression, handled, err)
		}
	}
}

func TestSDKDataScopeCompilerUsesAdmittedAuthorizationInstant(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "case"}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{
		Known: true, UserID: "user-1", WorkspaceID: "workspace-1",
	}}, accessfixture.Bundle{Permissions: []string{"case.read"}})
	now := time.Now().UTC()
	principal.AccessBundle.ExpiresAt = now.Add(-time.Minute)
	principal.AuthorizationEvaluatedAt = now.Add(-2 * time.Minute)

	expression, err, handled := RecordCompileSDKDataScopeExpression(object, []definitionmodel.ObjectSchema{object}, principal, "read")
	if err != nil || !handled || expression != nil {
		t.Fatalf("admitted snapshot expression=%#v handled=%v error=%v", expression, handled, err)
	}
	principal.AuthorizationEvaluatedAt = time.Time{}
	if _, err, handled := RecordCompileSDKDataScopeExpression(object, []definitionmodel.ObjectSchema{object}, principal, "read"); err == nil || !handled {
		t.Fatalf("expired unsnapshotted bundle handled=%v error=%v", handled, err)
	}
}

func TestSDKDataScopeCompilerRetainsDenyPredicateUnderAll(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "case"}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{
		Known: true, UserID: "user-1", WorkspaceID: "workspace-1",
	}}, accessfixture.Bundle{
		Permissions:  []string{"case.read"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "case", Scope: "all", Read: true}},
	})
	principal.AccessBundle.DataPolicies = append(principal.AccessBundle.DataPolicies, identitysdk.DataPolicy{
		Key: "case.read.deny-owner", Resource: "case", Action: "read", Effect: identitysdk.EffectDeny,
		DataScopes: []identitysdk.DataScope{identitysdk.DataScopeOwner},
		Predicate:  identitysdk.Predicate{Fact: "owner_user_id", Operator: identitysdk.OperatorEqual, Value: "$subject.id"},
	})

	expression, err, handled := RecordCompileSDKDataScopeExpression(object, []definitionmodel.ObjectSchema{object}, principal, "read")
	if err != nil || !handled || expression == nil || expression.Operator != "not" || len(expression.Children) != 1 {
		t.Fatalf("expression=%#v handled=%v err=%v", expression, handled, err)
	}
	if directSDKScopeExpressionMatches(*expression, recordmodel.Record{OwnerUserID: "user-1"}) {
		t.Fatal("deny predicate was lost under all")
	}
	if !directSDKScopeExpressionMatches(*expression, recordmodel.Record{OwnerUserID: "other"}) {
		t.Fatal("all did not allow a record outside the deny predicate")
	}
}

func TestSDKDataScopeCompilerUnionsIdentityIssuedPolicies(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "case"}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{
		Known: true, UserID: "user-1", OrgID: "sales",
	}}, accessfixture.Bundle{
		Permissions: []string{"case.read"},
		DataPolicies: []accessfixture.DataPolicyFixture{
			{ObjectKey: "case", Scope: "owner", Read: true},
			{ObjectKey: "case", Scope: "org", Read: true},
		},
	})

	expression, err, handled := RecordCompileSDKDataScopeExpression(object, []definitionmodel.ObjectSchema{object}, principal, "read")
	if err != nil || !handled || expression == nil || expression.Operator != "or" || len(expression.Children) != 2 {
		t.Fatalf("expression=%#v handled=%v err=%v", expression, handled, err)
	}
	if !directSDKScopeExpressionMatches(*expression, recordmodel.Record{OwnerUserID: "user-1", OwnerOrgID: "support"}) {
		t.Fatal("owner policy did not match")
	}
	if !directSDKScopeExpressionMatches(*expression, recordmodel.Record{OwnerUserID: "other", OwnerOrgID: "sales"}) {
		t.Fatal("organization policy did not match")
	}
	if directSDKScopeExpressionMatches(*expression, recordmodel.Record{OwnerUserID: "other", OwnerOrgID: "support"}) {
		t.Fatal("record outside every SDK policy matched")
	}
}

func TestSDKDataScopeCompilerUsesIdentityIssuedOrganizationScopes(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "case"}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{
		Known: true, UserID: "manager", OrgID: "region", OrgScopeIDs: []string{"region", "store"}, SupportOrgScopeIDs: []string{"support", "support-child"},
	}}, accessfixture.Bundle{Permissions: []string{"case.read"}, DataPolicies: []accessfixture.DataPolicyFixture{
		{ObjectKey: "case", Scope: "org_child", Read: true},
		{ObjectKey: "case", Scope: "target_org", Read: true},
	}})
	expression, err, handled := RecordCompileSDKDataScopeExpression(object, []definitionmodel.ObjectSchema{object}, principal, "read")
	if err != nil || !handled || expression == nil {
		t.Fatalf("expression=%#v handled=%v err=%v", expression, handled, err)
	}
	if !directSDKScopeExpressionMatches(*expression, recordmodel.Record{OwnerOrgID: "store"}) ||
		!directSDKScopeExpressionMatches(*expression, recordmodel.Record{OwnerOrgID: "support-child"}) ||
		directSDKScopeExpressionMatches(*expression, recordmodel.Record{OwnerOrgID: "outside", OwnerUserID: "outside"}) {
		t.Fatalf("organization scope expression=%#v", expression)
	}
}

func TestSDKDataScopeCompilerTranslatesRelationsAndBusinessClaims(t *testing.T) {
	member := definitionmodel.ObjectSchema{Key: "member", Fields: []definitionmodel.FieldSchema{{Key: "coach_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "coach"}}}}
	booking := definitionmodel.ObjectSchema{Key: "booking", Fields: []definitionmodel.FieldSchema{{Key: "member_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "member"}}}}
	coach := definitionmodel.ObjectSchema{Key: "coach", Fields: []definitionmodel.FieldSchema{{Key: "id", Type: "text"}}}
	predicate := &accessfixture.PredicateFixture{
		Operator: "eq",
		Path: []accessfixture.RelationSegmentFixture{
			{Direction: "forward", RelationFieldKey: "member_id", TargetObjectKey: "member"},
			{Direction: "forward", RelationFieldKey: "coach_id", TargetObjectKey: "coach"},
		},
		FieldKey: "id", ValueSource: "actor_claim", ClaimKey: "coach_id",
	}
	principal := accessfixture.Attach(principalmodel.Principal{
		Principal:      identitysdk.Principal{Known: true, UserID: "user-1"},
		BusinessClaims: map[string]profilebindingmodel.ClaimValue{"coach_id": {Type: "relation", Value: "coach-1"}},
	}, accessfixture.Bundle{
		Permissions:  []string{"booking.read"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "booking", Read: true, Predicate: predicate}},
	})

	expression, err, handled := RecordCompileSDKDataScopeExpression(booking, []definitionmodel.ObjectSchema{booking, member, coach}, principal, "read")
	if err != nil || !handled || expression == nil || len(expression.Path) != 2 || expression.Values[0] != "coach-1" {
		t.Fatalf("expression=%#v handled=%v err=%v", expression, handled, err)
	}
	if !sdkScopeExpressionHasRelation(*expression) {
		t.Fatal("relation path was lost")
	}
}

func TestSDKDataScopeCompilerFailsClosed(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "case"}
	if expression, err, handled := RecordCompileSDKDataScopeExpression(object, []definitionmodel.ObjectSchema{object}, principalmodel.Principal{}, "read"); err != nil || handled || expression != nil {
		t.Fatalf("principal without AccessBundle expression=%#v handled=%v err=%v", expression, handled, err)
	}

	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"case.read"}})
	principal.AccessBundle.DataPolicies = nil
	expression, err, handled := RecordCompileSDKDataScopeExpression(object, []definitionmodel.ObjectSchema{object}, principal, "read")
	if err != nil || !handled || expression == nil || expression.Operator != "in" || len(expression.Values) != 0 {
		t.Fatalf("missing data allow did not compile to deny-all: %#v handled=%v err=%v", expression, handled, err)
	}

	bad := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{
		Permissions: []string{"case.read"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "case", Read: true, Predicate: &accessfixture.PredicateFixture{
			Operator: "eq", FieldKey: "missing", ValueSource: "literal", Values: []string{"one"},
		}}},
	})
	if _, err, handled := RecordCompileSDKDataScopeExpression(object, []definitionmodel.ObjectSchema{object}, bad, "read"); err == nil || !handled {
		t.Fatalf("invalid fact accepted handled=%v err=%v", handled, err)
	}
}

func TestSDKScopeExpressionDirectMatcherSupportsPortableOperators(t *testing.T) {
	record := recordmodel.Record{ID: "case-1", Data: map[string]any{"status": "active", "note": "present"}}
	tests := []struct {
		expression recordmodel.RecordScopeExpression
		want       bool
	}{
		{recordmodel.RecordScopeExpression{Operator: "eq", FieldKey: "id", Values: []string{"case-1"}}, true},
		{recordmodel.RecordScopeExpression{Operator: "starts_with", FieldKey: "status", Values: []string{"act"}}, true},
		{recordmodel.RecordScopeExpression{Operator: "exists", FieldKey: "note"}, true},
		{recordmodel.RecordScopeExpression{Operator: "not_exists", FieldKey: "missing"}, true},
		{recordmodel.RecordScopeExpression{Operator: "not", Children: []recordmodel.RecordScopeExpression{{Operator: "eq", FieldKey: "status", Values: []string{"closed"}}}}, true},
		{recordmodel.RecordScopeExpression{Operator: "or", Children: []recordmodel.RecordScopeExpression{{Operator: "eq", FieldKey: "status", Values: []string{"closed"}}, {Operator: "eq", FieldKey: "status", Values: []string{"active"}}}}, true},
		{recordmodel.RecordScopeExpression{Operator: "unknown"}, false},
	}
	for _, test := range tests {
		if got := directSDKScopeExpressionMatches(test.expression, record); got != test.want {
			t.Fatalf("expression=%#v got=%v want=%v", test.expression, got, test.want)
		}
	}

	if normalizeSDKScopeAction("view") != "read" || normalizeSDKScopeAction("edit") != "update" {
		t.Fatal("SDK action aliases were not normalized")
	}
}
