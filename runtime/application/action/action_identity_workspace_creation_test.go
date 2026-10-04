package action

import (
	"reflect"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

func TestWorkspaceIdentityCreationUsesActionTransactionAndOneTimeCredential(t *testing.T) {
	userID := stableIdentityUserID("workspace-a", "execution-identity-1")
	delivery := &identityHandlerDeliveryStub{result: identitysdk.HandlerDeliveryResult{User: identitysdk.User{ID: userID}, InitialCredential: &identitysdk.HandlerInitialCredential{InitialPassword: "synthetic-secret", MustChangePassword: true, NoStore: true}}}
	execution := identityDeliveryTestExecution(t, delivery)
	execution.targetGrant = nil
	execution.targetOrganization = runtimeext.TargetOrganization{}
	execution.targetResolved = false
	execution.identityGrant = &runtimeext.IdentityHandlerDeliveryCapability{Operations: []runtimeext.IdentityHandlerOperation{runtimeext.IdentityHandlerCreateWorkspace}, InitialCredentialOutputField: "initial_credential"}
	request := runtimeext.IdentityHandlerDeliveryRequest{User: runtimeext.IdentityHandlerUserMutation{Operation: runtimeext.IdentityHandlerCreateWorkspace, User: runtimeext.IdentityUser{Name: "Workspace technician", Email: "synthetic@example.invalid"}, LoginMode: runtimeext.IdentityHandlerLoginPassword}, RoleKeys: []string{"technician"}}
	result, err := execution.DeliverIdentity(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	expected := identitysdk.HandlerDeliveryRequest{ContractVersion: identitysdk.HandlerDeliveryContractVersionV1, AccessToken: "trusted-token", IdempotencyKey: "execution-identity-1:identity_handler_delivery", User: identitysdk.HandlerUserMutation{Operation: identitysdk.HandlerUserCreate, User: identitysdk.User{ID: userID, Name: "Workspace technician", Email: "synthetic@example.invalid"}, LoginMode: identitysdk.HandlerLoginPassword}, RoleKeys: []string{"technician"}}
	if !reflect.DeepEqual(delivery.request, expected) {
		t.Fatalf("unexpected bound request: %#v", delivery.request)
	}
	if result.User.ID != userID || result.User.OrgID != "" || !result.InitialCredentialPending || execution.initialCredential == nil || delivery.calls != 1 {
		t.Fatalf("result=%#v calls=%d", result, delivery.calls)
	}
	if _, err := execution.DeliverIdentity(t.Context(), request); apperror.CodeOf(err) != "identity.handler_delivery_already_used" || delivery.calls != 1 {
		t.Fatalf("duplicate delivery: calls=%d err=%v", delivery.calls, err)
	}
	execution.unitOfWork.rollBack(t.Context())
}

func TestWorkspaceIdentityCreationRejectsOrganizationAndProfileOverrides(t *testing.T) {
	for _, mutation := range []string{"ungranted", "target", "user_org", "profile", "version"} {
		t.Run(mutation, func(t *testing.T) {
			delivery := &identityHandlerDeliveryStub{}
			execution := identityDeliveryTestExecution(t, delivery)
			execution.targetGrant = nil
			execution.targetOrganization = runtimeext.TargetOrganization{}
			execution.targetResolved = false
			execution.identityGrant = &runtimeext.IdentityHandlerDeliveryCapability{Operations: []runtimeext.IdentityHandlerOperation{runtimeext.IdentityHandlerCreateWorkspace}}
			request := runtimeext.IdentityHandlerDeliveryRequest{User: runtimeext.IdentityHandlerUserMutation{Operation: runtimeext.IdentityHandlerCreateWorkspace, LoginMode: runtimeext.IdentityHandlerLoginNone}}
			switch mutation {
			case "ungranted":
				execution.identityGrant.Operations = []runtimeext.IdentityHandlerOperation{runtimeext.IdentityHandlerCreate}
			case "target":
				execution.targetGrant = &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceExplicit}
			case "user_org":
				request.User.User.OrgID = "foreign-org"
			case "profile":
				request.ProfileBinding = &runtimeext.IdentityHandlerProfileBindingMutation{BindingKey: "employee"}
			case "version":
				request.User.ExpectedVersion = 1
			}
			if _, err := execution.DeliverIdentity(t.Context(), request); err == nil || delivery.calls != 0 {
				t.Fatalf("override accepted: err=%v calls=%d", err, delivery.calls)
			}
		})
	}
}
