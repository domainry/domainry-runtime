package capability

import (
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	workspaceprovisioncontract "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/contract"
)

func authoringIdentityDomain() capabilitycontract.CapabilityAuthoringDomain {
	return workspaceprovisioncontract.AuthoringDomain()
}
