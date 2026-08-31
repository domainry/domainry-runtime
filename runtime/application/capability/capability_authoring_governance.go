package capability

import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

func authoringSchedulerDomain() capabilitycontract.CapabilityAuthoringDomain {
	return schedulerAuthoringDomain()
}
