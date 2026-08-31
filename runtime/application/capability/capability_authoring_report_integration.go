package capability

import (
	"encoding/json"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
)

func authoringReportDomain() capabilitycontract.CapabilityAuthoringDomain {
	return reportcontract.ReportAuthoringDomain()
}

func authoringIntegrationDomain() capabilitycontract.CapabilityAuthoringDomain {
	var domain capabilitycontract.CapabilityAuthoringDomain
	convertIntegrationAuthoringContract(integrationsdk.IntegrationAuthoringDomain(), &domain)
	return domain
}

func specializeIntegrationAuthoringCapability(key string, connector connectormodel.ConnectorSchema, providerKey, operationKey string) (capabilitycontract.CapabilityAuthoringDefinition, bool) {
	payload, err := json.Marshal(connector)
	if err != nil {
		return capabilitycontract.CapabilityAuthoringDefinition{}, false
	}
	definition, found, err := integrationsdk.SpecializeIntegrationAuthoringCapability(key, payload, integrationsdk.AuthoringSelection{ProviderKey: providerKey, OperationKey: operationKey})
	if err != nil || !found {
		return capabilitycontract.CapabilityAuthoringDefinition{}, false
	}
	var result capabilitycontract.CapabilityAuthoringDefinition
	convertIntegrationAuthoringContract(definition, &result)
	return result, true
}

func convertIntegrationAuthoringContract(source, target any) {
	payload, err := json.Marshal(source)
	if err != nil {
		panic("marshal Integration authoring contract: " + err.Error())
	}
	if err := json.Unmarshal(payload, target); err != nil {
		panic("adapt Integration authoring contract: " + err.Error())
	}
}
