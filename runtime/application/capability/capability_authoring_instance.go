package capability

import (
	"context"
	"sort"
	"strings"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *CapabilityAuthoringApplicationService) capabilityAuthoringInstance(ctx context.Context, principal principalmodel.Principal) (capabilitycontract.CapabilityAuthoringInstance, error) {
	snapshot := capabilitycontract.CapabilityInstanceSchema{}
	if s.schema != nil {
		snapshot = s.schema(ctx, principal)
	}
	return s.capabilityAuthoringInstanceFromSchema(ctx, principal, snapshot)
}

func (s *CapabilityAuthoringApplicationService) capabilityAuthoringInstanceFromSchema(ctx context.Context, principal principalmodel.Principal, snapshot capabilitycontract.CapabilityInstanceSchema) (capabilitycontract.CapabilityAuthoringInstance, error) {
	result := capabilitycontract.CapabilityAuthoringInstance{
		ObjectKeys: []string{}, FieldKeys: []capabilitycontract.CapabilityAuthoringScopedValues{}, ActionKeys: []string{}, WorkflowKeys: []string{}, ReportKeys: []string{},
		RoleKeys: []string{}, PermissionKeys: []string{}, UserIDs: []string{}, WorkforceProfileIDs: []string{}, DepartmentIDs: []string{}, RoleIDs: []string{}, MenuIDs: []string{},
		ConnectorKeys: []string{}, ConnectionKeys: []string{}, ConnectorOperations: []capabilitycontract.CapabilityAuthoringConnectorBinding{},
	}
	permissions := map[string]bool{}
	for _, object := range snapshot.Objects {
		result.ObjectKeys = append(result.ObjectKeys, object.Key)
		fields := capabilitycontract.CapabilityAuthoringScopedValues{Scope: object.Key, Values: []string{}}
		for _, field := range object.Fields {
			fields.Values = append(fields.Values, field.Key)
		}
		sort.Strings(fields.Values)
		result.FieldKeys = append(result.FieldKeys, fields)
	}
	for _, action := range snapshot.Actions {
		result.ActionKeys = append(result.ActionKeys, action.Key)
		if key := strings.TrimSpace(action.Key); key != "" {
			permissions[key] = true
		}
	}
	for _, workflow := range snapshot.Workflows {
		result.WorkflowKeys = append(result.WorkflowKeys, workflow.Key)
	}
	for _, report := range snapshot.Reports {
		result.ReportKeys = append(result.ReportKeys, report.Key)
	}
	connections := map[string]bool{}
	for _, connection := range snapshot.Integrations.Connections {
		connections[connection.ConnectorKey] = connections[connection.ConnectorKey] || connection.Status == "ready" || connection.Status == "enabled"
		result.ConnectionKeys = append(result.ConnectionKeys, connection.Key)
	}
	for _, connector := range snapshot.Integrations.Connectors {
		result.ConnectorKeys = append(result.ConnectorKeys, connector.Key)
		binding := capabilitycontract.CapabilityAuthoringConnectorBinding{ConnectorKey: connector.Key, ProviderKeys: []string{}, Operations: []string{}, Ready: connections[connector.Key]}
		for _, provider := range connector.Providers {
			binding.ProviderKeys = append(binding.ProviderKeys, provider.Key)
		}
		for _, operation := range connector.Operations {
			binding.Operations = append(binding.Operations, operation.Key)
		}
		sort.Strings(binding.ProviderKeys)
		sort.Strings(binding.Operations)
		result.ConnectorOperations = append(result.ConnectorOperations, binding)
	}
	for permission := range permissions {
		result.PermissionKeys = append(result.PermissionKeys, permission)
	}
	if s.identityReferences != nil {
		references, err := s.identityReferences(ctx, principal)
		if err != nil {
			return capabilitycontract.CapabilityAuthoringInstance{}, err
		}
		result.UserIDs = normalizedCapabilityReferences(references.UserIDs)
		result.WorkforceProfileIDs = normalizedCapabilityReferences(references.WorkforceProfileIDs)
		result.DepartmentIDs = normalizedCapabilityReferences(references.DepartmentIDs)
		result.RoleIDs = normalizedCapabilityReferences(references.RoleIDs)
		result.MenuIDs = normalizedCapabilityReferences(references.MenuIDs)
	}
	sort.Strings(result.ObjectKeys)
	sort.Slice(result.FieldKeys, func(i, j int) bool { return result.FieldKeys[i].Scope < result.FieldKeys[j].Scope })
	sort.Strings(result.ActionKeys)
	sort.Strings(result.WorkflowKeys)
	sort.Strings(result.ReportKeys)
	sort.Strings(result.RoleKeys)
	sort.Strings(result.PermissionKeys)
	sort.Strings(result.ConnectorKeys)
	sort.Strings(result.ConnectionKeys)
	sort.Slice(result.ConnectorOperations, func(i, j int) bool {
		return result.ConnectorOperations[i].ConnectorKey < result.ConnectorOperations[j].ConnectorKey
	})
	return result, nil
}

func normalizedCapabilityReferences(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
