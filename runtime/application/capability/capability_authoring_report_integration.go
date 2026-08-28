package capability

import (
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
)

func authoringReportDomain() capabilitycontract.CapabilityAuthoringDomain {
	return reportcontract.ReportAuthoringDomain()
}

func authoringIntegrationDomain() capabilitycontract.CapabilityAuthoringDomain {
	return integrationcontract.IntegrationAuthoringDomain()
}
