package capability

import (
	"strings"
	"sync"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	endpointmodel "github.com/domainry/domainry-runtime/runtime/domain/endpoint/model"
	hostsurfacemodel "github.com/domainry/domainry-runtime/runtime/domain/hostsurface/model"
)

const (
	RuntimeAuthoringContractVersion  = capabilitycontract.RuntimeAuthoringContractVersion
	RuntimeAuthoringContractHash     = capabilitycontract.RuntimeAuthoringContractHash
	RuntimeCapabilityContractVersion = capabilitycontract.RuntimeCapabilityContractVersion
)

var RuntimeExecutionCapabilities = capabilitycontract.RuntimeExecutionCapabilities
var RuntimeAutomationExecutionCatalog = capabilitycontract.RuntimeAutomationExecutionCatalog

type RuntimeAuthoringCatalogDomainSummary struct {
	Key             string
	CapabilityCount int
}

type RuntimeAuthoringCatalogSummaryValue struct {
	ContractVersion         string
	EndpointContractVersion string
	RuntimeVersion          string
	ContractHash            string
	CapabilityKeys          []string
	Domains                 []RuntimeAuthoringCatalogDomainSummary
}

var runtimeAuthoringCatalogSummaryOnce sync.Once
var runtimeAuthoringCatalogSummary RuntimeAuthoringCatalogSummaryValue

// RuntimeAuthoringCatalogSummary returns immutable scalar/index facts without
// rebuilding and allocating the complete authoring JSON Schema catalog on
// every discovery or business-system index request.
func RuntimeAuthoringCatalogSummary() RuntimeAuthoringCatalogSummaryValue {
	runtimeAuthoringCatalogSummaryOnce.Do(func() {
		contract := RuntimeAuthoringCapabilities()
		summary := RuntimeAuthoringCatalogSummaryValue{
			ContractVersion: contract.ContractVersion, EndpointContractVersion: contract.EndpointContractVersion,
			RuntimeVersion: contract.RuntimeVersion, ContractHash: contract.ContractHash,
			CapabilityKeys: []string{}, Domains: []RuntimeAuthoringCatalogDomainSummary{},
		}
		for _, domain := range contract.Domains {
			summary.Domains = append(summary.Domains, RuntimeAuthoringCatalogDomainSummary{Key: domain.Key, CapabilityCount: len(domain.Capabilities)})
			for _, capability := range domain.Capabilities {
				summary.CapabilityKeys = append(summary.CapabilityKeys, capability.Key)
			}
		}
		runtimeAuthoringCatalogSummary = summary
	})
	result := runtimeAuthoringCatalogSummary
	result.CapabilityKeys = append([]string(nil), result.CapabilityKeys...)
	result.Domains = append([]RuntimeAuthoringCatalogDomainSummary(nil), result.Domains...)
	return result
}

func RuntimeAuthoringCapabilities() capabilitycontract.CapabilityRuntimeAuthoringContract {
	contract := capabilitycontract.CapabilityRuntimeAuthoringContract{
		ContractVersion:         capabilitycontract.RuntimeAuthoringContractVersion,
		EndpointContractVersion: endpointmodel.ContractVersion,
		RuntimeVersion:          capabilitycontract.RuntimeCapabilityContractVersion,
		Domains: []capabilitycontract.CapabilityAuthoringDomain{
			authoringSchemaDomain(), authoringActionDomain(), authoringWorkflowDomain(),
			authoringAutomationDomain(), authoringProfileBindingDomain(), authoringMaintenanceDomain(),
		},
		Instance: capabilitycontract.CapabilityAuthoringInstance{ObjectKeys: []string{}, BusinessCalendarKeys: []string{}, FieldKeys: []capabilitycontract.CapabilityAuthoringScopedValues{}, ActionKeys: []string{}, WorkflowKeys: []string{}, ReportKeys: []string{}, RoleKeys: []string{}, PermissionKeys: []string{}, UserIDs: []string{}, OrgIDs: []string{}, RoleIDs: []string{}, MenuIDs: []string{}, ConnectorKeys: []string{}, ConnectionKeys: []string{}, ConnectorOperations: []capabilitycontract.CapabilityAuthoringConnectorBinding{}, AssigneeResolvers: []capabilitycontract.CapabilityAuthoringAssigneeResolver{}, NotificationAudienceResolverKeys: hostsurfacemodel.NotificationAudienceResolverKeys()},
	}
	normalizeSourceControlledMetadataCapabilities(&contract)
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
