package validation

import (
	"fmt"
	"strings"

	definitioncontract "github.com/domainry/domainry-runtime/runtime/domain/definition/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func (state *validationState) validateRoles() {
	seen := map[string]bool{}
	for index, role := range state.manifest.Roles {
		path := fmt.Sprintf("roles[%d]", index)
		key := strings.TrimSpace(role.Key)
		if key == "" {
			state.add(path+".key", "is required")
		} else if seen[key] {
			state.add(path+".key", "duplicate role key %q", key)
		}
		seen[key] = true
		if strings.TrimSpace(role.Name) == "" {
			state.add(path+".name", "is required")
		}
		audience := strings.TrimSpace(role.Audience)
		if audience == "" {
			audience = "any"
		}
		assignmentMode := strings.TrimSpace(role.AssignmentMode)
		if assignmentMode == "" {
			assignmentMode = "manual"
		}
		if role.ProvisionToWorkspaces && audience != "any" && audience != "workforce" {
			state.add(path+".provision_to_workspaces", "tenant login roles must use any or workforce audience")
		}
		if role.ProvisionToWorkspaces && assignmentMode == "system_managed" {
			state.add(path+".provision_to_workspaces", "system-managed roles cannot be provisioned as tenant login roles")
		}
		permissions := map[string]bool{}
		for permissionIndex, permission := range role.Permissions {
			permission = strings.TrimSpace(permission)
			permissionPath := fmt.Sprintf("%s.permissions[%d]", path, permissionIndex)
			if permission == "" {
				state.add(permissionPath, "is required")
			} else if permissions[permission] {
				state.add(permissionPath, "duplicate permission %q", permission)
			}
			permissions[permission] = true
		}
		dataObjects := map[string]bool{}
		for dataIndex, permission := range role.DataPermissions {
			dataPath := fmt.Sprintf("%s.data_permissions[%d]", path, dataIndex)
			objectKey := strings.TrimSpace(permission.ObjectKey)
			if objectKey == "" {
				state.add(dataPath+".object_key", "is required")
			} else if state.objects[objectKey].Key == "" {
				state.add(dataPath+".object_key", "unknown object %q", objectKey)
			} else if dataObjects[objectKey] {
				state.add(dataPath+".object_key", "duplicate data permission for object %q", objectKey)
			}
			dataObjects[objectKey] = true
			if strings.TrimSpace(permission.Scope) == "" {
				state.add(dataPath+".scope", "is required")
			}
			if strings.TrimSpace(permission.Filter) != "" && permission.Predicate != nil {
				state.add(dataPath, "filter and predicate are mutually exclusive")
			}
			if permission.Predicate != nil && state.objects[objectKey].Key != "" {
				state.validateRolePolicyExpression(dataPath+".predicate", objectKey, *permission.Predicate, 0)
			}
		}
		fieldPermissions := map[string]bool{}
		for fieldIndex, permission := range role.FieldPermissions {
			fieldPath := fmt.Sprintf("%s.field_permissions[%d]", path, fieldIndex)
			objectKey := strings.TrimSpace(permission.ObjectKey)
			fieldKey := strings.TrimSpace(permission.FieldKey)
			identity := objectKey + "\x00" + fieldKey
			if objectKey == "" || state.objects[objectKey].Key == "" {
				state.add(fieldPath+".object_key", "unknown object %q", objectKey)
			} else if fieldKey == "" {
				state.add(fieldPath+".field_key", "is required")
			} else if fieldKey != "*" && !runtimeRoleFieldExists(state.fields[objectKey], fieldKey) {
				state.add(fieldPath+".field_key", "unknown or disabled field %q on object %q", fieldKey, objectKey)
			} else if fieldPermissions[identity] {
				state.add(fieldPath+".field_key", "duplicate field permission for %s.%s", objectKey, fieldKey)
			}
			fieldPermissions[identity] = true
		}
		referencePermissions := map[string]bool{}
		for referenceIndex, permission := range role.ReferencePermissions {
			referencePath := fmt.Sprintf("%s.reference_permissions[%d]", path, referenceIndex)
			state.validateRoleReferencePermission(referencePath, permission)
			identity := strings.TrimSpace(permission.SourceObjectKey) + "\x00" + strings.TrimSpace(permission.RelationFieldKey)
			if referencePermissions[identity] {
				state.add(referencePath+".relation_field_key", "duplicate reference permission")
			}
			referencePermissions[identity] = true
		}
		exportRules := map[string]bool{}
		for exportIndex, rule := range role.ExportRules {
			exportPath := fmt.Sprintf("%s.export_rules[%d]", path, exportIndex)
			objectKey := strings.TrimSpace(rule.ObjectKey)
			if objectKey == "" || state.objects[objectKey].Key == "" {
				state.add(exportPath+".object_key", "unknown object %q", objectKey)
			} else if exportRules[objectKey] {
				state.add(exportPath+".object_key", "duplicate export rule for object %q", objectKey)
			}
			exportRules[objectKey] = true
			seenFields := map[string]bool{}
			for fieldIndex, rawFieldKey := range rule.Fields {
				fieldKey := strings.TrimSpace(rawFieldKey)
				fieldPath := fmt.Sprintf("%s.fields[%d]", exportPath, fieldIndex)
				if fieldKey == "" {
					state.add(fieldPath, "is required")
				} else if fieldKey != "id" && !runtimeRoleFieldExists(state.fields[objectKey], fieldKey) {
					state.add(fieldPath, "unknown or disabled field %q on object %q", fieldKey, objectKey)
				} else if seenFields[fieldKey] {
					state.add(fieldPath, "duplicate export field %q", fieldKey)
				}
				seenFields[fieldKey] = true
			}
		}
	}
}

