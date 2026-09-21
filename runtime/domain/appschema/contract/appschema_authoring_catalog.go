package contract

import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

func ApplicationSchemaAuthoringDomain() capabilitycontract.CapabilityAuthoringDomain {
	return capabilitycontract.CapabilityAuthoringDomain{Key: "schema", Capabilities: ApplicationSchemaAuthoringCapabilities()}
}

func ApplicationSchemaAuthoringCapabilities() []capabilitycontract.CapabilityAuthoringDefinition {
	capabilities := []capabilitycontract.CapabilityAuthoringDefinition{ApplicationSchemaObjectAuthoringCapability(), ApplicationSchemaFieldAuthoringCapability(), ApplicationSchemaRelationAuthoringCapability(), ApplicationSchemaBusinessCalendarAuthoringCapability()}
	return append(capabilities, ApplicationSchemaDictionaryAuthoringCapabilities()...)
}
