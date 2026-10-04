package runtime

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"reflect"
	"testing"
)

func TestWorkspaceCreationCapabilityGrantsCreateWithoutStoreResolution(t *testing.T) {
	descriptor := runtimeext.HandlerDescriptor{ActionKey: "maintenance.create_user", InputType: "maintenance.CreateUserInput", OutputType: "maintenance.CreateUserOutput", HandlerRevision: "test-v1", IdentityHandlerDelivery: &runtimeext.IdentityHandlerDeliveryCapability{Operations: []runtimeext.IdentityHandlerOperation{runtimeext.IdentityHandlerCreateWorkspace}, InitialCredentialOutputField: "initial_credential"}}
	permissions, err := runtimeDownstreamCapabilityPermissions([]runtimeext.HandlerDescriptor{descriptor})
	expected := map[string][]string{descriptor.ActionKey: {identitysdk.HandlerDeliveryCreatePermission}}
	if err != nil || !reflect.DeepEqual(permissions, expected) {
		t.Fatalf("permissions=%#v err=%v", permissions, err)
	}
	descriptor.TargetOrganization = &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceExplicit, Input: runtimeext.TargetOrganizationInputInvocation}
	if _, err := runtimeDownstreamCapabilityPermissions([]runtimeext.HandlerDescriptor{descriptor}); err == nil {
		t.Fatal("Workspace creation accepted an organization target")
	}
	descriptor.TargetOrganization = nil
	descriptor.IdentityHandlerDelivery.ProfileBindings = []runtimeext.IdentityProfileBindingCapability{{BindingKey: "employee", ObjectKey: "employee_profile"}}
	if _, err := runtimeDownstreamCapabilityPermissions([]runtimeext.HandlerDescriptor{descriptor}); err == nil {
		t.Fatal("Workspace creation accepted an organization profile binding")
	}
}
