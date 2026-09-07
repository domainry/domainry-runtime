package action

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type ActionOwner string

const (
	ActionOwnerSystemOperation ActionOwner = "system_operation"
	ActionOwnerBusinessHandler ActionOwner = "business_handler"
)

type ActionCatalogEntry struct {
	Definition      definitionmodel.ActionSchema
	Owner           ActionOwner
	SystemOperation string
	HandlerBinding  runtimeext.BusinessHandlerBinding
	ResolutionError error
}

type SystemOperationDescriptor struct {
	Key            string
	Kind           string
	Matches        func(definitionmodel.ActionSchema) bool
	WriteOperation string
}

type SystemOperationCatalog struct {
	entries          []SystemOperationDescriptor
	validationErrors []error
}

const (
	SystemOperationCreate            = "record.create"
	SystemOperationUpdate            = "record.update"
	SystemOperationDelete            = "record.delete"
	SystemOperationRestore           = "record.restore"
	SystemOperationTransition        = "record.transition"
	SystemOperationConditionalUpdate = "record.conditional_update"
)

func NewRuntimeSystemOperationCatalog(extensions ...SystemOperationDescriptor) *SystemOperationCatalog {
	standard := []SystemOperationDescriptor{
		{Key: SystemOperationCreate, Kind: definitionmodel.ActionKindObjectCreate, WriteOperation: "create"},
		{Key: SystemOperationUpdate, Kind: definitionmodel.ActionKindRecordUpdate, WriteOperation: "update"},
		{Key: SystemOperationDelete, Kind: definitionmodel.ActionKindRecordDelete, WriteOperation: "delete"},
		{Key: SystemOperationRestore, Kind: definitionmodel.ActionKindRecordRestore, WriteOperation: "restore"},
		{Key: SystemOperationTransition, Kind: definitionmodel.ActionKindTransitionState, WriteOperation: "update"},
		{Key: SystemOperationConditionalUpdate, Kind: definitionmodel.ActionKindConditionalUpdate, WriteOperation: "conditional_update"},
	}
	return NewSystemOperationCatalog(append(append([]SystemOperationDescriptor(nil), extensions...), standard...)...)
}

func NewSystemOperationCatalog(entries ...SystemOperationDescriptor) *SystemOperationCatalog {
	result := &SystemOperationCatalog{entries: append([]SystemOperationDescriptor(nil), entries...)}
	keys, kinds := map[string]bool{}, map[string]bool{}
	for index := range result.entries {
		entry := &result.entries[index]
		entry.Key, entry.Kind, entry.WriteOperation = strings.TrimSpace(entry.Key), strings.TrimSpace(entry.Kind), strings.TrimSpace(entry.WriteOperation)
		switch {
		case entry.Key == "":
			result.validationErrors = append(result.validationErrors, fmt.Errorf("system operation key is required"))
		case keys[entry.Key]:
			result.validationErrors = append(result.validationErrors, fmt.Errorf("system operation key %s is duplicated", entry.Key))
		default:
			keys[entry.Key] = true
		}
		if entry.Kind != "" && entry.Matches != nil {
			result.validationErrors = append(result.validationErrors, fmt.Errorf("system operation %s must declare either kind or matcher, not both", entry.Key))
		} else if entry.Kind == "" && entry.Matches == nil {
			result.validationErrors = append(result.validationErrors, fmt.Errorf("system operation %s has no action kind or matcher", entry.Key))
		} else if entry.Kind != "" && kinds[entry.Kind] {
			result.validationErrors = append(result.validationErrors, fmt.Errorf("system operation action kind %s is duplicated", entry.Kind))
		} else if entry.Kind != "" {
			kinds[entry.Kind] = true
		}
		if !actionCapabilityWriteOperation(entry.WriteOperation) {
			result.validationErrors = append(result.validationErrors, fmt.Errorf("system operation %s has invalid write capability %q", entry.Key, entry.WriteOperation))
		}
	}
	sort.Slice(result.entries, func(i, j int) bool { return result.entries[i].Key < result.entries[j].Key })
	sort.Slice(result.validationErrors, func(i, j int) bool { return result.validationErrors[i].Error() < result.validationErrors[j].Error() })
	return result
}

