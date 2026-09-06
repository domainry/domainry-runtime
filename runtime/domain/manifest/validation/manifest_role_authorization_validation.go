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
		if audience != "any" && audience != "user" && audience != "business_profile" && audience != "service" {
			state.add(path+".audience", "must be one of any, user, business_profile, service")
		}
		assignmentMode := strings.TrimSpace(role.AssignmentMode)
		if assignmentMode == "" {
			assignmentMode = "manual"
		}
		if assignmentMode != "manual" && assignmentMode != "request_only" && assignmentMode != "system_managed" {
			state.add(path+".assignment_mode", "must be one of manual, request_only, system_managed")
		}
		if audience == "service" && assignmentMode != "system_managed" {
			state.add(path+".assignment_mode", "service roles must use system_managed")
		}
		if role.ProvisionToWorkspaces && audience != "any" && audience != "user" && audience != "business_profile" {
			state.add(path+".provision_to_workspaces", "Workspace login roles must use any, user, or business_profile audience")
		}
		if role.ProvisionToWorkspaces && assignmentMode == "system_managed" {
			state.add(path+".provision_to_workspaces", "system-managed roles cannot be provisioned as tenant login roles")
		}
		permissions := map[string]bool{}
		for permissionIndex, grant := range role.Permissions {
			permission := strings.TrimSpace(grant.PermissionKey)
			permissionPath := fmt.Sprintf("%s.permissions[%d]", path, permissionIndex)
			if permission == "" {
				state.add(permissionPath+".permission_key", "is required")
			} else if permissions[permission] {
				state.add(permissionPath+".permission_key", "duplicate permission %q", permission)
			}
			permissions[permission] = true
			if !grant.DataScope.Valid() {
				state.add(permissionPath+".data_scope", "must be one of all, owner, org, org_child, target_org")
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
	state.validateInitialWorkspaceAdministratorRole()
}

func (state *validationState) validateInitialWorkspaceAdministratorRole() {
	key := strings.TrimSpace(state.manifest.InitialWorkspaceAdministratorRole)
	if key == "" {
		for _, role := range state.manifest.Roles {
			if role.ProvisionToWorkspaces {
				state.add("initial_workspace_administrator_role", "is required when Workspace login roles are declared")
				return
			}
		}
		return
	}
	for _, role := range state.manifest.Roles {
		if strings.TrimSpace(role.Key) != key {
			continue
		}
		audience := strings.TrimSpace(role.Audience)
		if audience == "" {
			audience = "any"
		}
		assignmentMode := strings.TrimSpace(role.AssignmentMode)
		if assignmentMode == "" {
			assignmentMode = "manual"
		}
		if !role.ProvisionToWorkspaces || (audience != "any" && audience != "user") || assignmentMode != "manual" || strings.TrimSpace(role.RequiredBindingKey) != "" {
			state.add("initial_workspace_administrator_role", "must reference a provisioned any/user manual role without required_binding_key")
		}
		return
	}
	state.add("initial_workspace_administrator_role", "references unknown role %q", key)
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

func runtimeRoleFieldExists(fields map[string]definitionmodel.FieldSchema, key string) bool {
	field, exists := fields[strings.TrimSpace(key)]
	return exists && strings.TrimSpace(field.DisabledAt) == ""
}
