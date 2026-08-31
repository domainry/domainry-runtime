package capability

import (
	"strings"

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
			authoringSchemaDomain(), authoringActionDomain(), authoringWorkflowDomain(),
			authoringAutomationDomain(), authoringSchedulerDomain(), authoringProfileBindingDomain(),
			authoringReportDomain(), authoringIntegrationDomain(), authoringMaintenanceDomain(),
			authoringIdentityDomain(),
		},
		Instance: capabilitycontract.CapabilityAuthoringInstance{ObjectKeys: []string{}, FieldKeys: []capabilitycontract.CapabilityAuthoringScopedValues{}, ActionKeys: []string{}, WorkflowKeys: []string{}, ReportKeys: []string{}, RoleKeys: []string{}, PermissionKeys: []string{}, UserIDs: []string{}, WorkforceProfileIDs: []string{}, DepartmentIDs: []string{}, RoleIDs: []string{}, MenuIDs: []string{}, ConnectorKeys: []string{}, ConnectionKeys: []string{}, ConnectorOperations: []capabilitycontract.CapabilityAuthoringConnectorBinding{}},
	}
	normalizeSourceControlledMetadataCapabilities(&contract)
	materializeAuthoringCapabilitySurfaces(&contract)
	materializeAuthoringCapabilityPermissions(&contract)
	capabilitycontract.SortAuthoringContract(&contract)
	contract.ContractHash = capabilitycontract.ContractHash(contract)
	return contract
}

// normalizeSourceControlledMetadataCapabilities removes the former online
// publication lifecycle from capabilities whose definitions live in project
// JSON. Runtime may validate and simulate candidate JSON, but it does not
// publish versions, accept optimistic-lock hashes, or emit mutation audits.
func normalizeSourceControlledMetadataCapabilities(contract *capabilitycontract.CapabilityRuntimeAuthoringContract) {
	if contract == nil {
		return
	}
	for domainIndex := range contract.Domains {
		for capabilityIndex := range contract.Domains[domainIndex].Capabilities {
			definition := &contract.Domains[domainIndex].Capabilities[capabilityIndex]
			if definition.Execution == nil || definition.Execution.ChangeControl != "source_controlled_json" {
				continue
			}
			definition.Lifecycle = "source_controlled_json"
			definition.AuditEvents = nil
			definition.ResourceOperations = nil
			definition.SystemDraftResourceType = ""
			definition.SystemDraftResourceTypeInputJSONPointer = ""
			definition.Parameters = filterSourceControlledParameters(definition.Parameters)
			definition.Errors = filterSourceControlledErrors(definition.Errors)
			definition.ConfigurationRoutes = filterSourceControlledRoutes(definition.ConfigurationRoutes)
			removeSourceControlledHashInput(definition.InputSchema)
			for exampleIndex := range definition.Examples {
				delete(definition.Examples[exampleIndex].Value, "expected_schema_hash")
			}
		}
	}
}

func filterSourceControlledParameters(values []capabilitycontract.CapabilityAuthoringParameter) []capabilitycontract.CapabilityAuthoringParameter {
	result := make([]capabilitycontract.CapabilityAuthoringParameter, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(strings.TrimSpace(value.Key), "expected_") && strings.HasSuffix(strings.TrimSpace(value.Key), "_hash") {
			continue
		}
		result = append(result, value)
	}
	return result
}

func filterSourceControlledErrors(values []capabilitycontract.CapabilityAuthoringError) []capabilitycontract.CapabilityAuthoringError {
	result := make([]capabilitycontract.CapabilityAuthoringError, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(strings.TrimSpace(value.Code), "backend.change_plan.") || strings.Contains(strings.ToLower(value.FieldPath), "expected_schema_hash") {
			continue
		}
		result = append(result, value)
	}
	return result
}

func filterSourceControlledRoutes(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if strings.Contains(value, "/change-plans") || strings.Contains(value, "/versions") || strings.Contains(value, "/rollback") || strings.HasPrefix(value, "PUT ") || strings.HasPrefix(value, "PATCH ") || strings.HasPrefix(value, "DELETE ") {
			continue
		}
		if !seen[value] {
			result, seen[value] = append(result, value), true
		}
	}
	return result
}

func removeSourceControlledHashInput(schema *capabilitycontract.CapabilityAuthoringSchema) {
	if schema == nil || schema.Properties == nil {
		return
	}
	delete(schema.Properties, "expected_schema_hash")
	kept := schema.Required[:0]
	for _, value := range schema.Required {
		if value != "expected_schema_hash" {
			kept = append(kept, value)
		}
	}
	schema.Required = kept
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
