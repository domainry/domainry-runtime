package capability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"sync"

	definitioncontract "github.com/domainry/domainry-runtime/runtime/domain/definition/contract"

	"github.com/domainry/domainry-foundation/apperror"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	endpointmodel "github.com/domainry/domainry-runtime/runtime/domain/endpoint/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type CapabilityDiscoveryFilter struct {
	Status   string
	Requires string
}

type CapabilityDetailSelection struct {
	ObjectKey    string
	ConnectorKey string
	ProviderKey  string
	OperationKey string
}

func (s *CapabilityAuthoringApplicationService) DiscoveryIndex(ctx context.Context, principal principalmodel.Principal) (capabilitycontract.CapabilityDiscoveryIndex, error) {
	return s.DiscoveryIndexExpanded(ctx, principal, false)
}

// DiscoveryIndexExpanded keeps endpoint policies out of the default discovery
// response. Callers that are explicitly auditing transport policy can opt in
// without forcing every model-facing capability lookup to carry that catalog.
func (s *CapabilityAuthoringApplicationService) DiscoveryIndexExpanded(ctx context.Context, principal principalmodel.Principal, includeEndpointContracts bool) (capabilitycontract.CapabilityDiscoveryIndex, error) {
	if err := capabilityAuthorizePrincipal(principal); err != nil {
		return capabilitycontract.CapabilityDiscoveryIndex{}, err
	}
	instance, err := s.capabilityAuthoringInstance(ctx, principal)
	if err != nil {
		return capabilitycontract.CapabilityDiscoveryIndex{}, err
	}
	catalog := RuntimeAuthoringCatalogSummary()
	endpointContractCount, endpointContractsHash := managementEndpointContractIndex()
	result := capabilitycontract.CapabilityDiscoveryIndex{
		ContractVersion: catalog.ContractVersion, EndpointContractVersion: catalog.EndpointContractVersion, RuntimeVersion: catalog.RuntimeVersion, ContractHash: catalog.ContractHash, InstanceHash: capabilitycontract.CapabilityAuthoringInstanceHash(instance),
		EndpointContractCount: endpointContractCount, EndpointContractsHash: endpointContractsHash,
		Domains: []capabilitycontract.CapabilityDomainSummary{},
	}
	if includeEndpointContracts {
		result.EndpointContracts = managementEndpointContracts()
	}
	for _, domain := range catalog.Domains {
		result.Domains = append(result.Domains, capabilitycontract.CapabilityDomainSummary{
			Key: domain.Key, CapabilityCount: domain.CapabilityCount, DetailEndpoint: "/capabilities/domains/" + url.PathEscape(domain.Key),
		})
	}
	return result, nil
}

var managementEndpointContractIndexOnce sync.Once
var managementEndpointContractCount int
var managementEndpointContractsHash string

func managementEndpointContractIndex() (int, string) {
	managementEndpointContractIndexOnce.Do(func() {
		contracts := managementEndpointContracts()
		managementEndpointContractCount = len(contracts)
		managementEndpointContractsHash = capabilityEndpointContractsHash(contracts)
	})
	return managementEndpointContractCount, managementEndpointContractsHash
}

