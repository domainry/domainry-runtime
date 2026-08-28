package action

import (
	"fmt"
	"sort"
	"strings"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func actionReadCapabilityOperation(operation string) bool {
	switch strings.TrimSpace(operation) {
	case "get", "get_for_update", "optional", "list", "exists", "count":
		return true
	default:
		return false
	}
}

func actionCapabilityWriteOperation(operation string) bool {
	switch strings.TrimSpace(operation) {
	case "create", "update", "conditional_update", "delete", "restore":
		return true
	default:
		return false
	}
}

func resolvePublishedActionOwner(definition definitionmodel.ActionSchema, system SystemOperationDescriptor, systemFound bool, binding runtimeext.BusinessHandlerBinding, handlerFound bool) (definitionmodel.ActionSchema, ActionOwner, string, runtimeext.BusinessHandlerBinding, error) {
	published, err := normalizePublishedEffectSet(definition.EffectSet)
	if err != nil {
		return definition, "", "", runtimeext.BusinessHandlerBinding{}, fmt.Errorf("action %s has invalid published effect set: %w", definition.Key, err)
	}
	systemEffect := (*definitionmodel.ActionEffectSet)(nil)
	systemEligible := false
	if systemFound {
		systemEffect = effectSetFromSystemCapability(definition.ObjectKey, system.WriteOperation)
		systemEligible = published == nil || effectCapabilityMatches(published, systemEffect)
	}
	handlerEffect := (*definitionmodel.ActionEffectSet)(nil)
	handlerEligible := false
	if handlerFound {
		handlerEffect = effectSetFromHandlerCapabilities(binding.Descriptor.ObjectCapabilities)
		handlerEligible = (published == nil || effectCapabilityMatches(published, handlerEffect)) && actionFileCapabilitiesMatch(definition.FileOperations, binding.Descriptor.FileCapabilities)
	}
	switch {
	case systemEligible && handlerEligible && handlerCapabilityRequiresBusinessOwner(definition.ObjectKey, systemEffect, handlerEffect, binding.Descriptor):
		definition.EffectSet = selectedPublishedEffectSet(published, handlerEffect)
		return definition, ActionOwnerBusinessHandler, "", binding, nil
	case systemEligible && handlerEligible:
		return definition, "", "", runtimeext.BusinessHandlerBinding{}, fmt.Errorf("action %s has both system operation %s and business handler owners", definition.Key, system.Key)
	case systemEligible:
		definition.EffectSet = selectedPublishedEffectSet(published, systemEffect)
		return definition, ActionOwnerSystemOperation, system.Key, runtimeext.BusinessHandlerBinding{}, nil
	case handlerEligible:
		definition.EffectSet = selectedPublishedEffectSet(published, handlerEffect)
		return definition, ActionOwnerBusinessHandler, "", binding, nil
	case systemFound || handlerFound:
		return definition, "", "", runtimeext.BusinessHandlerBinding{}, fmt.Errorf("action %s business write set does not match its published execution capability", definition.Key)
	default:
		return definition, "", "", runtimeext.BusinessHandlerBinding{}, fmt.Errorf("action %s has no published execution owner", definition.Key)
	}
}

func actionFileCapabilitiesMatch(published, descriptor []string) bool {
	if len(published) != len(descriptor) {
		return false
	}
	want := map[string]bool{}
	for _, value := range published {
		value = strings.TrimSpace(value)
		if value == "" || want[value] {
			return false
		}
		want[value] = true
	}
	for _, value := range descriptor {
		if !want[strings.TrimSpace(value)] {
			return false
		}
	}
	return true
}

func handlerCapabilityRequiresBusinessOwner(primaryObject string, systemEffect, handlerEffect *definitionmodel.ActionEffectSet, descriptor runtimeext.HandlerDescriptor) bool {
	if !effectWriteCapabilityMatches(handlerEffect, systemEffect) || len(descriptor.ConnectorCapabilities) > 0 {
		return true
	}
	primaryObject = strings.TrimSpace(primaryObject)
	for _, effect := range handlerEffect.Read {
		if strings.TrimSpace(effect.ObjectKey) != primaryObject {
			return true
		}
	}
	return false
}

func selectedPublishedEffectSet(published, derived *definitionmodel.ActionEffectSet) *definitionmodel.ActionEffectSet {
	if published != nil {
		return published
	}
	return derived
}

func effectSetFromSystemCapability(objectKey, operation string) *definitionmodel.ActionEffectSet {
	return &definitionmodel.ActionEffectSet{Read: []definitionmodel.ActionObjectEffect{}, Write: []definitionmodel.ActionObjectEffect{{ObjectKey: strings.TrimSpace(objectKey), Fields: []string{}, Operations: []string{strings.TrimSpace(operation)}}}}
}

func effectSetFromHandlerCapabilities(capabilities []runtimeext.ActionObjectCapability) *definitionmodel.ActionEffectSet {
	result := &definitionmodel.ActionEffectSet{Read: []definitionmodel.ActionObjectEffect{}, Write: []definitionmodel.ActionObjectEffect{}}
	for _, capability := range capabilities {
		read, write := []string{}, []string{}
		for _, raw := range capability.Operations {
			operation := strings.TrimSpace(raw)
			if actionReadCapabilityOperation(operation) {
				read = append(read, operation)
			} else if actionCapabilityWriteOperation(operation) {
				write = append(write, operation)
			}
		}
		if len(read) > 0 {
			result.Read = append(result.Read, definitionmodel.ActionObjectEffect{ObjectKey: strings.TrimSpace(capability.ObjectKey), Fields: []string{}, Operations: read})
		}
		if len(write) > 0 {
			result.Write = append(result.Write, definitionmodel.ActionObjectEffect{ObjectKey: strings.TrimSpace(capability.ObjectKey), Fields: []string{}, Operations: write})
		}
	}
	return result
}

func normalizePublishedEffectSet(value *definitionmodel.ActionEffectSet) (*definitionmodel.ActionEffectSet, error) {
	if value == nil {
		return nil, nil
	}
	result := &definitionmodel.ActionEffectSet{Read: []definitionmodel.ActionObjectEffect{}, Write: []definitionmodel.ActionObjectEffect{}}
	var err error
	result.Read, err = normalizePublishedEffects(value.Read, false)
	if err != nil {
		return nil, fmt.Errorf("read set: %w", err)
	}
	result.Write, err = normalizePublishedEffects(value.Write, true)
	if err != nil {
		return nil, fmt.Errorf("write set: %w", err)
	}
	return result, nil
}

func normalizePublishedEffects(values []definitionmodel.ActionObjectEffect, write bool) ([]definitionmodel.ActionObjectEffect, error) {
	result := make([]definitionmodel.ActionObjectEffect, 0, len(values))
	objects := map[string]bool{}
	for _, value := range values {
		objectKey := strings.TrimSpace(value.ObjectKey)
		if objectKey == "" || objects[objectKey] {
			return nil, fmt.Errorf("object key %q is blank or duplicated", objectKey)
		}
		objects[objectKey] = true
		fields, err := normalizedUniqueStrings(value.Fields)
		if err != nil {
			return nil, fmt.Errorf("object %s fields: %w", objectKey, err)
		}
		operations, err := normalizedUniqueStrings(value.Operations)
		if err != nil {
			return nil, fmt.Errorf("object %s operations: %w", objectKey, err)
		}
		direction := "read"
		if write {
			direction = "write"
		}
		for _, operation := range operations {
			if (write && !actionCapabilityWriteOperation(operation)) || (!write && !actionReadCapabilityOperation(operation)) {
				return nil, fmt.Errorf("object %s operation %q is not a valid %s capability", objectKey, operation, direction)
			}
		}
		result = append(result, definitionmodel.ActionObjectEffect{ObjectKey: objectKey, Fields: fields, Operations: operations})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ObjectKey < result[j].ObjectKey })
	return result, nil
}

func normalizedUniqueStrings(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" || seen[value] {
			return nil, fmt.Errorf("value %q is blank or duplicated", value)
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func effectWriteCapabilityMatches(published, capability *definitionmodel.ActionEffectSet) bool {
	if published == nil || capability == nil {
		return false
	}
	return effectCapabilityListMatches(published.Write, capability.Write)
}

func effectCapabilityMatches(published, capability *definitionmodel.ActionEffectSet) bool {
	if published == nil || capability == nil {
		return false
	}
	return effectCapabilityListMatches(published.Read, capability.Read) && effectCapabilityListMatches(published.Write, capability.Write)
}

func effectCapabilityListMatches(published, capability []definitionmodel.ActionObjectEffect) bool {
	if len(published) != len(capability) {
		return false
	}
	byObject := map[string]definitionmodel.ActionObjectEffect{}
	for _, effect := range capability {
		byObject[effect.ObjectKey] = effect
	}
	for _, effect := range published {
		expected, ok := byObject[effect.ObjectKey]
		if !ok {
			return false
		}
		if len(effect.Operations) > 0 && !sameStrings(effect.Operations, expected.Operations) {
			return false
		}
	}
	return true
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
