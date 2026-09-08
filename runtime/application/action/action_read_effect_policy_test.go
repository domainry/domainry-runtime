package action

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestActionReadOnlyReferencePreservesCallerAccessWithoutWideningWrites(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{
		Known: true, WorkspaceID: "workspace", UserID: "member-user",
	}}, accessfixture.Bundle{
		Permissions: []string{"member.self_enroll", "member.read", "store.read"},
		DataPolicies: append(
			accessfixture.DataPoliciesForPermissions([]string{"member.self_enroll"}, identitysdk.DataScopeOwner),
			accessfixture.DataPoliciesForPermissions([]string{"member.read", "store.read"}, identitysdk.DataScopeAll)...,
		),
	})
	action := definitionmodel.ActionSchema{Key: "member.self_enroll", ObjectKey: "member"}
	effects := &definitionmodel.ActionEffectSet{
		Read:  []definitionmodel.ActionObjectEffect{{ObjectKey: "member"}, {ObjectKey: "store"}},
		Write: []definitionmodel.ActionObjectEffect{{ObjectKey: "member"}},
	}
	storePrincipal := actionReadEffectAuthorizationPrincipal(principal, effects, action, "store")
	if !recordpolicy.RecordCanAccess(storePrincipal, definitionmodel.ObjectSchema{Key: "store"}, recordmodel.Record{OwnerUserID: "store-manager"}) {
		t.Fatal("an owner-scoped enrollment cannot read a public store already readable by its caller")
	}
	memberPrincipal := actionReadEffectAuthorizationPrincipal(principal, effects, action, "member")
	if recordpolicy.RecordCanAccess(memberPrincipal, definitionmodel.ObjectSchema{Key: "member"}, recordmodel.Record{OwnerUserID: "another-member"}) {
		t.Fatal("broad caller CRUD read widened the Action's writable member scope")
	}
	if !recordpolicy.RecordCanAccess(memberPrincipal, definitionmodel.ObjectSchema{Key: "member"}, recordmodel.Record{OwnerUserID: "member-user"}) {
		t.Fatal("the Action lost access to the caller's own member")
	}
	denied := principal
	bundle := *principal.AccessBundle
	bundle.FunctionGrants = append(append([]identitysdk.FunctionGrant(nil), bundle.FunctionGrants...), identitysdk.FunctionGrant{Resource: "store", Action: "read", Effect: identitysdk.EffectDeny})
	denied.AccessBundle = &bundle
	deniedPrincipal := actionReadEffectAuthorizationPrincipal(denied, effects, action, "store")
	if recordpolicy.RecordCanAccess(deniedPrincipal, definitionmodel.ObjectSchema{Key: "store"}, recordmodel.Record{OwnerUserID: "member-user"}) {
		t.Fatal("an explicit reference read denial was bypassed")
	}
}
