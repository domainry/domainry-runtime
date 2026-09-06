package runtime

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
)

// publishRuntimeProjectProfileExtensions sends Runtime-owned profile metadata
// through Identity's typed, in-process publication port. Remote and older
// bindings may omit the optional capability; Identity will then continue to
// use its source-owned metadata only.
func publishRuntimeProjectProfileExtensions(ctx context.Context, binding identitysdk.Binding, extensions []profilebindingmodel.Binding) error {
	provider, ok := binding.(identitysdk.EmbeddedProjectProfileExtensionBinding)
	if !ok {
		return nil
	}
	publisher := provider.ProjectProfileExtensionPublisher()
	if publisher == nil {
		return nil
	}
	return publisher.PublishProjectProfileExtensions(ctx, runtimeProjectProfileExtensions(extensions))
}

func runtimeProjectProfileExtensions(source []profilebindingmodel.Binding) []identitysdk.ProjectProfileExtension {
	result := make([]identitysdk.ProjectProfileExtension, 0, len(source))
	for _, extension := range source {
		claims := make([]identitysdk.ProjectProfileClaimBinding, 0, len(extension.BusinessIdentity.Claims))
		for _, claim := range extension.BusinessIdentity.Claims {
			claims = append(claims, identitysdk.ProjectProfileClaimBinding{ClaimKey: claim.ClaimKey, FieldKey: claim.FieldKey})
		}
		proofs := make([]identitysdk.ProjectProfileClaimProof, 0, len(extension.BindingLifecycle.ClaimProofs))
		for _, proof := range extension.BindingLifecycle.ClaimProofs {
			proofs = append(proofs, identitysdk.ProjectProfileClaimProof{Type: proof.Type, FieldKey: proof.FieldKey})
		}
		result = append(result, identitysdk.ProjectProfileExtension{
			ContractVersion: extension.ContractVersion, MinReaderVersion: extension.MinReaderVersion,
			ObjectKey: extension.ObjectKey, IdentityRelationField: extension.IdentityRelationField, Cardinality: extension.Cardinality,
			BusinessIdentity: identitysdk.ProjectBusinessIdentityBinding{
				Key: extension.BusinessIdentity.Key, StatusField: extension.BusinessIdentity.StatusField,
				ActiveStatusValues: append([]string(nil), extension.BusinessIdentity.ActiveStatusValues...), BlacklistField: extension.BusinessIdentity.BlacklistField,
				Claims: claims,
			},
			BindingLifecycle: identitysdk.ProjectProfileBindingLifecycle{
				AllowUnbound: extension.BindingLifecycle.AllowUnbound, InvitationChannels: append([]string(nil), extension.BindingLifecycle.InvitationChannels...),
				ClaimProofs: proofs, RebindRequiresApproval: extension.BindingLifecycle.RebindRequiresApproval, RebindRevokesSessions: extension.BindingLifecycle.RebindRevokesSessions,
			},
			SummaryFields: append([]string(nil), extension.SummaryFields...), ProfileTabs: append([]string(nil), extension.ProfileTabs...),
			ProfileTabLabels: cloneRuntimeStringMap(extension.ProfileTabLabels), ProfileTabFields: cloneRuntimeStringSliceMap(extension.ProfileTabFields),
			ProfileTabRelatedObjects: cloneRuntimeStringSliceMap(extension.ProfileTabRelatedObjects), ProfileTabComponents: cloneRuntimeStringSliceMap(extension.ProfileTabComponents),
			DefaultVisibility: extension.DefaultVisibility, RequiredPermissions: append([]string(nil), extension.RequiredPermissions...), StandaloneWorkspace: extension.StandaloneWorkspace,
		})
	}
	return result
}

func cloneRuntimeStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneRuntimeStringSliceMap(source map[string][]string) map[string][]string {
	if source == nil {
		return nil
	}
	result := make(map[string][]string, len(source))
	for key, value := range source {
		result[key] = append([]string(nil), value...)
	}
	return result
}
