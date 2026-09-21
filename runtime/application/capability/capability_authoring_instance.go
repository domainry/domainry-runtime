package capability

import (
	"context"
	"sort"
	"strings"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	hostsurfacemodel "github.com/domainry/domainry-runtime/runtime/domain/hostsurface/model"
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
		ObjectKeys: []string{}, BusinessCalendarKeys: []string{}, FieldKeys: []capabilitycontract.CapabilityAuthoringScopedValues{}, ActionKeys: []string{}, WorkflowKeys: []string{}, ReportKeys: []string{},
		RoleKeys: []string{}, PermissionKeys: []string{}, UserIDs: []string{}, OrgIDs: []string{}, RoleIDs: []string{}, MenuIDs: []string{},
		ConnectorKeys: []string{}, ConnectionKeys: []string{}, ConnectorOperations: []capabilitycontract.CapabilityAuthoringConnectorBinding{},
		AssigneeResolvers:                []capabilitycontract.CapabilityAuthoringAssigneeResolver{},
		NotificationAudienceResolverKeys: hostsurfacemodel.NotificationAudienceResolverKeys(),
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
	for _, calendar := range snapshot.BusinessCalendars {
		result.BusinessCalendarKeys = append(result.BusinessCalendarKeys, calendar.Key)
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
		result.OrgIDs = normalizedCapabilityReferences(references.OrgIDs)
		result.RoleIDs = normalizedCapabilityReferences(references.RoleIDs)
		result.MenuIDs = normalizedCapabilityReferences(references.MenuIDs)
	}
	if s.assigneeResolverReferences != nil {
		result.AssigneeResolvers = cloneCapabilityAssigneeResolverReferences(s.assigneeResolverReferences())
	}
	sort.Strings(result.ObjectKeys)
	sort.Strings(result.BusinessCalendarKeys)
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
	sort.Slice(result.AssigneeResolvers, func(i, j int) bool {
		return result.AssigneeResolvers[i].ResolverKey < result.AssigneeResolvers[j].ResolverKey
	})
	sort.Strings(result.NotificationAudienceResolverKeys)
	return result, nil
}

func cloneCapabilityAssigneeResolverReferences(values []capabilitycontract.CapabilityAuthoringAssigneeResolver) []capabilitycontract.CapabilityAuthoringAssigneeResolver {
	result := make([]capabilitycontract.CapabilityAuthoringAssigneeResolver, len(values))
	for index, value := range values {
		result[index] = value
		result[index].IdentityProjections = normalizedCapabilityReferences(value.IdentityProjections)
		result[index].CandidateRoleKeys = normalizedCapabilityReferences(value.CandidateRoleKeys)
		result[index].ConfigFields = append([]capabilitycontract.CapabilityAuthoringAssigneeResolverConfig(nil), value.ConfigFields...)
		for fieldIndex := range result[index].ConfigFields {
			result[index].ConfigFields[fieldIndex].Enum = normalizedCapabilityReferences(result[index].ConfigFields[fieldIndex].Enum)
		}
		sort.Slice(result[index].ConfigFields, func(i, j int) bool { return result[index].ConfigFields[i].Key < result[index].ConfigFields[j].Key })
		result[index].RecordCapabilities = append([]capabilitycontract.CapabilityAuthoringAssigneeRecordCapability(nil), value.RecordCapabilities...)
		for capabilityIndex := range result[index].RecordCapabilities {
			result[index].RecordCapabilities[capabilityIndex].Fields = normalizedCapabilityReferences(result[index].RecordCapabilities[capabilityIndex].Fields)
			result[index].RecordCapabilities[capabilityIndex].FilterFields = normalizedCapabilityReferences(result[index].RecordCapabilities[capabilityIndex].FilterFields)
		}
		sort.Slice(result[index].RecordCapabilities, func(i, j int) bool {
			return result[index].RecordCapabilities[i].Key < result[index].RecordCapabilities[j].Key
		})
		result[index].RelationCapabilities = append([]capabilitycontract.CapabilityAuthoringAssigneeRelation(nil), value.RelationCapabilities...)
		for capabilityIndex := range result[index].RelationCapabilities {
			result[index].RelationCapabilities[capabilityIndex].TargetFields = normalizedCapabilityReferences(result[index].RelationCapabilities[capabilityIndex].TargetFields)
		}
		sort.Slice(result[index].RelationCapabilities, func(i, j int) bool {
			return result[index].RelationCapabilities[i].Key < result[index].RelationCapabilities[j].Key
		})
	}
	return result
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
