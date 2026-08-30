package capability

import actionauthoring "github.com/domainry/domainry-runtime/runtime/domain/action/projection"
import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
import metadataauthoring "github.com/domainry/domainry-runtime/runtime/domain/appschema/contract"

func authoringSchemaDomain() capabilitycontract.CapabilityAuthoringDomain {
	return metadataauthoring.ApplicationSchemaAuthoringDomain()
}

func authoringViewDomain() capabilitycontract.CapabilityAuthoringDomain {
	return metadataauthoring.ApplicationSchemaViewAuthoringDomain()
}

func authoringActionDomain() capabilitycontract.CapabilityAuthoringDomain {
	return actionauthoring.ActionAuthoringDomain()
}
