package service

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestDependentReadUsesParentCapabilityForSystemPrincipal(t *testing.T) {
	parent := definitionmodel.ObjectSchema{Key: "channel_thread"}
	child := definitionmodel.ObjectSchema{
		Key: "channel_thread_participant",
		Fields: []definitionmodel.FieldSchema{{
			Key: "channel_thread", Type: "relation", Required: true,
			Validation: definitionmodel.FieldValidation{Target: parent.Key},
		}},
		AuthorizationParent: &definitionmodel.ObjectAuthorizationParent{ObjectKey: parent.Key, RelationField: "channel_thread"},
	}
	principal := principalmodel.NewSystemPrincipal(
		"integration-owner",
		principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "execute verified integration event"),
		parent.Key+".read",
	)
	principal.WorkspaceID = "workspace"
	policy := NewRecordQueryPolicyDomainService(RecordQueryPolicyDependencies{Objects: func() []definitionmodel.ObjectSchema {
		return []definitionmodel.ObjectSchema{parent, child}
	}})

	if _, err := policy.ObjectForAction(principal, child.Key, "read"); err != nil {
		t.Fatalf("parent-authorized system read rejected: %v", err)
	}
	query := policy.NormalizeListQuery(child, recordmodel.RecordListQuery{}, principal)
	if query.AuthorizationMode != recordmodel.RecordQueryAuthorizationUnrestricted || query.ScopeExpression != nil {
		t.Fatalf("dependent system query authorization=%q scope=%+v", query.AuthorizationMode, query.ScopeExpression)
	}
	if !policy.CanAccessRecord(principal, child, recordmodel.Record{RecordID: "participant-1"}) {
		t.Fatal("dependent record was removed by the result visibility check")
	}
	allowed, err := policy.CanAccessPersistedRecordScope(context.Background(), principal, child, recordmodel.Record{RecordID: "participant-1"}, false)
	if err != nil || !allowed {
		t.Fatalf("dependent persisted read allowed=%v err=%v", allowed, err)
	}
	allowed, err = policy.CanAccessRecordAction(context.Background(), principal, child, recordmodel.Record{RecordID: "participant-1"}, "read")
	if err != nil || !allowed {
		t.Fatalf("dependent action read allowed=%v err=%v", allowed, err)
	}
	if principal.HasPermission(child.Key + ".read") {
		t.Fatal("dependent object received an independent system capability")
	}
}

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