func (c *SystemOperationCatalog) Resolve(action definitionmodel.ActionSchema) (SystemOperationDescriptor, bool, error) {
	if c == nil {
		return SystemOperationDescriptor{}, false, nil
	}
	if len(c.validationErrors) > 0 {
		return SystemOperationDescriptor{}, false, fmt.Errorf("system operation catalog is invalid: %w", errors.Join(c.validationErrors...))
	}
	var matched *SystemOperationDescriptor
	for index := range c.entries {
		entry := c.entries[index]
		matches := entry.Kind != "" && entry.Kind == strings.TrimSpace(action.Kind)
		if entry.Matches != nil {
			matches = entry.Matches(action)
		}
		if !matches {
			continue
		}
		if matched != nil {
			return SystemOperationDescriptor{}, false, fmt.Errorf("action %s matches multiple system operations: %s, %s", action.Key, matched.Key, entry.Key)
		}
		copy := entry
		matched = &copy
	}
	if matched == nil {
		return SystemOperationDescriptor{}, false, nil
	}
	return *matched, true, nil
}

type ActionCatalog struct {
	mu               sync.RWMutex
	entries          map[string]ActionCatalogEntry
	validationErrors []error
	system           *SystemOperationCatalog
	handlers         *runtimeext.BusinessHandlerRegistry
}

func NewActionCatalog(actions []definitionmodel.ActionSchema, system *SystemOperationCatalog, handlers *runtimeext.BusinessHandlerRegistry) *ActionCatalog {
	catalog := &ActionCatalog{system: system, handlers: handlers, entries: map[string]ActionCatalogEntry{}}
	catalog.Replace(actions)
	return catalog
}

func (c *ActionCatalog) Replace(actions []definitionmodel.ActionSchema) {
	next := make(map[string]ActionCatalogEntry, len(actions))
	validationErrors := make([]error, 0)
	seen := map[string]bool{}
	for index, definition := range actions {
		definition = normalizePublishedActionContract(definition)
		key := strings.TrimSpace(definition.Key)
		if key == "" {
			validationErrors = append(validationErrors, fmt.Errorf("action at index %d has an empty key", index))
			continue
		}
		if seen[key] {
			validationErrors = append(validationErrors, fmt.Errorf("action key %s is duplicated", key))
			continue
		}
		seen[key] = true
		definition.Key = key
		entry := ActionCatalogEntry{Definition: definition}
		system, systemFound, systemErr := c.system.Resolve(definition)
		binding, handlerFound := runtimeext.BusinessHandlerBinding{}, false
		if c.handlers != nil {
			binding, handlerFound = c.handlers.Binding(key)
		}
		switch {
		case systemErr != nil:
			entry.ResolutionError = systemErr
		default:
			entry.Definition, entry.Owner, entry.SystemOperation, entry.HandlerBinding, entry.ResolutionError = resolvePublishedActionOwner(definition, system, systemFound, binding, handlerFound)
			if entry.ResolutionError == nil {
				entry.ResolutionError = validatePublishedActionContract(entry)
			}
		}
		next[key] = entry
	}
	sort.Slice(validationErrors, func(i, j int) bool { return validationErrors[i].Error() < validationErrors[j].Error() })
	c.mu.Lock()
	c.entries = next
	c.validationErrors = validationErrors
	c.mu.Unlock()
}