func (state *validationState) validateRoleReferencePermission(path string, permission manifestmodel.RoleReferencePermission) {
	sourceKey := strings.TrimSpace(permission.SourceObjectKey)
	targetKey := strings.TrimSpace(permission.TargetObjectKey)
	relationKey := strings.TrimSpace(permission.RelationFieldKey)
	source := state.objects[sourceKey]
	if source.Key == "" {
		state.add(path+".source_object_key", "unknown object %q", sourceKey)
		return
	}
	relation := state.fields[sourceKey][relationKey]
	if relationKey == "" || relation.Key == "" || strings.TrimSpace(relation.DisabledAt) != "" {
		state.add(path+".relation_field_key", "unknown or disabled field %q on object %q", relationKey, sourceKey)
	} else if strings.TrimSpace(relation.Type) != "relation" {
		state.add(path+".relation_field_key", "field %s.%s is not a relation", sourceKey, relationKey)
	} else if actualTarget := strings.TrimSpace(relation.Validation.Target); actualTarget != targetKey {
		state.add(path+".target_object_key", "must match relation target %q", actualTarget)
	}
	target, targetExists := state.objects[targetKey]
	if !targetExists && !definitioncontract.IsFoundationObjectKey(targetKey) {
		state.add(path+".target_object_key", "unknown object %q", targetKey)
	}
	seenFields := map[string]bool{}
	for index, rawFieldKey := range permission.DisplayFields {
		fieldKey := strings.TrimSpace(rawFieldKey)
		fieldPath := fmt.Sprintf("%s.display_fields[%d]", path, index)
		if fieldKey == "" {
			state.add(fieldPath, "is required")
		} else if targetExists && fieldKey != "id" && !runtimeRoleFieldExists(state.fields[target.Key], fieldKey) {
			state.add(fieldPath, "unknown or disabled field %q on object %q", fieldKey, targetKey)
		} else if seenFields[fieldKey] {
			state.add(fieldPath, "duplicate display field %q", fieldKey)
		}
		seenFields[fieldKey] = true
	}
}

