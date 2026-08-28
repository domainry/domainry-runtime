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
	BusinessHandlers []BusinessHandler
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
	mu       sync.RWMutex
	frozen   bool
	bindings map[string]BusinessHandlerBinding
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
	return result
}
