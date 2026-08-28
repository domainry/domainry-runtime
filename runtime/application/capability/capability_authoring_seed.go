package capability

import (
	businessseedcontract "github.com/domainry/domainry-runtime/runtime/domain/businessseed/contract"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

func authoringSeedDomain() capabilitycontract.CapabilityAuthoringDomain {
	return businessseedcontract.BusinessSeedAuthoringDomain()
}
