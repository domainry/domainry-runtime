package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

// publishRuntimeProjectRoles projects application-owned authorization roles
// through Identity's deployment-neutral port. Older/remote bindings may omit
// the optional capability; they continue to own their role provisioning.
func publishRuntimeProjectRoles(ctx context.Context, binding identitysdk.Binding, objects []definitionmodel.ObjectSchema, roles []manifestmodel.RoleSchema, workspaceID, applicationKey string) error {
	if binding == nil {
		return nil
	}
	publisher, ok := binding.(identitysdk.ProjectRoleCatalogPublisher)
	if !ok {
		return nil
	}
	catalog, err := RuntimeWorkspaceProjectRoleCatalog(objects, roles, workspaceID, applicationKey)
	if err != nil {
		return err
	}
	_, err = publisher.PublishProjectRoles(ctx, catalog)
	return err
}

// RuntimeProjectRoleCatalog converts Runtime-owned role metadata into
// Identity's deployment-neutral role contract. Callers that publish a Runtime
// manifest must first apply the Workspace role policy through
// RuntimeWorkspaceProjectRoleCatalog or RuntimeWorkspaceBootstrapRoleCatalog.
func RuntimeProjectRoleCatalog(objects []definitionmodel.ObjectSchema, roles []manifestmodel.RoleSchema, workspaceID, applicationKey string) identitysdk.ProjectRoleCatalog {
	catalog := identitysdk.ProjectRoleCatalog{
		Application: identitysdk.ApplicationRef{
			WorkspaceID:    identitysdk.WorkspaceID(strings.TrimSpace(workspaceID)),
			ApplicationKey: identitysdk.ApplicationKey(strings.TrimSpace(applicationKey)),
		},
		Objects: mustProjectRoleJSON(objects),
		Roles:   make([]identitysdk.ProjectRoleDefinition, 0, len(roles)),
	}
	for _, role := range roles {
		definition := identitysdk.ProjectRoleDefinition{
			Key:                   strings.TrimSpace(role.Key),
			Name:                  strings.TrimSpace(role.Name),
			Permissions:           runtimeRolePermissions(role.Permissions),
			FieldPermissions:      mustProjectRoleJSON(role.FieldPermissions),
			ReferencePermissions:  mustProjectRoleJSON(role.ReferencePermissions),
			ExportRules:           mustProjectRoleJSON(role.ExportRules),
			Audience:              strings.TrimSpace(role.Audience),
			RequiredBindingKey:    strings.TrimSpace(role.RequiredBindingKey),
			AssignmentMode:        strings.TrimSpace(role.AssignmentMode),
			RiskLevel:             strings.TrimSpace(role.RiskLevel),
			ConflictRoleKeys:      append([]string(nil), role.ConflictRoleKeys...),
			GrantableRoleKeys:     append([]string(nil), role.GrantableRoleKeys...),
			PermissionSetKeys:     append([]string(nil), role.PermissionSetKeys...),
			PermissionSetGroups:   append([]string(nil), role.PermissionSetGroups...),
			GuardrailKeys:         append([]string(nil), role.GuardrailKeys...),
			Guardrails:            mustProjectRoleJSON(role.Guardrails),
			ProvisionToWorkspaces: role.ProvisionToWorkspaces,
		}
		payload, err := json.Marshal(definition)
		if err != nil {
			panic(err) // RoleSchema consists exclusively of JSON-safe values.
		}
		hash := sha256.Sum256(payload)
		definition.SchemaHash = hex.EncodeToString(hash[:])
		catalog.Roles = append(catalog.Roles, definition)
	}
	return catalog
}

// RuntimeWorkspaceProjectRoleCatalog projects the complete project role
// catalog for ordinary workspace-bound Identity publication. Workspace-login
// and internal roles are validated together; internal roles remain in this
// catalog so service subjects can be authorized.
func RuntimeWorkspaceProjectRoleCatalog(objects []definitionmodel.ObjectSchema, roles []manifestmodel.RoleSchema, workspaceID, applicationKey string) (identitysdk.ProjectRoleCatalog, error) {
	_, complete, err := runtimeWorkspaceRoleCatalogRoles(roles)
	if err != nil {
		return identitysdk.ProjectRoleCatalog{}, err
	}
	return RuntimeProjectRoleCatalog(objects, complete, workspaceID, applicationKey), nil
}

