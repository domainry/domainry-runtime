package capability

import (
	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

// CapabilityAuthoringApplicationService exposes authoring capabilities.
type CapabilityAuthoringApplicationService struct {
	schema               func(context.Context, principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema
	identityReferences   func(context.Context, principalmodel.Principal) (CapabilityIdentityReferences, error)
	preferenceReferences func(context.Context, principalmodel.Principal) ([]string, error)
	ruleSetReferences    func(context.Context, principalmodel.Principal) ([]string, error)
}

func (s *CapabilityAuthoringApplicationService) UseRuleSetReferenceSource(source func(context.Context, principalmodel.Principal) ([]string, error)) {
	if s != nil {
		s.ruleSetReferences = source
	}
}

func (s *CapabilityAuthoringApplicationService) UsePreferenceReferenceSource(source func(context.Context, principalmodel.Principal) ([]string, error)) {
	if s != nil {
		s.preferenceReferences = source
	}
}

type CapabilityIdentityReferences struct {
	UserIDs             []string
	WorkforceProfileIDs []string
	DepartmentIDs       []string
	RoleIDs             []string
	MenuIDs             []string
}

func NewCapabilityAuthoringApplicationService(schema func(context.Context, principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema) *CapabilityAuthoringApplicationService {
	return &CapabilityAuthoringApplicationService{schema: schema}
}

// UseIdentityReferenceSource binds Identity-owned live references at
// the composition root. Capability discovery remains an aggregator and does
// not read the Identity repository directly.
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
	if err := capabilityAuthorizeAdmin(principal); err != nil {
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
