package service

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestDependentReadScopeUsesParentNotChildOwner(t *testing.T) {
	parent := definitionmodel.ObjectSchema{Key: "deal", Fields: []definitionmodel.FieldSchema{{Key: "owner", Type: "user"}}}
	child := definitionmodel.ObjectSchema{Key: "event", Fields: []definitionmodel.FieldSchema{{Key: "deal", Type: "relation", Required: true, Validation: definitionmodel.FieldValidation{Target: "deal"}}}, AuthorizationParent: &definitionmodel.ObjectAuthorizationParent{ObjectKey: "deal", RelationField: "deal"}}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "seller"}}, accessfixture.Bundle{Permissions: []string{"deal.read", "event.read"}, DataPolicies: accessfixture.DataPoliciesForPermissions([]string{"deal.read", "event.read"}, identitysdk.DataScopeOwner)})
	// An impossible child scope must not hide a parent's own history written
	// by another actor. Parent read authorization is the only row ACL.
	principal.AccessBundle.DataPolicies[1].Predicate = identitysdk.Predicate{Fact: "not_a_child_field", Operator: identitysdk.OperatorEqual, Value: "other"}
	scope, err, handled := RecordCompileSDKDataScopeExpression(child, []definitionmodel.ObjectSchema{parent, child}, principal, "read")
	if err != nil || !handled || scope == nil || len(scope.Children) != 2 {
		t.Fatalf("inherited read failed: scope=%+v err=%v", scope, err)
	}
	leaf := scope.Children[1]
	if leaf.FieldKey != "owner_user_id" || len(leaf.Path) != 1 || leaf.Path[0].TargetObjectKey != "deal" || leaf.Path[0].RelationFieldKey != "deal" {
		t.Fatalf("not bound to parent scope: %+v", leaf)
	}
	principal.AccessBundle.FunctionGrants = principal.AccessBundle.FunctionGrants[:1]
	principal.AccessBundle.DataPolicies = principal.AccessBundle.DataPolicies[:1]
	policy := NewRecordQueryPolicyDomainService(RecordQueryPolicyDependencies{Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{parent, child} }})
	if _, err := policy.ObjectForAction(principal, child.Key, "read"); err != nil {
		t.Fatalf("separate child verb still required: %v", err)
	}
	if _, err, _ := RecordCompileSDKDataScopeExpression(child, []definitionmodel.ObjectSchema{parent, child}, principal, "read"); err != nil {
		t.Fatal(err)
	}
	parent.AuthorizationParent = &definitionmodel.ObjectAuthorizationParent{ObjectKey: "event", RelationField: "event"}
	if _, err, _ := RecordCompileSDKDataScopeExpression(child, []definitionmodel.ObjectSchema{parent, child}, principal, "read"); err == nil {
		t.Fatal("recursive ownership chain accepted")
	}
}
