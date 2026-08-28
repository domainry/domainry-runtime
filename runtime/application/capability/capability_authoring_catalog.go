package capability

import (
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"
)

const (
	RuntimeAuthoringContractVersion  = capabilitycontract.RuntimeAuthoringContractVersion
	RuntimeAuthoringContractHash     = capabilitycontract.RuntimeAuthoringContractHash
	RuntimeCapabilityContractVersion = capabilitycontract.RuntimeCapabilityContractVersion
)

var RuntimeExecutionCapabilities = capabilitycontract.RuntimeExecutionCapabilities
var RuntimeAutomationCapabilities = capabilitycontract.RuntimeAutomationCapabilities

func RuntimeAuthoringCapabilities() capabilitycontract.CapabilityRuntimeAuthoringContract {
	contract := capabilitycontract.CapabilityRuntimeAuthoringContract{
		ContractVersion:        capabilitycontract.RuntimeAuthoringContractVersion,
		SurfaceContractVersion: surfacemodel.ContractVersion,
		RuntimeVersion:         capabilitycontract.RuntimeCapabilityContractVersion,
		Domains: []capabilitycontract.CapabilityAuthoringDomain{
			authoringSchemaDomain(), authoringViewDomain(), authoringActionDomain(), authoringWorkflowDomain(),
			authoringAutomationDomain(), authoringSchedulerDomain(), authoringProfileBindingDomain(),
			authoringReportDomain(), authoringIntegrationDomain(), authoringSeedDomain(), authoringMaintenanceDomain(),
		},
		Instance: capabilitycontract.CapabilityAuthoringInstance{ObjectKeys: []string{}, FieldKeys: []capabilitycontract.CapabilityAuthoringScopedValues{}, ActionKeys: []string{}, WorkflowKeys: []string{}, ReportKeys: []string{}, PreferenceKeys: []string{}, RuleSetKeys: []string{}, RoleKeys: []string{}, PermissionKeys: []string{}, UserIDs: []string{}, WorkforceProfileIDs: []string{}, DepartmentIDs: []string{}, RoleIDs: []string{}, MenuIDs: []string{}, ConnectorKeys: []string{}, ConnectionKeys: []string{}, ConnectorOperations: []capabilitycontract.CapabilityAuthoringConnectorBinding{}},
	}
	materializeAuthoringCapabilitySurfaces(&contract)
	materializeAuthoringCapabilityPermissions(&contract)
	capabilitycontract.SortAuthoringContract(&contract)
	contract.ContractHash = capabilitycontract.ContractHash(contract)
	return contract
}

func materializeAuthoringCapabilitySurfaces(contract *capabilitycontract.CapabilityRuntimeAuthoringContract) {
	if contract == nil {
		return
	}
	for domainIndex := range contract.Domains {
		for capabilityIndex := range contract.Domains[domainIndex].Capabilities {
			definition := &contract.Domains[domainIndex].Capabilities[capabilityIndex]
			definition.Surface = surfacemodel.ProductSurfaceAdminConsole
			definition.ActorAudiences = []surfacemodel.ActorAudience{surfacemodel.ActorAudiencePlatformAdmin}
			definition.ExposureClass = surfacemodel.ExposureClassPlatformAdmin
		}
	}
}

func authoringContractHash(contract capabilitycontract.CapabilityRuntimeAuthoringContract) string {
	return capabilitycontract.ContractHash(contract)
}

func CapabilityAuthoringInstanceHash(instance capabilitycontract.CapabilityAuthoringInstance) string {
	return capabilitycontract.CapabilityAuthoringInstanceHash(instance)
}

func sortAuthoringContract(contract *capabilitycontract.CapabilityRuntimeAuthoringContract) {
	capabilitycontract.SortAuthoringContract(contract)
}

func SortAuthoringContract(contract *capabilitycontract.CapabilityRuntimeAuthoringContract) {
	capabilitycontract.SortAuthoringContract(contract)
}

func ContractHash(contract capabilitycontract.CapabilityRuntimeAuthoringContract) string {
	return capabilitycontract.ContractHash(contract)
}

func RuntimeAuthoringErrorContract(code string, params map[string]string) capabilitycontract.CapabilityAuthoringErrorContract {
	return capabilitycontract.RuntimeAuthoringErrorContract(code, params)
}
