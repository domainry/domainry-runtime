package action

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestActionAuthorizationWorkspaceAndPermissionBoundaries(t *testing.T) {
	for _, authorize := range []func(principalmodel.Principal) error{actionAuthorizeQuery, actionAuthorizeCommand} {
		if err := authorize(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}); err != nil {
			t.Fatalf("known workspace rejected: %v", err)
		}
		for _, principal := range []principalmodel.Principal{principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}} {
			if err := authorize(principal); apperror.CodeOf(err) != "backend.workspace_scope_required" || apperror.KindOf(err) != apperror.KindForbidden {
				t.Fatalf("invalid workspace principal=%+v error=%v", principal, err)
			}
		}
	}

	if ActionAllowed(principalmodel.Principal{}, definitionmodel.ActionSchema{Key: "order.submit"}) {
		t.Fatal("unknown principal was authorized")
	}
	permissions := []string{"order.submit", "order.approve"}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: permissions, DataPolicies: accessfixture.DataPoliciesForPermissions(permissions, identitysdk.DataScopeAll)})
	if !ActionAllowed(principal, definitionmodel.ActionSchema{ObjectKey: "order", Key: "order.submit"}) {
		t.Fatal("action key fallback permission rejected")
	}
	if !ActionAllowed(principal, definitionmodel.ActionSchema{ObjectKey: "order", Key: "order.approve"}) {
		t.Fatal("same-key Action permission rejected")
	}
	if ActionAllowed(principal, definitionmodel.ActionSchema{ObjectKey: "order", Key: "order.delete"}) {
		t.Fatal("missing permission was authorized")
	}
}

func TestActionPersistencePrincipalDoesNotDuplicateOrAliasPermissions(t *testing.T) {
	permissions := []string{"order.read", "order.update"}
	principal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{Permissions: permissions, DataPolicies: accessfixture.DataPoliciesForPermissions(permissions, identitysdk.DataScopeAll)})
	persist := ActionPersistencePrincipal(principal, definitionmodel.ActionSchema{Key: "order.update", ObjectKey: " order "}, "update")
	actualPermissions := persist.PermissionKeys()
	if len(actualPermissions) != 2 || !persist.HasPermission("order.update") {
		t.Fatalf("persistence permissions=%v", actualPermissions)
	}
	actualPermissions[0] = "changed"
	if principal.PermissionKeys()[0] != "order.read" {
		t.Fatalf("persistence permissions alias caller=%v", principal.PermissionKeys())
	}
}

func TestActionPermissionHasNoCRUDFallback(t *testing.T) {
	permissions := []string{"order.read", "order.update"}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Key: "editor", Permissions: permissions, DataPolicies: accessfixture.DataPoliciesForPermissions(permissions, identitysdk.DataScopeAll)})
	action := definitionmodel.ActionSchema{Key: "order.submit", ObjectKey: "order"}
	if ActionAllowed(principal, action) {
		t.Fatal("CRUD permission must not authorize a different Action")
	}
	principal = accessfixture.WithMutation(principal, func(role *accessfixture.Bundle) {
		role.Permissions = append(role.Permissions, "order.submit")
		role.DataPolicies = append(role.DataPolicies, accessfixture.DataPoliciesForPermissions([]string{"order.submit"}, identitysdk.DataScopeAll)...)
	})
	if !ActionAllowed(principal, action) {
		t.Fatal("same-key Action grant was not honored")
	}
}
