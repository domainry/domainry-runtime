package contract

import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

func MetadataAuthoringDomain() capabilitycontract.CapabilityAuthoringDomain {
	return capabilitycontract.CapabilityAuthoringDomain{Key: "schema", Capabilities: MetadataAuthoringCapabilities()}
}

func MetadataViewAuthoringDomain() capabilitycontract.CapabilityAuthoringDomain {
	return capabilitycontract.CapabilityAuthoringDomain{Key: "view", Capabilities: MetadataViewAuthoringCapabilities()}
}

func MetadataAuthoringCapabilities() []capabilitycontract.CapabilityAuthoringDefinition {
	capabilities := []capabilitycontract.CapabilityAuthoringDefinition{MetadataObjectAuthoringCapability(), MetadataFieldAuthoringCapability(), MetadataRelationAuthoringCapability()}
	return append(capabilities, MetadataDictionaryAuthoringCapabilities()...)
}

func MetadataViewAuthoringCapabilities() []capabilitycontract.CapabilityAuthoringDefinition {
	return []capabilitycontract.CapabilityAuthoringDefinition{MetadataViewAuthoringCapability()}
}