func normalizePublishedActionContract(definition definitionmodel.ActionSchema) definitionmodel.ActionSchema {
	definition.InputType = strings.TrimSpace(definition.InputType)
	definition.OutputType = strings.TrimSpace(definition.OutputType)
	definition.InputContractSHA256 = strings.TrimSpace(definition.InputContractSHA256)
	definition.OutputContractSHA256 = strings.TrimSpace(definition.OutputContractSHA256)
	if definition.TargetOrganization != nil {
		policy := *definition.TargetOrganization
		policy.Source = strings.TrimSpace(policy.Source)
		policy.Input = strings.TrimSpace(policy.Input)
		definition.TargetOrganization = &policy
	}
	if definition.OrganizationUnitDelivery != nil {
		policy := *definition.OrganizationUnitDelivery
		policy.Operations = append([]string(nil), policy.Operations...)
		for index := range policy.Operations {
			policy.Operations[index] = strings.TrimSpace(policy.Operations[index])
		}
		sort.Strings(policy.Operations)
		policy.NodeTypes = append([]string(nil), policy.NodeTypes...)
		for index := range policy.NodeTypes {
			policy.NodeTypes[index] = strings.TrimSpace(policy.NodeTypes[index])
		}
		sort.Strings(policy.NodeTypes)
		policy.ParentSource = strings.TrimSpace(policy.ParentSource)
		definition.OrganizationUnitDelivery = &policy
	}
	if definition.StoreOrganizationMutation != nil {
		policy := *definition.StoreOrganizationMutation
		policy.Operations = append([]string(nil), policy.Operations...)
		for index := range policy.Operations {
			policy.Operations[index] = strings.TrimSpace(policy.Operations[index])
		}
		sort.Strings(policy.Operations)
		definition.StoreOrganizationMutation = &policy
	}
	return definition
}

func validatePublishedActionContract(entry ActionCatalogEntry) error {
	definition := entry.Definition
	fields := []struct {
		name     string
		catalog  string
		registry string
	}{
		{name: "input_type", catalog: definition.InputType, registry: entry.HandlerBinding.Descriptor.InputType},
		{name: "output_type", catalog: definition.OutputType, registry: entry.HandlerBinding.Descriptor.OutputType},
		{name: "input_contract_sha256", catalog: definition.InputContractSHA256, registry: entry.HandlerBinding.Descriptor.InputContractSHA256},
		{name: "output_contract_sha256", catalog: definition.OutputContractSHA256, registry: entry.HandlerBinding.Descriptor.OutputContractSHA256},
	}
	if entry.Owner == ActionOwnerSystemOperation {
		if definition.TargetOrganization != nil || definition.OrganizationUnitDelivery != nil || definition.StoreOrganizationMutation != nil {
			return fmt.Errorf("system action %s must not declare business Handler organization capability", definition.Key)
		}
		for _, field := range fields {
			if field.catalog != "" {
				return fmt.Errorf("system action %s must not declare business handler contract field %s", definition.Key, field.name)
			}
		}
		return nil
	}
	if entry.Owner != ActionOwnerBusinessHandler {
		return nil
	}
	for _, field := range fields {
		if field.catalog == "" {
			return fmt.Errorf("published action %s requires %s", definition.Key, field.name)
		}
		if field.catalog != field.registry {
			return fmt.Errorf("published action %s %s mismatch: catalog=%q registry=%q", definition.Key, field.name, field.catalog, field.registry)
		}
	}
	if !actionTargetOrganizationCapabilityMatches(definition.TargetOrganization, entry.HandlerBinding.Descriptor.TargetOrganization) {
		return fmt.Errorf("published action %s target organization capability mismatch", definition.Key)
	}
	if !actionOrganizationUnitDeliveryCapabilityMatches(definition.OrganizationUnitDelivery, entry.HandlerBinding.Descriptor.OrganizationUnitDelivery) {
		return fmt.Errorf("published action %s organization unit delivery capability mismatch", definition.Key)
	}
	if !actionStoreOrganizationMutationCapabilityMatches(definition.StoreOrganizationMutation, entry.HandlerBinding.Descriptor.StoreOrganizationMutation) {
		return fmt.Errorf("published action %s store organization mutation capability mismatch", definition.Key)
	}
	if err := validateIdentityInitialCredentialOutput(definition, entry.HandlerBinding.Descriptor.IdentityHandlerDelivery); err != nil {
		return err
	}
	if err := validateStoreOrganizationSnapshotOutput(definition, entry.HandlerBinding.Descriptor); err != nil {
		return err
	}
	return nil
}

