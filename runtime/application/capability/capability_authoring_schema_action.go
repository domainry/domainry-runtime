package capability

import actionauthoring "github.com/domainry/domainry-runtime/runtime/domain/action/projection"
import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
import metadataauthoring "github.com/domainry/domainry-runtime/runtime/domain/metadata/contract"

func authoringSchemaDomain() capabilitycontract.CapabilityAuthoringDomain {
	return metadataauthoring.MetadataAuthoringDomain()
}

func authoringViewDomain() capabilitycontract.CapabilityAuthoringDomain {
	return metadataauthoring.MetadataViewAuthoringDomain()
}

func authoringActionDomain() capabilitycontract.CapabilityAuthoringDomain {
	return actionauthoring.ActionAuthoringDomain()
}
