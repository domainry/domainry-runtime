package capability

import (
	"context"
	"net/url"
	"sort"
	"strings"

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
	contract, err := s.Capabilities(ctx, principal)
	if err != nil {
		return capabilitycontract.CapabilityDiscoveryIndex{}, err
	}
	result := capabilitycontract.CapabilityDiscoveryIndex{
		ContractVersion: contract.ContractVersion, EndpointContractVersion: contract.EndpointContractVersion, RuntimeVersion: contract.RuntimeVersion, ContractHash: contract.ContractHash, InstanceHash: contract.InstanceHash,
		EndpointContracts: tenantAdminEndpointContracts(),
		Domains:           []capabilitycontract.CapabilityDomainSummary{},
	}
	for _, domain := range contract.Domains {
		result.Domains = append(result.Domains, capabilitycontract.CapabilityDomainSummary{
			Key: domain.Key, CapabilityCount: len(domain.Capabilities), DetailEndpoint: "/tenant-admin/platform-capabilities/domains/" + url.PathEscape(domain.Key),
		})
	}
	return result, nil
}

func tenantAdminEndpointContracts() []endpointmodel.RuntimeEndpointContractV1 {
	result := make([]endpointmodel.RuntimeEndpointContractV1, 0)
	for _, endpointContract := range endpointmodel.EndpointContracts {
		for _, exposure := range endpointContract.ListenerExposures {
			if exposure == endpointmodel.ListenerExposureTenantAdmin {
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
				ValidationEndpoint: definition.ValidationEndpoint, DetailEndpoint: "/tenant-admin/platform-capabilities/capabilities/" + url.PathEscape(definition.Key),
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
	contract, _, err := s.capabilitiesAndSchema(ctx, principal)
	if err != nil {
		return capabilitycontract.CapabilityDetail{}, err
	}
	capabilityKey, selection.ObjectKey, selection.ConnectorKey, selection.ProviderKey, selection.OperationKey = strings.TrimSpace(capabilityKey), strings.TrimSpace(selection.ObjectKey), strings.TrimSpace(selection.ConnectorKey), strings.TrimSpace(selection.ProviderKey), strings.TrimSpace(selection.OperationKey)
	for _, domain := range contract.Domains {
		for _, definition := range domain.Capabilities {
			if definition.Key == capabilityKey {
				return capabilitycontract.CapabilityDetail{
					ContractVersion: contract.ContractVersion, RuntimeVersion: contract.RuntimeVersion, ContractHash: contract.ContractHash,
					InstanceHash: contract.InstanceHash, Domain: domain.Key, Selection: map[string]string{}, Capability: definition,
				}, nil
			}
		}
	}
	return capabilitycontract.CapabilityDetail{}, capabilityDiscoveryNotFound("backend.capability.not_found", "capability", capabilityKey)
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
