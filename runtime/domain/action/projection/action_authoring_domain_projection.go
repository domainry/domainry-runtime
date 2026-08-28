package projection

import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

// ActionAuthoringDomain is the Action owner's complete authoring catalog.
// The platform catalog delegates to this entrypoint and does not maintain a
// second list of Action Definition capabilities.
func ActionAuthoringDomain() capabilitycontract.CapabilityAuthoringDomain {
	return capabilitycontract.CapabilityAuthoringDomain{Key: "action", Capabilities: []capabilitycontract.CapabilityAuthoringDefinition{ActionDefinitionAuthoringCapability()}}
}
