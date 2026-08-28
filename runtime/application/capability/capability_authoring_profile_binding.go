package capability

import (
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	profilebindingcontract "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/contract"
)

func authoringProfileBindingDomain() capabilitycontract.CapabilityAuthoringDomain {
	return capabilitycontract.CapabilityAuthoringDomain{Key: "principal", Capabilities: []capabilitycontract.CapabilityAuthoringDefinition{
		profilebindingcontract.ProfileBindingAuthoringCapability(),
	}}
}
