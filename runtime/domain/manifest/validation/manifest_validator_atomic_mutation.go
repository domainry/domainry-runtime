package validation

import (
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func (state *validationState) validateAtomicMutationBatch(action definitionmodel.ActionSchema, step map[string]any, path string) {
	mutations := validationMapSlice(step["mutations"])
	if len(mutations) == 0 {
		state.add(path+".mutations", "backend.action.atomic_mutations_required")
		return
	}
	allAliases := map[string]bool{}
	for _, mutation := range mutations {
		if len(validationStringList(mutation["returning"])) > 0 {
			allAliases[cleanManifestReference(firstValidationValue(mutation["as"], mutation["key"]))] = true
		}
	}
	aliases := map[string]map[string]definitionmodel.FieldSchema{}
	for index, mutation := range mutations {
		mutationPath := fmt.Sprintf("%s.mutations[%d]", path, index)
		operation := cleanManifestReference(mutation["operation"])
		if operation != "create" && operation != "update" {
			state.add(mutationPath+".operation", "backend.action.atomic_operation_invalid: %s", operation)
		}
		objectKey := cleanManifestReference(mutation["object_key"])
		if objectKey == "" {
			objectKey = strings.TrimSpace(action.ObjectKey)
		}
		if strings.HasPrefix(objectKey, "$") {
			state.add(mutationPath+".object_key", "backend.action.atomic_dynamic_object_forbidden")
			continue
		}
		object, found := state.objects[objectKey]
		if !found {
			continue
		}
		fields := manifestAtomicFields(object)
		for _, key := range []string{"data", "patch", "where"} {
			for field, value := range validationMap(mutation[key]) {
				state.validateAtomicMutationFieldReference(fields, aliases, allAliases, field, value, mutationPath+"."+key+"."+field)
			}
		}
		for _, key := range []string{"increment", "decrement"} {
			for field, value := range validationMap(mutation[key]) {
				schema, ok := fields[field]
				if !ok {
					state.add(mutationPath+"."+key+"."+field, "backend.action.atomic_field_unknown: %s", field)
					continue
				}
				if schema.Type != "number" && schema.Type != "percent" && schema.Type != "currency" {
					state.add(mutationPath+"."+key+"."+field, "backend.action.atomic_arithmetic_type_invalid: %s", schema.Type)
				}
				state.validateAtomicReturningReference(schema, aliases, allAliases, value, mutationPath+"."+key+"."+field)
			}
		}
		preconditions := validationMapSlice(mutation["preconditions"])
		if precondition := validationMap(mutation["precondition"]); len(precondition) > 0 {
			preconditions = append(preconditions, precondition)
		}
		for predicateIndex, predicate := range preconditions {
			predicatePath := fmt.Sprintf("%s.preconditions[%d]", mutationPath, predicateIndex)
			field := cleanManifestReference(predicate["field"])
			schema, ok := fields[field]
			if !ok {
				state.add(predicatePath+".field", "backend.action.atomic_field_unknown: %s", field)
			}
			switch cleanManifestReference(predicate["operator"]) {
			case "eq", "ne", "lt", "lte", "gt", "gte":
			default:
				state.add(predicatePath+".operator", "backend.action.atomic_predicate_operator_invalid")
			}
			if ok {
				state.validateAtomicReturningReference(schema, aliases, allAliases, predicate["value"], predicatePath+".value")
			}
		}
		returning := validationStringList(mutation["returning"])
		if len(returning) == 0 {
			continue
		}
		alias := cleanManifestReference(firstValidationValue(mutation["as"], mutation["key"]))
		if alias == "" {
			state.add(mutationPath+".as", "backend.action.atomic_returning_alias_required")
			continue
		}
		if _, duplicate := aliases[alias]; duplicate {
			state.add(mutationPath+".as", "backend.action.atomic_returning_alias_duplicate: %s", alias)
			continue
		}
		returned := map[string]definitionmodel.FieldSchema{"id": {Key: "id", Type: "text"}}
		for _, field := range returning {
			if field == "id" {
				continue
			}
			schema, ok := fields[field]
			if !ok {
				state.add(mutationPath+".returning", "backend.action.atomic_returning_field_invalid: %s", field)
				continue
			}
			returned[field] = schema
		}
		aliases[alias] = returned
	}
}

func manifestAtomicFields(object definitionmodel.ObjectSchema) map[string]definitionmodel.FieldSchema {
	fields := make(map[string]definitionmodel.FieldSchema, len(object.Fields))
	for _, field := range object.Fields {
		fields[strings.TrimSpace(field.Key)] = field
	}
	return fields
}

func (state *validationState) validateAtomicMutationFieldReference(fields map[string]definitionmodel.FieldSchema, aliases map[string]map[string]definitionmodel.FieldSchema, allAliases map[string]bool, field string, value any, path string) {
	target, ok := fields[field]
	if !ok {
		state.add(path, "backend.action.atomic_field_unknown: %s", field)
		return
	}
	state.validateAtomicReturningReference(target, aliases, allAliases, value, path)
}

func (state *validationState) validateAtomicReturningReference(target definitionmodel.FieldSchema, aliases map[string]map[string]definitionmodel.FieldSchema, allAliases map[string]bool, value any, path string) {
	text, ok := value.(string)
	if !ok || !strings.HasPrefix(strings.TrimSpace(text), "$steps.") {
		return
	}
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(text), "$steps."), ".")
	if len(parts) != 2 {
		state.add(path, "backend.action.atomic_returning_reference_invalid")
		return
	}
	returned, ok := aliases[parts[0]]
	if !ok {
		if allAliases[parts[0]] {
			state.add(path, "backend.action.atomic_returning_reference_forward: %s", parts[0])
		}
		return
	}
	source, ok := returned[parts[1]]
	if !ok {
		state.add(path, "backend.action.atomic_returning_field_invalid: %s", parts[1])
		return
	}
	if target.Type != source.Type && !manifestAtomicNumericTypes[target.Type+":"+source.Type] {
		state.add(path, "backend.action.atomic_returning_type_mismatch: %s -> %s", source.Type, target.Type)
	}
}

var manifestAtomicNumericTypes = map[string]bool{
	"number:percent": true, "percent:number": true,
}
