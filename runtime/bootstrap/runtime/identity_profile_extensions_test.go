package runtime

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
)

type runtimeProjectProfileExtensionPublisherBinding struct {
	runtimeIdentityBindingStub
	published []identitysdk.ProjectProfileExtension
}

func (binding *runtimeProjectProfileExtensionPublisherBinding) ProjectProfileExtensionPublisher() identitysdk.ProjectProfileExtensionPublisher {
	return runtimeProjectProfileExtensionPublisher{binding: binding}
}

type runtimeProjectProfileExtensionPublisher struct {
	binding *runtimeProjectProfileExtensionPublisherBinding
}

func (publisher runtimeProjectProfileExtensionPublisher) PublishProjectProfileExtensions(_ context.Context, extensions []identitysdk.ProjectProfileExtension) error {
	publisher.binding.published = extensions
	return nil
}

func TestPublishRuntimeProjectProfileExtensionsUsesTypedOptionalPortAndDeepCopies(t *testing.T) {
	source := []profilebindingmodel.Binding{{
		ContractVersion: profilebindingmodel.ContractVersion, MinReaderVersion: profilebindingmodel.MinimumReaderVersion,
		ObjectKey: "employee_profile", IdentityRelationField: "identity_user_id", Cardinality: "one_to_one",
		BusinessIdentity: profilebindingmodel.BusinessIdentityBinding{
			Key: "employee", StatusField: "status", ActiveStatusValues: []string{"active"}, Claims: []profilebindingmodel.ClaimBinding{{ClaimKey: "store_code", FieldKey: "store_code"}},
		},
		BindingLifecycle: profilebindingmodel.Lifecycle{
			InvitationChannels: []string{"email"}, ClaimProofs: []profilebindingmodel.ClaimProof{{Type: "field", FieldKey: "employee_no"}}, RebindRequiresApproval: true, RebindRevokesSessions: true,
		},
		SummaryFields: []string{"name"}, ProfileTabs: []string{"employment"}, ProfileTabLabels: map[string]string{"employment": "Employment"},
		ProfileTabFields: map[string][]string{"employment": {"employee_no"}}, ProfileTabRelatedObjects: map[string][]string{"employment": {"shift"}},
		ProfileTabComponents: map[string][]string{"employment": {"schedule"}}, DefaultVisibility: "authenticated", RequiredPermissions: []string{"employee_profile.read"},
	}}
	binding := &runtimeProjectProfileExtensionPublisherBinding{}
	if err := publishRuntimeProjectProfileExtensions(t.Context(), binding, source); err != nil {
		t.Fatal(err)
	}
	if len(binding.published) != 1 {
		t.Fatalf("published=%#v", binding.published)
	}
	published := binding.published[0]
	if published.ObjectKey != "employee_profile" || published.BusinessIdentity.Key != "employee" || published.BusinessIdentity.Claims[0].FieldKey != "store_code" || published.BindingLifecycle.ClaimProofs[0].FieldKey != "employee_no" || !published.BindingLifecycle.RebindRevokesSessions {
		t.Fatalf("published=%#v", published)
	}
	source[0].BusinessIdentity.ActiveStatusValues[0] = "disabled"
	source[0].ProfileTabLabels["employment"] = "Mutated"
	source[0].ProfileTabFields["employment"][0] = "mutated"
	if published.BusinessIdentity.ActiveStatusValues[0] != "active" || published.ProfileTabLabels["employment"] != "Employment" || published.ProfileTabFields["employment"][0] != "employee_no" {
		t.Fatalf("publisher retained source aliases: %#v", published)
	}
	if err := publishRuntimeProjectProfileExtensions(t.Context(), &binding.runtimeIdentityBindingStub, source); err != nil {
		t.Fatalf("binding without optional publisher failed: %v", err)
	}
}

var _ identitysdk.EmbeddedProjectProfileExtensionBinding = (*runtimeProjectProfileExtensionPublisherBinding)(nil)