func capabilityEndpointContractsHash(contracts []endpointmodel.RuntimeEndpointContractV1) string {
	payload, _ := json.Marshal(contracts)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func managementEndpointContracts() []endpointmodel.RuntimeEndpointContractV1 {
	result := make([]endpointmodel.RuntimeEndpointContractV1, 0)
	for _, endpointContract := range endpointmodel.EndpointContracts {
		for _, exposure := range endpointContract.ListenerExposures {
			if exposure == endpointmodel.ListenerExposureManagement {
				result = append(result, endpointContract)
				break
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].EndpointIdentity < result[j].EndpointIdentity
	})
	return result
}

func (s *CapabilityAuthoringApplicationService) DomainCapabilities(ctx context.Context, principal principalmodel.Principal, domainKey string, filter CapabilityDiscoveryFilter) (capabilitycontract.CapabilityDomainDetail, error) {
	contract, err := s.Capabilities(ctx, principal)
	if err != nil {
		return capabilitycontract.CapabilityDomainDetail{}, err
	}
	domainKey = strings.TrimSpace(domainKey)
	for _, domain := range contract.Domains {
		if domain.Key != domainKey {
			continue
		}
		result := capabilitycontract.CapabilityDomainDetail{
			ContractVersion: contract.ContractVersion, EndpointContractVersion: contract.EndpointContractVersion, RuntimeVersion: contract.RuntimeVersion, ContractHash: contract.ContractHash, InstanceHash: contract.InstanceHash,
			Key: domain.Key, Capabilities: []capabilitycontract.CapabilitySummary{},
		}
		for _, definition := range domain.Capabilities {
			if strings.TrimSpace(filter.Status) != "" && definition.Status != strings.TrimSpace(filter.Status) {
				continue
			}
			if strings.TrimSpace(filter.Requires) != "" && !capabilityStringContains(definition.Requires, strings.TrimSpace(filter.Requires)) {
				continue
			}
			result.Capabilities = append(result.Capabilities, capabilitycontract.CapabilitySummary{
				Key: definition.Key, Status: definition.Status, Lifecycle: definition.Lifecycle, Requires: definition.Requires,
				ValidationEndpoint: definition.ValidationEndpoint, DetailEndpoint: "/capabilities/" + url.PathEscape(definition.Key),
			})
		}
		return result, nil
	}
	return capabilitycontract.CapabilityDomainDetail{}, capabilityDiscoveryNotFound("backend.capability.domain_not_found", "domain", domainKey)
}

func (s *CapabilityAuthoringApplicationService) CapabilityDetail(ctx context.Context, principal principalmodel.Principal, capabilityKey string) (capabilitycontract.CapabilityDetail, error) {
	return s.CapabilityDetailSelected(ctx, principal, capabilityKey, CapabilityDetailSelection{})
}

func (s *CapabilityAuthoringApplicationService) CapabilityDetailSelected(ctx context.Context, principal principalmodel.Principal, capabilityKey string, selection CapabilityDetailSelection) (capabilitycontract.CapabilityDetail, error) {
	contract, schema, err := s.capabilitiesAndSchema(ctx, principal)
	if err != nil {
		return capabilitycontract.CapabilityDetail{}, err
	}
	capabilityKey, selection.ObjectKey, selection.ConnectorKey, selection.ProviderKey, selection.OperationKey = strings.TrimSpace(capabilityKey), strings.TrimSpace(selection.ObjectKey), strings.TrimSpace(selection.ConnectorKey), strings.TrimSpace(selection.ProviderKey), strings.TrimSpace(selection.OperationKey)
	domainKey := ""
	var selectedDefinition capabilitycontract.CapabilityAuthoringDefinition
	for _, domain := range contract.Domains {
		for _, definition := range domain.Capabilities {
			if definition.Key == capabilityKey {
				domainKey, selectedDefinition = domain.Key, definition
				break
			}
		}
		if domainKey != "" {
			break
		}
	}
	if domainKey == "" {
		return capabilitycontract.CapabilityDetail{}, capabilityDiscoveryNotFound("backend.capability.not_found", "capability", capabilityKey)
	}
	selectedInstance, selectedValues, err := capabilitySelectedInstance(contract.Instance, schema, selection)
	if err != nil {
		return capabilitycontract.CapabilityDetail{}, err
	}
	return capabilitycontract.CapabilityDetail{
		ContractVersion: contract.ContractVersion, RuntimeVersion: contract.RuntimeVersion, ContractHash: contract.ContractHash,
		InstanceHash: contract.InstanceHash, Domain: domainKey, Selection: selectedValues, Instance: selectedInstance, Capability: selectedDefinition,
	}, nil
}

func capabilitySelectedInstance(instance capabilitycontract.CapabilityAuthoringInstance, schema capabilitycontract.CapabilityInstanceSchema, selection CapabilityDetailSelection) (*capabilitycontract.CapabilityAuthoringInstance, map[string]string, error) {
	values := map[string]string{}
	if selection.ObjectKey == "" && selection.ConnectorKey == "" && selection.ProviderKey == "" && selection.OperationKey == "" {
		return nil, values, nil
	}
	if selection.ProviderKey != "" && selection.ConnectorKey == "" || selection.OperationKey != "" && selection.ConnectorKey == "" {
		return nil, nil, capabilityDiscoveryBadRequest("backend.capability.selection_connector_required", "connector_key", selection.ConnectorKey)
	}
	selected := capabilitycontract.CapabilityAuthoringInstance{
		ObjectKeys: []string{}, FieldKeys: []capabilitycontract.CapabilityAuthoringScopedValues{}, ActionKeys: []string{}, WorkflowKeys: []string{}, ReportKeys: []string{},
		RoleKeys: []string{}, PermissionKeys: []string{}, UserIDs: []string{}, OrgIDs: []string{}, RoleIDs: []string{}, MenuIDs: []string{},
		ConnectorKeys: []string{}, ConnectionKeys: []string{}, ConnectorOperations: []capabilitycontract.CapabilityAuthoringConnectorBinding{},
	}
	if selection.ObjectKey != "" {
		objectFound := false
		for _, object := range schema.Objects {
			if strings.TrimSpace(object.Key) != selection.ObjectKey {
				continue
			}
			objectFound = true
			selected.ObjectKeys = append(selected.ObjectKeys, selection.ObjectKey)
			fields := capabilitycontract.CapabilityAuthoringScopedValues{Scope: selection.ObjectKey, Values: []string{}}
			for _, field := range object.Fields {
				if key := strings.TrimSpace(field.Key); key != "" {
					fields.Values = append(fields.Values, key)
				}
			}
			sort.Strings(fields.Values)
			selected.FieldKeys = append(selected.FieldKeys, fields)
			break
		}
		if !objectFound {
			return nil, nil, capabilityDiscoveryBadRequest("backend.capability.selection_object_not_found", "object_key", selection.ObjectKey)
		}
		for _, action := range schema.Actions {
			if strings.TrimSpace(action.ObjectKey) == selection.ObjectKey {
				key := strings.TrimSpace(action.Key)
				selected.ActionKeys = append(selected.ActionKeys, key)
				selected.PermissionKeys = append(selected.PermissionKeys, key)
			}
		}
		sort.Strings(selected.ActionKeys)
		sort.Strings(selected.PermissionKeys)
		values["object_key"] = selection.ObjectKey
	}
	if selection.ConnectorKey != "" {
		connectorFound := false
		for _, binding := range instance.ConnectorOperations {
			if binding.ConnectorKey != selection.ConnectorKey {
				continue
			}
			connectorFound = true
			selectedBinding := capabilitycontract.CapabilityAuthoringConnectorBinding{ConnectorKey: binding.ConnectorKey, Ready: binding.Ready, ProviderKeys: []string{}, Operations: []string{}}
			if selection.ProviderKey == "" {
				selectedBinding.ProviderKeys = append(selectedBinding.ProviderKeys, binding.ProviderKeys...)
			} else if capabilityStringContains(binding.ProviderKeys, selection.ProviderKey) {
				selectedBinding.ProviderKeys = append(selectedBinding.ProviderKeys, selection.ProviderKey)
				values["provider_key"] = selection.ProviderKey
			} else {
				return nil, nil, capabilityDiscoveryBadRequest("backend.capability.selection_provider_not_found", "provider_key", selection.ProviderKey)
			}
			if selection.OperationKey == "" {
				selectedBinding.Operations = append(selectedBinding.Operations, binding.Operations...)
			} else if capabilityStringContains(binding.Operations, selection.OperationKey) {
				selectedBinding.Operations = append(selectedBinding.Operations, selection.OperationKey)
				values["operation_key"] = selection.OperationKey
			} else {
				return nil, nil, capabilityDiscoveryBadRequest("backend.capability.selection_operation_not_found", "operation_key", selection.OperationKey)
			}
			selected.ConnectorKeys = append(selected.ConnectorKeys, binding.ConnectorKey)
			selected.ConnectorOperations = append(selected.ConnectorOperations, selectedBinding)
			break
		}
		if !connectorFound {
			return nil, nil, capabilityDiscoveryBadRequest("backend.capability.selection_connector_not_found", "connector_key", selection.ConnectorKey)
		}
		values["connector_key"] = selection.ConnectorKey
	}
	return &selected, values, nil
}

func (s *CapabilityAuthoringApplicationService) ReferenceValues(ctx context.Context, principal principalmodel.Principal, kind, scope string) (capabilitycontract.CapabilityReferenceResult, error) {
	contract, err := s.Capabilities(ctx, principal)
	if err != nil {
		return capabilitycontract.CapabilityReferenceResult{}, err
	}
	kind, scope = strings.TrimSpace(kind), strings.TrimSpace(scope)
	values := []string{}
	switch kind {
	case "object_key":
		values = contract.Instance.ObjectKeys
	case "relation_target_object_key":
		values = append(values, contract.Instance.ObjectKeys...)
		values = append(values, definitioncontract.IdentityUserObjectKey, definitioncontract.IdentityOrganizationUnitObjectKey)
	case "field_key":
		if scope == "" {
			return capabilitycontract.CapabilityReferenceResult{}, capabilityDiscoveryBadRequest("backend.capability.reference_scope_required", "kind", kind)
		}
		for _, fields := range contract.Instance.FieldKeys {
			if fields.Scope == scope {
				values = fields.Values
				break
			}
		}
	case "action_key":
		values = contract.Instance.ActionKeys
	case "workflow_key":
		values = contract.Instance.WorkflowKeys
	case "report_key":
		values = contract.Instance.ReportKeys
	case "role_key":
		values = contract.Instance.RoleKeys
	case "permission_key":
		values = contract.Instance.PermissionKeys
	case "user_id":
		values = contract.Instance.UserIDs
	case "org_id":
		values = contract.Instance.OrgIDs
	case "role_id":
		values = contract.Instance.RoleIDs
	case "menu_id":
		values = contract.Instance.MenuIDs
	case "connector_key":
		values = contract.Instance.ConnectorKeys
	case "connection_key":
		values = contract.Instance.ConnectionKeys
	case "scheduler_target_key":
		if scope == "" {
			return capabilitycontract.CapabilityReferenceResult{}, capabilityDiscoveryBadRequest("backend.capability.reference_scope_required", "kind", kind)
		}
		switch scope {
		case "workflow":
			for _, workflowKey := range contract.Instance.WorkflowKeys {
				if strings.HasPrefix(workflowKey, "scheduled:") {
					values = append(values, workflowKey)
				} else {
					values = append(values, "scheduled:"+workflowKey)
				}
			}
		case "report_snapshot_refresh":
			values = contract.Instance.ReportKeys
		default:
			return capabilitycontract.CapabilityReferenceResult{}, capabilityDiscoveryBadRequest("backend.capability.reference_scope_invalid", "scope", scope)
		}
	case "operation_key", "provider_key":
		if scope == "" {
			return capabilitycontract.CapabilityReferenceResult{}, capabilityDiscoveryBadRequest("backend.capability.reference_scope_required", "kind", kind)
		}
		for _, binding := range contract.Instance.ConnectorOperations {
			if binding.ConnectorKey == scope {
				if kind == "operation_key" {
					values = binding.Operations
				} else {
					values = binding.ProviderKeys
				}
				break
			}
		}
	default:
		return capabilitycontract.CapabilityReferenceResult{}, capabilityDiscoveryBadRequest("backend.capability.reference_kind_invalid", "kind", kind)
	}
	values = append([]string(nil), values...)
	sort.Strings(values)
	return capabilitycontract.CapabilityReferenceResult{Kind: kind, Scope: scope, InstanceHash: contract.InstanceHash, Values: values}, nil
}

func capabilityStringContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func capabilityDiscoveryNotFound(code, key, value string) error {
	return &apperror.AppError{Kind: apperror.KindNotFound, Code: code, Params: map[string]string{key: value}}
}

func capabilityDiscoveryBadRequest(code, key, value string) error {
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: code, Params: map[string]string{key: value}}
}

func ValidateCapabilityDiscoveryHashes(expectedContractHash, actualContractHash, expectedInstanceHash, actualInstanceHash string) error {
	if expected := strings.TrimSpace(expectedContractHash); expected != "" && expected != actualContractHash {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.capability.contract_drift", Params: map[string]string{"expected_contract_hash": expected, "actual_contract_hash": actualContractHash}}
	}
	if expected := strings.TrimSpace(expectedInstanceHash); expected != "" && expected != actualInstanceHash {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.capability.instance_drift", Params: map[string]string{"expected_instance_hash": expected, "actual_instance_hash": actualInstanceHash}}
	}
	return nil
}
