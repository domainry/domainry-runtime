package action

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestActionAuthorizationAndPersistenceAuthority(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{"leave_request.recover"}})
	action := definitionmodel.ActionSchema{ObjectKey: "leave_request", RequiresPermission: "leave_request.recover"}
	if !ActionAllowed(principal, action) {
		t.Fatal("authorized Action was rejected")
	}
	persist := ActionPersistencePrincipal(principal, "leave_request")
	if !persist.HasPermission("leave_request.update") || principal.HasPermission("leave_request.update") || persist.HasPermission("employee_hr_profile.update") {
		t.Fatalf("unexpected persistence authority: caller=%#v persistence=%#v", principal.PermissionKeys(), persist.PermissionKeys())
	}
}
