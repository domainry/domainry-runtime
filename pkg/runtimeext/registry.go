package runtimeext

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	ErrBusinessHandlerRegistryFrozen = errors.New("business handler registry is frozen")
	ErrBusinessHandlerRequired       = errors.New("business handler is required")
	ErrBusinessHandlerDuplicate      = errors.New("business handler is already registered")
)

// ExtensionSet is generated project composition input. Runtime freezes its
// handlers before accepting traffic.
type ExtensionSet struct {
	BusinessHandlers              []BusinessHandler
	WorkspaceBootstrapParticipant WorkspaceBootstrapParticipant
}

// BusinessHandlerBinding is the immutable registration-time association
// between one published Action identity and its source-owned Handler.
type BusinessHandlerBinding struct {
	Descriptor HandlerDescriptor
	Handler    BusinessHandler
}

// BusinessHandlerRegistry owns startup-time Business Handler registration. It
// intentionally has no runtime replacement or unregister operation.
type BusinessHandlerRegistry struct {
	mu                 sync.RWMutex
	frozen             bool
	bindings           map[string]BusinessHandlerBinding
	workspaceBootstrap WorkspaceBootstrapParticipant
}

func NewBusinessHandlerRegistry() *BusinessHandlerRegistry {
	return &BusinessHandlerRegistry{bindings: map[string]BusinessHandlerBinding{}}
}

func (r *BusinessHandlerRegistry) Register(handler BusinessHandler) error {
	if handler == nil {
		return ErrBusinessHandlerRequired
	}
	descriptor := normalizeHandlerDescriptor(handler.Descriptor())
	if err := descriptor.Validate(); err != nil {
		return err
	}
	key := strings.TrimSpace(descriptor.ActionKey)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrBusinessHandlerRegistryFrozen
	}
	if r.bindings == nil {
		r.bindings = map[string]BusinessHandlerBinding{}
	}
	if _, exists := r.bindings[key]; exists {
		return fmt.Errorf("%w: %s", ErrBusinessHandlerDuplicate, key)
	}
	r.bindings[key] = BusinessHandlerBinding{Descriptor: descriptor, Handler: handler}
	return nil
}

func (r *BusinessHandlerRegistry) RegisterExtensionSet(set ExtensionSet) error {
	if set.WorkspaceBootstrapParticipant != nil {
		if err := set.WorkspaceBootstrapParticipant.Descriptor().Validate(); err != nil {
			return err
		}
	}
	type registration struct {
		key     string
		binding BusinessHandlerBinding
	}
	registrations := make([]registration, 0, len(set.BusinessHandlers))
	seen := map[string]bool{}
	for _, handler := range set.BusinessHandlers {
		if handler == nil {
			return ErrBusinessHandlerRequired
		}
		descriptor := normalizeHandlerDescriptor(handler.Descriptor())
		if err := descriptor.Validate(); err != nil {
			return err
		}
		key := strings.TrimSpace(descriptor.ActionKey)
		if seen[key] {
			return fmt.Errorf("%w: %s", ErrBusinessHandlerDuplicate, key)
		}
		seen[key] = true
		registrations = append(registrations, registration{key: key, binding: BusinessHandlerBinding{Descriptor: descriptor, Handler: handler}})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrBusinessHandlerRegistryFrozen
	}
	if set.WorkspaceBootstrapParticipant != nil && r.workspaceBootstrap != nil {
		return ErrWorkspaceBootstrapParticipantDuplicate
	}
	if r.bindings == nil {
		r.bindings = map[string]BusinessHandlerBinding{}
	}
	for _, current := range registrations {
		if _, exists := r.bindings[current.key]; exists {
			return fmt.Errorf("%w: %s", ErrBusinessHandlerDuplicate, current.key)
		}
	}
	for _, current := range registrations {
		r.bindings[current.key] = current.binding
	}
	if set.WorkspaceBootstrapParticipant != nil {
		r.workspaceBootstrap = set.WorkspaceBootstrapParticipant
	}
	return nil
}