// RuntimeWorkspaceBootstrapRoleCatalog is the unbound Workspace-login subset
// used only while the first Workspace is created. Roles are selected by their
// authoring facts rather than by product-specific keys. Roles omitted from the
// subset are still validated and remain in RuntimeWorkspaceProjectRoleCatalog.
func RuntimeWorkspaceBootstrapRoleCatalog(objects []definitionmodel.ObjectSchema, roles []manifestmodel.RoleSchema, initialWorkspaceAdministratorRole, applicationKey string) (identitysdk.ProjectRoleCatalog, error) {
	bootstrap, _, err := runtimeWorkspaceRoleCatalogRoles(roles)
	if err != nil {
		return identitysdk.ProjectRoleCatalog{}, err
	}
	if len(bootstrap) == 0 {
		return identitysdk.ProjectRoleCatalog{}, fmt.Errorf("Runtime Workspace role catalog contains no provisioned human login role")
	}
	administratorRole, err := runtimeInitialWorkspaceAdministratorRole(roles, initialWorkspaceAdministratorRole)
	if err != nil {
		return identitysdk.ProjectRoleCatalog{}, err
	}
	catalog := RuntimeProjectRoleCatalog(objects, bootstrap, "", applicationKey)
	catalog.InitialWorkspaceAdministratorRoleKey = administratorRole
	return catalog, nil
}

func runtimeInitialWorkspaceAdministratorRole(roles []manifestmodel.RoleSchema, requested string) (string, error) {
	key := strings.TrimSpace(requested)
	if key == "" {
		return "", fmt.Errorf("Runtime manifest initial_workspace_administrator_role is required")
	}
	for _, role := range roles {
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
			return "", fmt.Errorf("Runtime manifest initial_workspace_administrator_role %q must reference a provisioned any/user manual role without required_binding_key", key)
		}
		return key, nil
	}
	return "", fmt.Errorf("Runtime manifest initial_workspace_administrator_role %q references an unknown role", key)
}

func runtimeWorkspaceRoleCatalogRoles(roles []manifestmodel.RoleSchema) ([]manifestmodel.RoleSchema, []manifestmodel.RoleSchema, error) {
	seen := make(map[string]bool, len(roles))
	bootstrap := make([]manifestmodel.RoleSchema, 0, len(roles))
	complete := make([]manifestmodel.RoleSchema, 0, len(roles))
	for _, role := range roles {
		key := strings.TrimSpace(role.Key)
		if key == "" {
			return nil, nil, fmt.Errorf("Runtime Workspace role catalog contains a role with an empty key")
		}
		if seen[key] {
			return nil, nil, fmt.Errorf("Runtime Workspace role catalog contains duplicate role %q", key)
		}
		seen[key] = true
		role.Key = key
		if strings.TrimSpace(role.Name) == "" {
			return nil, nil, fmt.Errorf("Runtime Workspace role %q has an empty name", key)
		}

		audience := strings.TrimSpace(role.Audience)
		if audience == "" {
			audience = "any"
		}
		switch audience {
		case "any", "user", "business_profile", "service":
		default:
			return nil, nil, fmt.Errorf("Runtime Workspace role %q has unsupported audience %q", key, audience)
		}
		assignmentMode := strings.TrimSpace(role.AssignmentMode)
		if assignmentMode == "" {
			assignmentMode = "manual"
		}
		switch assignmentMode {
		case "manual", "request_only", "system_managed":
		default:
			return nil, nil, fmt.Errorf("Runtime Workspace role %q has unsupported assignment_mode %q", key, assignmentMode)
		}
		if audience == "service" && assignmentMode != "system_managed" {
			return nil, nil, fmt.Errorf("Runtime Workspace service role %q must use assignment_mode system_managed", key)
		}
		if role.ProvisionToWorkspaces {
			if audience == "service" {
				return nil, nil, fmt.Errorf("Runtime Workspace service role %q cannot set provision_to_workspaces true", key)
			}
			if assignmentMode == "system_managed" {
				return nil, nil, fmt.Errorf("Runtime Workspace system-managed role %q cannot set provision_to_workspaces true", key)
			}
			bootstrap = append(bootstrap, role)
		}
		complete = append(complete, role)
	}
	sort.Slice(bootstrap, func(left, right int) bool { return bootstrap[left].Key < bootstrap[right].Key })
	sort.Slice(complete, func(left, right int) bool { return complete[left].Key < complete[right].Key })
	return bootstrap, complete, nil
}

func runtimeRolePermissions(source []manifestmodel.RolePermission) []identitysdk.ProjectRolePermission {
	result := make([]identitysdk.ProjectRolePermission, 0, len(source))
	seen := make(map[string]bool, len(source))
	for _, permission := range source {
		permission.PermissionKey = strings.TrimSpace(permission.PermissionKey)
		if permission.PermissionKey != "" && permission.DataScope.Valid() && !seen[permission.PermissionKey] {
			seen[permission.PermissionKey] = true
			result = append(result, identitysdk.ProjectRolePermission{PermissionKey: permission.PermissionKey, DataScope: permission.DataScope, AuditDenial: permission.AuditDenial})
		}
	}
	return result
}

func mustProjectRoleJSON(value any) json.RawMessage {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err) // Manifest policy structs contain no unsupported JSON values.
	}
	return payload
}
