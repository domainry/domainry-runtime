package capability

import (
	automationpolicy "github.com/domainry/domainry-runtime/runtime/domain/automation/policy"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

func authoringAutomationDomain() capabilitycontract.CapabilityAuthoringDomain {
	return automationpolicy.AutomationAuthoringDomain()
}