func validateStoreOrganizationSnapshotOutput(action definitionmodel.ActionSchema, descriptor runtimeext.HandlerDescriptor) error {
	listGrants := map[string]bool{}
	for _, capability := range descriptor.ObjectCapabilities {
		for _, operation := range capability.Operations {
			if strings.TrimSpace(operation) == "list" {
				listGrants[strings.TrimSpace(capability.ObjectKey)] = true
			}
		}
	}
	count := 0
	for _, field := range action.OutputFields {
		if strings.TrimSpace(field.Type) != "store_organization_snapshot" {
			continue
		}
		count++
		if strings.TrimSpace(action.Kind) != definitionmodel.ActionKindObjectOperation {
			return fmt.Errorf("published action %s store Organization snapshot output requires object_operation", action.Key)
		}
		if descriptor.StoreOrganizationCatalog == nil || !field.Required || field.Repeated || len(field.StoreOrganizationSnapshotObjectKeys) == 0 {
			return fmt.Errorf("published action %s store Organization snapshot output %s is not closed over a catalog and required singleton Objects", action.Key, field.Key)
		}
		if descriptor.TargetOrganization != nil || descriptor.OrganizationUnitDelivery != nil || descriptor.StoreOrganizationMutation != nil || descriptor.WorkspaceIdentityUsage != nil || descriptor.IdentityHandlerDelivery != nil || len(descriptor.CrossWorkspaceAggregates) != 0 || len(descriptor.ConnectorCapabilities) != 0 || len(descriptor.NotificationEventTypes) != 0 || len(descriptor.FileCapabilities) != 0 {
			return fmt.Errorf("published action %s store Organization snapshot output may grant only read-only record access and the store Organization catalog", action.Key)
		}
		for _, objectKey := range field.StoreOrganizationSnapshotObjectKeys {
			if !listGrants[strings.TrimSpace(objectKey)] {
				return fmt.Errorf("published action %s store Organization snapshot output %s requires %s.list", action.Key, field.Key, objectKey)
			}
		}
		for _, capability := range descriptor.ObjectCapabilities {
			for _, operation := range capability.Operations {
				switch strings.TrimSpace(operation) {
				case "create", "update", "conditional_update", "conditional_update_many", "delete", "restore":
					return fmt.Errorf("published action %s store Organization snapshot output must not grant record writes", action.Key)
				}
			}
		}
	}
	if count > 1 {
		return fmt.Errorf("published action %s declares more than one store Organization snapshot output", action.Key)
	}
	return nil
}

func actionOrganizationUnitDeliveryCapabilityMatches(policy *definitionmodel.ActionOrganizationUnitDeliveryPolicy, capability *runtimeext.OrganizationUnitDeliveryCapability) bool {
	if policy == nil || capability == nil {
		return policy == nil && capability == nil
	}
	if strings.TrimSpace(policy.ParentSource) != strings.TrimSpace(string(capability.ParentSource)) || len(policy.Operations) != len(capability.Operations) || len(policy.NodeTypes) != len(capability.NodeTypes) {
		return false
	}
	for index := range policy.Operations {
		if strings.TrimSpace(policy.Operations[index]) != strings.TrimSpace(string(capability.Operations[index])) {
			return false
		}
	}
	for index := range policy.NodeTypes {
		if strings.TrimSpace(policy.NodeTypes[index]) != strings.TrimSpace(string(capability.NodeTypes[index])) {
			return false
		}
	}
	return true
}

func actionStoreOrganizationMutationCapabilityMatches(policy *definitionmodel.ActionStoreOrganizationMutationPolicy, capability *runtimeext.ActionStoreOrganizationMutationCapability) bool {
	if policy == nil || capability == nil {
		return policy == nil && capability == nil
	}
	if len(policy.Operations) != len(capability.Operations) {
		return false
	}
	for index := range policy.Operations {
		if strings.TrimSpace(policy.Operations[index]) != strings.TrimSpace(string(capability.Operations[index])) {
			return false
		}
	}
	return true
}

