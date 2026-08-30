package contract

import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

func ApplicationSchemaAuthoringDomain() capabilitycontract.CapabilityAuthoringDomain {
	return capabilitycontract.CapabilityAuthoringDomain{Key: "schema", Capabilities: ApplicationSchemaAuthoringCapabilities()}
}

func ApplicationSchemaViewAuthoringDomain() capabilitycontract.CapabilityAuthoringDomain {
	return capabilitycontract.CapabilityAuthoringDomain{Key: "view", Capabilities: ApplicationSchemaViewAuthoringCapabilities()}
}

func ApplicationSchemaAuthoringCapabilities() []capabilitycontract.CapabilityAuthoringDefinition {
	capabilities := []capabilitycontract.CapabilityAuthoringDefinition{ApplicationSchemaObjectAuthoringCapability(), ApplicationSchemaFieldAuthoringCapability(), ApplicationSchemaRelationAuthoringCapability()}
	return append(capabilities, ApplicationSchemaDictionaryAuthoringCapabilities()...)
}

func ApplicationSchemaViewAuthoringCapabilities() []capabilitycontract.CapabilityAuthoringDefinition {
	return []capabilitycontract.CapabilityAuthoringDefinition{ApplicationSchemaViewAuthoringCapability()}
}
