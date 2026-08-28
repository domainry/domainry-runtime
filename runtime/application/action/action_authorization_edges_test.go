package action

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
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
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"order.submit", "order.approve"}})
	if !ActionAllowed(principal, definitionmodel.ActionSchema{ObjectKey: "order", Key: "order.submit"}) {
		t.Fatal("action key fallback permission rejected")
	}
	if !ActionAllowed(principal, definitionmodel.ActionSchema{ObjectKey: "order", Key: "custom", RequiresPermission: "approve"}) {
		t.Fatal("object fallback permission rejected")
	}
	if ActionAllowed(principal, definitionmodel.ActionSchema{ObjectKey: "order", Key: "order.delete"}) {
		t.Fatal("missing permission was authorized")
	}
}

func TestActionPersistencePrincipalDoesNotDuplicateOrAliasPermissions(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{Permissions: []string{"order.read", "order.update"}})
	persist := ActionPersistencePrincipal(principal, " order ")
	permissions := persist.PermissionKeys()
	if len(permissions) != 2 || !persist.HasPermission("order.update") {
		t.Fatalf("persistence permissions=%v", permissions)
	}
	permissions[0] = "changed"
	if principal.PermissionKeys()[0] != "order.read" {
		t.Fatalf("persistence permissions alias caller=%v", principal.PermissionKeys())
	}
}

func TestActionPermissionMigrationHasNoInheritedCRUDFallback(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Key: "editor", Permissions: []string{"order.read", "order.update"}})
	inherited := definitionmodel.ActionSchema{Key: "order.submit", ObjectKey: "order", RequiresPermission: "order.update"}
	dedicated := inherited
	dedicated.RequiresPermission = "order.submit"
	if !ActionAllowed(principal, inherited) {
		t.Fatal("role holding the inherited CRUD permission must see the old published Action")
	}
	if ActionAllowed(principal, dedicated) {
		t.Fatal("old CRUD permission must not authorize the dedicated Action after publication")
	}
	principal = accessfixture.WithMutation(principal, func(role *accessfixture.Bundle) {
		role.Permissions = append(role.Permissions, "order.submit")
	})
	if !ActionAllowed(principal, dedicated) {
		t.Fatal("same-draft dedicated Action grant was not honored")
	}
}