func validateIdentityInitialCredentialOutput(action definitionmodel.ActionSchema, capability *runtimeext.IdentityHandlerDeliveryCapability) error {
	if capability == nil || strings.TrimSpace(capability.InitialCredentialOutputField) == "" {
		return nil
	}
	key := strings.TrimSpace(capability.InitialCredentialOutputField)
	for _, field := range action.OutputFields {
		if strings.TrimSpace(field.Key) != key {
			continue
		}
		if strings.TrimSpace(field.Type) != "object" || strings.TrimSpace(field.SourceObjectKey) != "" || strings.TrimSpace(field.SourceFieldKey) != "" || field.Repeated {
			return fmt.Errorf("published action %s initial credential output field %s must be a generated non-repeated object without source lineage", action.Key, key)
		}
		return nil
	}
	return fmt.Errorf("published action %s requires generated initial credential output field %s", action.Key, key)
}

func actionTargetOrganizationCapabilityMatches(policy *definitionmodel.ActionTargetOrganizationPolicy, capability *runtimeext.ActionTargetOrganizationCapability) bool {
	if policy == nil || capability == nil {
		return policy == nil && capability == nil
	}
	return strings.TrimSpace(policy.Source) == strings.TrimSpace(string(capability.Source)) && strings.TrimSpace(policy.Input) == strings.TrimSpace(capability.Input)
}

func (c *ActionCatalog) Entry(actionKey string) (ActionCatalogEntry, bool) {
	if c == nil {
		return ActionCatalogEntry{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[strings.TrimSpace(actionKey)]
	return entry, ok
}

func (c *ActionCatalog) Definitions() []definitionmodel.ActionSchema {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	keys := make([]string, 0, len(c.entries))
	for key := range c.entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]definitionmodel.ActionSchema, 0, len(keys))
	for _, key := range keys {
		result = append(result, c.entries[key].Definition)
	}
	return result
}

func (c *ActionCatalog) HasBusinessHandlerOwner() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, entry := range c.entries {
		if entry.Owner == ActionOwnerBusinessHandler && entry.ResolutionError == nil {
			return true
		}
	}
	return false
}

func (c *ActionCatalog) ValidationErrors() []error {
	if c == nil {
		return []error{fmt.Errorf("action catalog is required")}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	validationErrors := make([]error, 0)
	validationErrors = append(validationErrors, c.validationErrors...)
	for _, err := range c.system.ValidationErrors() {
		validationErrors = append(validationErrors, fmt.Errorf("system operation catalog: %w", err))
	}
	for key, entry := range c.entries {
		if entry.ResolutionError != nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s: %w", key, entry.ResolutionError))
		}
	}
	if c.handlers != nil {
		if !c.handlers.Frozen() {
			validationErrors = append(validationErrors, fmt.Errorf("business handler registry must be frozen before Action Catalog validation"))
		}
		for _, descriptor := range c.handlers.Descriptors() {
			entry, ok := c.entries[strings.TrimSpace(descriptor.ActionKey)]
			if !ok {
				validationErrors = append(validationErrors, fmt.Errorf("business handler %s has no published action", descriptor.ActionKey))
				continue
			}
			if entry.Owner != ActionOwnerBusinessHandler {
				validationErrors = append(validationErrors, fmt.Errorf("business handler %s is not the action owner", descriptor.ActionKey))
			}
		}
	}
	sort.Slice(validationErrors, func(i, j int) bool { return validationErrors[i].Error() < validationErrors[j].Error() })
	return validationErrors
}

func (c *SystemOperationCatalog) Descriptors() []SystemOperationDescriptor {
	if c == nil {
		return nil
	}
	return append([]SystemOperationDescriptor(nil), c.entries...)
}

func (c *SystemOperationCatalog) ValidationErrors() []error {
	if c == nil {
		return []error{fmt.Errorf("system operation catalog is required")}
	}
	return append([]error(nil), c.validationErrors...)
}
