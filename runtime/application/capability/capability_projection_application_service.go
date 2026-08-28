package capability

import (
	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"github.com/domainry/domainry-foundation/apperror"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

func (s *CapabilityAuthoringApplicationService) ExecutionCapabilities(_ context.Context, principal principalmodel.Principal) (capabilitycontract.CapabilityExecutionCatalog, error) {
	if err := capabilityAuthorizeAdmin(principal); err != nil {
		return capabilitycontract.CapabilityExecutionCatalog{}, err
	}
	catalog := capabilitycontract.RuntimeExecutionCapabilities()
	projection := RuntimeAuthoringProjection("action", "automation", "workflow")
	catalog.AuthoringProjection = &projection
	return catalog, nil
}

func (s *CapabilityAuthoringApplicationService) MetadataProjection(_ context.Context, principal principalmodel.Principal) (CapabilityMetadataAuthoringProjection, error) {
	if err := capabilityAuthorizeAdmin(principal); err != nil {
		return CapabilityMetadataAuthoringProjection{}, err
	}
	contract := RuntimeAuthoringCapabilities()
	capabilities := []capabilitycontract.CapabilityAuthoringDefinition{}
	for _, authoringDomain := range contract.Domains {
		if authoringDomain.Key == "schema" {
			capabilities = append(capabilities, authoringDomain.Capabilities...)
			break
		}
	}
	return CapabilityMetadataAuthoringProjection{
		AuthoringProjection: RuntimeAuthoringProjection("schema"),
		Capabilities:        CapabilityLegacyObjectKinds(), AuthoringCapabilities: capabilities,
	}, nil
}

func capabilityAuthorizeAdmin(principal principalmodel.Principal) error {
	if principal.Known && principal.HasPermission("workspace.admin") {
		return nil
	}
	return &apperror.AppError{Kind: apperror.KindForbidden, Code: "auth.permission_denied"}
}