func (r *BusinessHandlerRegistry) Freeze() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen = true
}

func (r *BusinessHandlerRegistry) Frozen() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frozen
}

func (r *BusinessHandlerRegistry) Binding(actionKey string) (BusinessHandlerBinding, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	binding, ok := r.bindings[strings.TrimSpace(actionKey)]
	if ok {
		binding.Descriptor = cloneHandlerDescriptor(binding.Descriptor)
	}
	return binding, ok
}

func (r *BusinessHandlerRegistry) Descriptors() []HandlerDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]HandlerDescriptor, 0, len(r.bindings))
	for _, binding := range r.bindings {
		result = append(result, cloneHandlerDescriptor(binding.Descriptor))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ActionKey < result[j].ActionKey })
	return result
}

func (r *BusinessHandlerRegistry) WorkspaceBootstrapParticipant() WorkspaceBootstrapParticipant {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.workspaceBootstrap
}

func normalizeHandlerDescriptor(descriptor HandlerDescriptor) HandlerDescriptor {
	result := cloneHandlerDescriptor(descriptor)
	result.ActionKey = strings.TrimSpace(result.ActionKey)
	result.InputType = strings.TrimSpace(result.InputType)
	result.OutputType = strings.TrimSpace(result.OutputType)
	result.InputContractSHA256 = strings.TrimSpace(result.InputContractSHA256)
	result.OutputContractSHA256 = strings.TrimSpace(result.OutputContractSHA256)
	result.HandlerRevision = strings.TrimSpace(result.HandlerRevision)
	for index := range result.ObjectCapabilities {
		capability := &result.ObjectCapabilities[index]
		capability.ObjectKey = strings.TrimSpace(capability.ObjectKey)
		for operationIndex := range capability.Operations {
			capability.Operations[operationIndex] = strings.TrimSpace(capability.Operations[operationIndex])
		}
		sort.Strings(capability.Operations)
	}
	sort.Slice(result.ObjectCapabilities, func(i, j int) bool {
		return result.ObjectCapabilities[i].ObjectKey < result.ObjectCapabilities[j].ObjectKey
	})
	for index := range result.ConnectorCapabilities {
		capability := &result.ConnectorCapabilities[index]
		capability.ConnectorKey = strings.TrimSpace(capability.ConnectorKey)
		capability.ConnectionKey = strings.TrimSpace(capability.ConnectionKey)
		capability.OperationKey = strings.TrimSpace(capability.OperationKey)
	}
	sort.Slice(result.ConnectorCapabilities, func(i, j int) bool {
		left, right := result.ConnectorCapabilities[i], result.ConnectorCapabilities[j]
		return left.ConnectorKey+"\x00"+left.ConnectionKey+"\x00"+left.OperationKey < right.ConnectorKey+"\x00"+right.ConnectionKey+"\x00"+right.OperationKey
	})
	for index := range result.NotificationEventTypes {
		result.NotificationEventTypes[index] = strings.TrimSpace(result.NotificationEventTypes[index])
	}
	sort.Strings(result.NotificationEventTypes)
	for index := range result.FileCapabilities {
		result.FileCapabilities[index] = strings.TrimSpace(result.FileCapabilities[index])
	}
	sort.Strings(result.FileCapabilities)
	for index := range result.CrossWorkspaceAggregates {
		capability := &result.CrossWorkspaceAggregates[index]
		capability.Key = strings.TrimSpace(capability.Key)
		capability.ObjectKey = strings.TrimSpace(capability.ObjectKey)
		for dimensionIndex := range capability.Dimensions {
			capability.Dimensions[dimensionIndex].Key = strings.TrimSpace(capability.Dimensions[dimensionIndex].Key)
			capability.Dimensions[dimensionIndex].Field = strings.TrimSpace(capability.Dimensions[dimensionIndex].Field)
			if capability.Dimensions[dimensionIndex].Transform != nil {
				transform := *capability.Dimensions[dimensionIndex].Transform
				if transform.DateBucket != nil {
					dateBucket := *transform.DateBucket
					dateBucket.Grain = strings.TrimSpace(dateBucket.Grain)
					dateBucket.TimeZone = strings.TrimSpace(dateBucket.TimeZone)
					transform.DateBucket = &dateBucket
				}
				capability.Dimensions[dimensionIndex].Transform = &transform
			}
		}
		sort.Slice(capability.Dimensions, func(i, j int) bool { return capability.Dimensions[i].Key < capability.Dimensions[j].Key })
		for measureIndex := range capability.Measures {
			capability.Measures[measureIndex].Key = strings.TrimSpace(capability.Measures[measureIndex].Key)
			capability.Measures[measureIndex].Field = strings.TrimSpace(capability.Measures[measureIndex].Field)
		}
		sort.Slice(capability.Measures, func(i, j int) bool { return capability.Measures[i].Key < capability.Measures[j].Key })
		for filterIndex := range capability.Filters {
			capability.Filters[filterIndex].Field = strings.TrimSpace(capability.Filters[filterIndex].Field)
			for operatorIndex := range capability.Filters[filterIndex].Operators {
				capability.Filters[filterIndex].Operators[operatorIndex] = strings.TrimSpace(capability.Filters[filterIndex].Operators[operatorIndex])
			}
			sort.Strings(capability.Filters[filterIndex].Operators)
		}
		sort.Slice(capability.Filters, func(i, j int) bool { return capability.Filters[i].Field < capability.Filters[j].Field })
	}
	sort.Slice(result.CrossWorkspaceAggregates, func(i, j int) bool {
		return result.CrossWorkspaceAggregates[i].Key < result.CrossWorkspaceAggregates[j].Key
	})
	if result.TargetOrganization != nil {
		capability := *result.TargetOrganization
		capability.Source = TargetOrganizationSource(strings.TrimSpace(string(capability.Source)))
		capability.Input = strings.TrimSpace(capability.Input)
		result.TargetOrganization = &capability
	}
	if result.IdentityHandlerDelivery != nil {
		capability := *result.IdentityHandlerDelivery
		capability.InitialCredentialOutputField = strings.TrimSpace(capability.InitialCredentialOutputField)
		capability.Operations = append([]IdentityHandlerOperation(nil), capability.Operations...)
		for index := range capability.Operations {
			capability.Operations[index] = IdentityHandlerOperation(strings.TrimSpace(string(capability.Operations[index])))
		}
		sort.Slice(capability.Operations, func(i, j int) bool { return capability.Operations[i] < capability.Operations[j] })
		capability.ProfileBindings = append([]IdentityProfileBindingCapability(nil), capability.ProfileBindings...)
		for index := range capability.ProfileBindings {
			capability.ProfileBindings[index].BindingKey = strings.TrimSpace(capability.ProfileBindings[index].BindingKey)
			capability.ProfileBindings[index].ObjectKey = strings.TrimSpace(capability.ProfileBindings[index].ObjectKey)
		}
		sort.Slice(capability.ProfileBindings, func(i, j int) bool {
			left, right := capability.ProfileBindings[i], capability.ProfileBindings[j]
			return left.BindingKey+"\x00"+left.ObjectKey < right.BindingKey+"\x00"+right.ObjectKey
		})
		result.IdentityHandlerDelivery = &capability
	}
	if result.StoreOrganizationCatalog != nil {
		capability := *result.StoreOrganizationCatalog
		result.StoreOrganizationCatalog = &capability
	}
	if result.StoreOrganizationMutation != nil {
		capability := *result.StoreOrganizationMutation
		capability.Operations = append([]StoreOrganizationMutationOperation(nil), capability.Operations...)
		for index := range capability.Operations {
			capability.Operations[index] = StoreOrganizationMutationOperation(strings.TrimSpace(string(capability.Operations[index])))
		}
		sort.Slice(capability.Operations, func(i, j int) bool { return capability.Operations[i] < capability.Operations[j] })
		result.StoreOrganizationMutation = &capability
	}
	if result.WorkspaceIdentityUsage != nil {
		capability := *result.WorkspaceIdentityUsage
		result.WorkspaceIdentityUsage = &capability
	}
	return result
}

