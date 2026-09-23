package runtimeext

import (
	"fmt"
	"sort"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

// ProjectActionDefinitions creates Runtime's in-memory operation catalog from
// typed Handler descriptors. JSON never supplies or duplicates these values.
func ProjectActionDefinitions(registry *ProjectExtensionRegistry) ([]definitionmodel.ActionSchema, error) {
	if registry == nil {
		return nil, nil
	}
	descriptors := registry.BusinessHandlerDescriptors()
	result := make([]definitionmodel.ActionSchema, 0, len(descriptors))
	for _, descriptor := range descriptors {
		definition, err := descriptor.ActionDefinition()
		if err != nil {
			return nil, err
		}
		result = append(result, definition)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, nil
}

// ActionDefinition projects one code-owned Handler descriptor into the
// Runtime action engine's immutable catalog contract.
func (descriptor HandlerDescriptor) ActionDefinition() (definitionmodel.ActionSchema, error) {
	descriptor = normalizeHandlerDescriptor(descriptor)
	if err := descriptor.Validate(); err != nil {
		return definitionmodel.ActionSchema{}, err
	}
	if descriptor.ObjectKey == "" || descriptor.Kind == "" {
		return definitionmodel.ActionSchema{}, fmt.Errorf("%w: %s must declare object_key and kind", ErrHandlerOperationRequired, descriptor.ActionKey)
	}
	definition := definitionmodel.ActionSchema{
		Key: descriptor.ActionKey, ObjectKey: descriptor.ObjectKey, Label: descriptor.Label,
		Kind: descriptor.Kind, RiskLevel: descriptor.RiskLevel,
		Preconditions: append([]string(nil), descriptor.Preconditions...), AuditEvent: descriptor.AuditEvent,
		InputType: descriptor.InputType, OutputType: descriptor.OutputType,
		PayloadFields: cloneDefinition(descriptor.PayloadFields), OutputFields: cloneDefinition(descriptor.OutputFields),
		Defaults: cloneDefinition(descriptor.Defaults), OptimisticConcurrency: descriptor.OptimisticConcurrency,
		ConcurrencyField: descriptor.ConcurrencyField, AssurancePolicy: cloneActionAssurancePolicy(descriptor.AssurancePolicy),
		FileOperations: append([]string(nil), descriptor.FileCapabilities...),
	}
	if definition.Label == "" {
		definition.Label = descriptor.ActionKey
	}
	if descriptor.TargetOrganization != nil {
		definition.TargetOrganization = &definitionmodel.ActionTargetOrganizationPolicy{Source: string(descriptor.TargetOrganization.Source), Input: descriptor.TargetOrganization.Input}
	}
	if descriptor.OrganizationUnitDelivery != nil {
		definition.OrganizationUnitDelivery = &definitionmodel.ActionOrganizationUnitDeliveryPolicy{ParentSource: string(descriptor.OrganizationUnitDelivery.ParentSource)}
		for _, value := range descriptor.OrganizationUnitDelivery.Operations {
			definition.OrganizationUnitDelivery.Operations = append(definition.OrganizationUnitDelivery.Operations, string(value))
		}
		for _, value := range descriptor.OrganizationUnitDelivery.NodeTypes {
			definition.OrganizationUnitDelivery.NodeTypes = append(definition.OrganizationUnitDelivery.NodeTypes, string(value))
		}
	}
	if descriptor.StoreOrganizationMutation != nil {
		definition.StoreOrganizationMutation = &definitionmodel.ActionStoreOrganizationMutationPolicy{}
		for _, value := range descriptor.StoreOrganizationMutation.Operations {
			definition.StoreOrganizationMutation.Operations = append(definition.StoreOrganizationMutation.Operations, string(value))
		}
	}
	return definition, nil
}

func cloneActionAssurancePolicy(value *definitionmodel.ActionAssurancePolicy) *definitionmodel.ActionAssurancePolicy {
	if value == nil {
		return nil
	}
	result := *value
	result.RequiredMethods = append([]string(nil), value.RequiredMethods...)
	for index := range result.RequiredMethods {
		result.RequiredMethods[index] = strings.TrimSpace(result.RequiredMethods[index])
	}
	return &result
}
