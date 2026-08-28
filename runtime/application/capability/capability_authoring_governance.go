package capability

import (
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	schedulerpolicy "github.com/domainry/domainry-runtime/runtime/domain/scheduler/policy"
)

func authoringSchedulerDomain() capabilitycontract.CapabilityAuthoringDomain {
	return schedulerpolicy.SchedulerAuthoringDomain()
}
