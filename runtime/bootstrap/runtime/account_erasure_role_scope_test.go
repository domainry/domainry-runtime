package runtime

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestAccountErasureDerivesOnlyStageIdentityPermissionsWithOriginalActionScope(t *testing.T) {
	descriptors := []runtimeext.HandlerDescriptor{}
	for _, item := range []struct {
		key       string
		operation runtimeext.AccountErasureOperation
	}{{"erase_request.approve", runtimeext.AccountErasureStage}, {"erase_request.receipt", runtimeext.AccountErasureGet}} {
		descriptors = append(descriptors, runtimeRoleCapabilityDescriptor(item.key, func(d *runtimeext.HandlerDescriptor) {
			d.TargetOrganization = &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceRecordOwner}
			d.ObjectCapabilities = []runtimeext.ActionObjectCapability{{ObjectKey: "member_profile", Operations: []string{"get_for_update"}}, {ObjectKey: "erase_request", Operations: []string{"get_for_update"}}}
			d.AccountErasure = &runtimeext.AccountErasureCapability{Operations: []runtimeext.AccountErasureOperation{item.operation}, ProfileBinding: runtimeext.IdentityProfileBindingCapability{BindingKey: "member", ObjectKey: "member_profile"}, RequestObjectKey: "erase_request", RequestProfileField: "profile_id", RequestRequesterField: "requested_by"}
		}))
	}
	roles := []manifestmodel.RoleSchema{
		{Key: "operator", Name: "Operator", Audience: "any", AssignmentMode: "manual", ProvisionToWorkspaces: true, Permissions: []manifestmodel.RolePermission{{PermissionKey: "erase_request.approve", DataScope: identitysdk.DataScopeOrgChild}}},
		{Key: "observer", Name: "Observer", Audience: "any", AssignmentMode: "manual", ProvisionToWorkspaces: true, Permissions: []manifestmodel.RolePermission{{PermissionKey: "erase_request.receipt", DataScope: identitysdk.DataScopeAll}}},
	}
	catalog, err := RuntimeWorkspaceProjectRoleCatalog(nil, roles, "workspace-a", "runtime", descriptors...)
	if err != nil {
		t.Fatal(err)
	}
	operator, _ := projectRoleByKey(catalog.Roles, "operator")
	observer, _ := projectRoleByKey(catalog.Roles, "observer")
	for _, key := range []string{identitysdk.HandlerDeliveryResolvePermission, identitysdk.HandlerDeliveryDisablePermission} {
		permission, found := projectRolePermissionByKey(operator.Permissions, key)
		if !found || permission.DataScope != identitysdk.DataScopeOrgChild {
			t.Fatalf("stage scope permission=%+v found=%t", permission, found)
		}
		if _, found := projectRolePermissionByKey(observer.Permissions, key); found {
			t.Fatalf("receipt grants Identity mutation/resolution: %s", key)
		}
	}
	if _, found := projectRolePermissionByKey(operator.Permissions, identitysdk.HandlerDeliveryCreatePermission); found {
		t.Fatal("erasure grants account creation")
	}
}