func (state *validationState) validateRolePolicyExpression(path, rootObjectKey string, expression manifestmodel.RolePolicyExpression, depth int) {
	if depth > 16 {
		state.add(path, "exceeds maximum depth")
		return
	}
	operator := strings.ToLower(strings.TrimSpace(expression.Operator))
	switch operator {
	case "and", "or":
		if len(expression.Children) == 0 {
			state.add(path+".children", "must not be empty for %s", operator)
		}
		for index, child := range expression.Children {
			state.validateRolePolicyExpression(fmt.Sprintf("%s.children[%d]", path, index), rootObjectKey, child, depth+1)
		}
		return
	case "not":
		if len(expression.Children) != 1 {
			state.add(path+".children", "must contain exactly one expression for not")
		}
		for index, child := range expression.Children {
			state.validateRolePolicyExpression(fmt.Sprintf("%s.children[%d]", path, index), rootObjectKey, child, depth+1)
		}
		return
	case "eq", "neq", "in", "not_in", "exists", "prefix":
	case "":
		state.add(path+".operator", "is required")
		return
	default:
		state.add(path+".operator", "is not executable by Runtime: %q", operator)
		return
	}
	currentKey := rootObjectKey
	visited := map[string]bool{currentKey: true}
	for index, segment := range expression.Path {
		segmentPath := fmt.Sprintf("%s.path[%d]", path, index)
		targetKey := strings.TrimSpace(segment.TargetObjectKey)
		target := state.objects[targetKey]
		if target.Key == "" || visited[targetKey] {
			state.add(segmentPath+".target_object_key", "unknown or cyclic object %q", targetKey)
			return
		}
		relationKey := strings.TrimSpace(segment.RelationFieldKey)
		switch strings.TrimSpace(segment.Direction) {
		case "forward":
			relation := state.fields[currentKey][relationKey]
			if !runtimeRoleRelationTargets(relation, targetKey) {
				state.add(segmentPath+".relation_field_key", "invalid forward relation %s.%s -> %s", currentKey, relationKey, targetKey)
				return
			}
		case "reverse":
			relation := state.fields[targetKey][relationKey]
			if !runtimeRoleRelationTargets(relation, currentKey) {
				state.add(segmentPath+".relation_field_key", "invalid reverse relation %s.%s -> %s", targetKey, relationKey, currentKey)
				return
			}
		default:
			state.add(segmentPath+".direction", "must be forward or reverse")
			return
		}
		visited[targetKey] = true
		currentKey = targetKey
	}
	fieldKey := strings.TrimSpace(expression.FieldKey)
	if fieldKey == "" {
		state.add(path+".field_key", "is required")
	} else if fieldKey != "id" && !runtimeRoleFieldExists(state.fields[currentKey], fieldKey) {
		state.add(path+".field_key", "unknown or disabled field %q on object %q", fieldKey, currentKey)
	}
	valueSource := strings.TrimSpace(expression.ValueSource)
	if valueSource != "literal" && valueSource != "actor_claim" {
		state.add(path+".value_source", "must be literal or actor_claim")
	} else if valueSource == "actor_claim" && strings.TrimSpace(expression.ClaimKey) == "" {
		state.add(path+".claim_key", "is required for actor_claim")
	}
}

func runtimeRoleFieldExists(fields map[string]definitionmodel.FieldSchema, key string) bool {
	field, exists := fields[strings.TrimSpace(key)]
	return exists && strings.TrimSpace(field.DisabledAt) == ""
}

func runtimeRoleRelationTargets(field definitionmodel.FieldSchema, target string) bool {
	return strings.TrimSpace(field.Key) != "" && strings.TrimSpace(field.DisabledAt) == "" &&
		strings.TrimSpace(field.Type) == "relation" && strings.TrimSpace(field.Validation.Target) == strings.TrimSpace(target)
}
