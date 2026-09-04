package capability

import (
	"context"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

// CapabilityAuthoringApplicationService exposes authoring capabilities.
type CapabilityAuthoringApplicationService struct {
	schema             func(context.Context, principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema
	identityReferences func(context.Context, principalmodel.Principal) (CapabilityIdentityReferences, error)
}

func capabilityAuthorizePrincipal(principal principalmodel.Principal) error {
	if principal.Known {
		return nil
	}
	return &apperror.AppError{Kind: apperror.KindForbidden, Code: "auth.permission_denied"}
}

type CapabilityIdentityReferences struct {
	UserIDs []string
	OrgIDs  []string
	RoleIDs []string
	MenuIDs []string
}

func NewCapabilityAuthoringApplicationService(schema func(context.Context, principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema) *CapabilityAuthoringApplicationService {
	return &CapabilityAuthoringApplicationService{schema: schema}
}

// UseIdentityReferenceSource binds Identity-owned live references at the
// composition root. Runtime resolves instance values only; Plane aggregates
// the immutable capability contracts.
func (s *CapabilityAuthoringApplicationService) UseIdentityReferenceSource(_ context.Context, source func(context.Context, principalmodel.Principal) (CapabilityIdentityReferences, error)) {
	if s != nil {
		s.identityReferences = source
	}
}

func (s *CapabilityAuthoringApplicationService) Capabilities(ctx context.Context, principal principalmodel.Principal) (capabilitycontract.CapabilityRuntimeAuthoringContract, error) {
	contract, _, err := s.capabilitiesAndSchema(ctx, principal)
	return contract, err
}

func (s *CapabilityAuthoringApplicationService) capabilitiesAndSchema(ctx context.Context, principal principalmodel.Principal) (capabilitycontract.CapabilityRuntimeAuthoringContract, capabilitycontract.CapabilityInstanceSchema, error) {
	if err := capabilityAuthorizePrincipal(principal); err != nil {
		return capabilitycontract.CapabilityRuntimeAuthoringContract{}, capabilitycontract.CapabilityInstanceSchema{}, err
	}
	contract := RuntimeAuthoringCapabilities()
	snapshot := capabilitycontract.CapabilityInstanceSchema{}
	if s.schema != nil {
		snapshot = s.schema(ctx, principal)
	}
	instance, err := s.capabilityAuthoringInstanceFromSchema(ctx, principal, snapshot)
	if err != nil {
		return capabilitycontract.CapabilityRuntimeAuthoringContract{}, capabilitycontract.CapabilityInstanceSchema{}, err
	}
	contract.Instance = instance
	contract.InstanceHash = capabilitycontract.CapabilityAuthoringInstanceHash(contract.Instance)
	return contract, snapshot, nil
}
