package capability

import (
	"context"
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	definitioncontract "github.com/domainry/domainry-runtime/runtime/domain/definition/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// ReferenceValues resolves Runtime-instance values used by Plane-published
// authoring contracts. Static capability discovery and aggregation stay in
// Plane; this query reads only instance state owned by the Runtime.
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
	case "business_calendar_key":
		values = contract.Instance.BusinessCalendarKeys
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
	case "assignee_resolver_key":
		for _, resolver := range contract.Instance.AssigneeResolvers {
			values = append(values, resolver.ResolverKey)
		}
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
		case "business_action":
			values = contract.Instance.ActionKeys
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
