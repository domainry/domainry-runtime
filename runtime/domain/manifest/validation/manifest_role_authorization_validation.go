package validation

import (
	"fmt"
	"strings"
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
		}
	}
}
