package composition

import (
	"context"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type CapabilityAuthoringSchemaProvider interface {
	SchemaForPrincipal(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot
}

func capabilityAssigneeResolverReferences(registry *runtimeext.ProjectExtensionRegistry) []capabilitycontract.CapabilityAuthoringAssigneeResolver {
	if registry == nil {
		return []capabilitycontract.CapabilityAuthoringAssigneeResolver{}
	}
	descriptors := registry.AssigneeResolverDescriptors()
	result := make([]capabilitycontract.CapabilityAuthoringAssigneeResolver, 0, len(descriptors))
	for _, descriptor := range descriptors {
		resolver := capabilitycontract.CapabilityAuthoringAssigneeResolver{
			ResolverKey: descriptor.ResolverKey, ResolverRevision: descriptor.ResolverRevision, ConfigContractSHA256: descriptor.ConfigContractSHA256,
			ConfigFields: []capabilitycontract.CapabilityAuthoringAssigneeResolverConfig{}, RecordCapabilities: []capabilitycontract.CapabilityAuthoringAssigneeRecordCapability{},
			RelationCapabilities: []capabilitycontract.CapabilityAuthoringAssigneeRelation{}, IdentityProjections: append([]string(nil), descriptor.IdentityProjections...),
			CandidateRoleKeys: append([]string(nil), descriptor.CandidateRoleKeys...), MaxReadOperations: descriptor.MaxReadOperations,
			MaxCandidates: descriptor.MaxCandidates, TimeoutMilliseconds: descriptor.TimeoutMilliseconds,
		}
		for _, field := range descriptor.ConfigFields {
			resolver.ConfigFields = append(resolver.ConfigFields, capabilitycontract.CapabilityAuthoringAssigneeResolverConfig{Key: field.Key, Type: string(field.Type), Required: field.Required, Enum: append([]string(nil), field.Enum...)})
		}
		for _, capability := range descriptor.RecordCapabilities {
			resolver.RecordCapabilities = append(resolver.RecordCapabilities, capabilitycontract.CapabilityAuthoringAssigneeRecordCapability{Key: capability.Key, ObjectKey: capability.ObjectKey, Fields: append([]string(nil), capability.Fields...), FilterFields: append([]string(nil), capability.FilterFields...), MaxRows: capability.MaxRows})
		}
		for _, capability := range descriptor.RelationCapabilities {
			resolver.RelationCapabilities = append(resolver.RelationCapabilities, capabilitycontract.CapabilityAuthoringAssigneeRelation{Key: capability.Key, SourceObjectKey: capability.SourceObjectKey, RelationFieldKey: capability.RelationFieldKey, TargetObjectKey: capability.TargetObjectKey, TargetFields: append([]string(nil), capability.TargetFields...), MaxTargets: capability.MaxTargets})
		}
		result = append(result, resolver)
	}
	return result
}

// CanonicalSchemaProvider exposes Runtime-owned schema facts to internal
// composition. It must never be replaced with an anonymous-principal
// projection: authorization filters are applied only at the consuming
// application boundary.
type CanonicalSchemaProvider interface {
	Schema() appschemamodel.ApplicationSchemaSnapshot
}

func newCapabilityAuthoringApplicationService(schema CapabilityAuthoringSchemaProvider) *capabilityapplication.CapabilityAuthoringApplicationService {
	return capabilityapplication.NewCapabilityAuthoringApplicationService(func(ctx context.Context, principal principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema {
		if schema == nil {
			return capabilitycontract.CapabilityInstanceSchema{}
		}
		snapshot := schema.SchemaForPrincipal(ctx, principal)
		return capabilitycontract.CapabilityInstanceSchema{Objects: snapshot.Objects, BusinessCalendars: snapshot.BusinessCalendars, Actions: snapshot.Actions, Workflows: snapshot.Workflows, Reports: snapshot.Reports, Integrations: snapshot.Integrations}
	})
}