func cloneHandlerDescriptor(descriptor HandlerDescriptor) HandlerDescriptor {
	result := descriptor
	result.ObjectCapabilities = make([]ActionObjectCapability, len(descriptor.ObjectCapabilities))
	for index, capability := range descriptor.ObjectCapabilities {
		result.ObjectCapabilities[index] = ActionObjectCapability{ObjectKey: capability.ObjectKey, Operations: append([]string(nil), capability.Operations...)}
	}
	result.ConnectorCapabilities = append([]ActionConnectorCapability(nil), descriptor.ConnectorCapabilities...)
	result.NotificationEventTypes = append([]string(nil), descriptor.NotificationEventTypes...)
	result.FileCapabilities = append([]string(nil), descriptor.FileCapabilities...)
	if descriptor.CrossWorkspaceAggregates != nil {
		result.CrossWorkspaceAggregates = make([]CrossWorkspaceAggregateCapability, len(descriptor.CrossWorkspaceAggregates))
		for index, capability := range descriptor.CrossWorkspaceAggregates {
			result.CrossWorkspaceAggregates[index] = capability
			result.CrossWorkspaceAggregates[index].Dimensions = append([]CrossWorkspaceAggregateDimension(nil), capability.Dimensions...)
			for dimensionIndex := range result.CrossWorkspaceAggregates[index].Dimensions {
				dimension := &result.CrossWorkspaceAggregates[index].Dimensions[dimensionIndex]
				if dimension.Transform != nil {
					transform := *dimension.Transform
					if transform.DateBucket != nil {
						dateBucket := *transform.DateBucket
						transform.DateBucket = &dateBucket
					}
					dimension.Transform = &transform
				}
			}
			result.CrossWorkspaceAggregates[index].Measures = append([]CrossWorkspaceAggregateMeasure(nil), capability.Measures...)
			result.CrossWorkspaceAggregates[index].Filters = make([]CrossWorkspaceAggregateFilterCapability, len(capability.Filters))
			for filterIndex, filter := range capability.Filters {
				result.CrossWorkspaceAggregates[index].Filters[filterIndex] = CrossWorkspaceAggregateFilterCapability{Field: filter.Field, Operators: append([]string(nil), filter.Operators...)}
			}
		}
	}
	if descriptor.TargetOrganization != nil {
		capability := *descriptor.TargetOrganization
		result.TargetOrganization = &capability
	}
	if descriptor.IdentityHandlerDelivery != nil {
		capability := *descriptor.IdentityHandlerDelivery
		capability.Operations = append([]IdentityHandlerOperation(nil), capability.Operations...)
		capability.ProfileBindings = append([]IdentityProfileBindingCapability(nil), capability.ProfileBindings...)
		result.IdentityHandlerDelivery = &capability
	}
	if descriptor.StoreOrganizationCatalog != nil {
		capability := *descriptor.StoreOrganizationCatalog
		result.StoreOrganizationCatalog = &capability
	}
	if descriptor.StoreOrganizationMutation != nil {
		capability := *descriptor.StoreOrganizationMutation
		capability.Operations = append([]StoreOrganizationMutationOperation(nil), capability.Operations...)
		result.StoreOrganizationMutation = &capability
	}
	if descriptor.WorkspaceIdentityUsage != nil {
		capability := *descriptor.WorkspaceIdentityUsage
		result.WorkspaceIdentityUsage = &capability
	}
	return result
}
