package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

var runtimeWorkspaceRoleKeys = [...]string{
	identitysdk.WorkspaceBootstrapRoleTenantAdmin,
	identitysdk.WorkspaceBootstrapRoleHeadquartersAdmin,
	identitysdk.WorkspaceBootstrapRoleStoreManager,
	identitysdk.WorkspaceBootstrapRoleStaff,
}

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

// RuntimeProjectRoleCatalog converts the compiler-bound Runtime manifest into
// Identity's deployment-neutral role contract. It is also used before the
// first workspace exists so bootstrap provisioning and ordinary publication
// validate the exact same role definitions.
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

// RuntimeWorkspaceProjectRoleCatalog projects the one canonical Workspace
// role catalog used both before initialization and by the ordinary bound
// Identity publication. The legacy-looking tenant_admin value is only a
// stable role key from the Identity protocol; it does not identify a Tenant.
func RuntimeWorkspaceProjectRoleCatalog(objects []definitionmodel.ObjectSchema, roles []manifestmodel.RoleSchema, workspaceID, applicationKey string) (identitysdk.ProjectRoleCatalog, error) {
	ordered, err := exactRuntimeWorkspaceRoles(roles)
	if err != nil {
		return identitysdk.ProjectRoleCatalog{}, err
	}
	return RuntimeProjectRoleCatalog(objects, ordered, workspaceID, applicationKey), nil
}

// RuntimeWorkspaceBootstrapRoleCatalog is the unbound form of the same exact
// catalog published after the initial Workspace has been committed.
func RuntimeWorkspaceBootstrapRoleCatalog(objects []definitionmodel.ObjectSchema, roles []manifestmodel.RoleSchema, applicationKey string) (identitysdk.ProjectRoleCatalog, error) {
	return RuntimeWorkspaceProjectRoleCatalog(objects, roles, "", applicationKey)
}

func exactRuntimeWorkspaceRoles(roles []manifestmodel.RoleSchema) ([]manifestmodel.RoleSchema, error) {
	allowed := make(map[string]bool, len(runtimeWorkspaceRoleKeys))
	for _, key := range runtimeWorkspaceRoleKeys {
		allowed[key] = true
	}
	byKey := make(map[string]manifestmodel.RoleSchema, len(roles))
	for _, role := range roles {
		key := strings.TrimSpace(role.Key)
		if !allowed[key] {
			return nil, fmt.Errorf("Runtime Workspace role catalog contains unsupported role %q", key)
		}
		if _, duplicate := byKey[key]; duplicate {
			return nil, fmt.Errorf("Runtime Workspace role catalog contains duplicate role %q", key)
		}
		role.Key = key
		byKey[key] = role
	}
	ordered := make([]manifestmodel.RoleSchema, 0, len(runtimeWorkspaceRoleKeys))
	for _, key := range runtimeWorkspaceRoleKeys {
		role, found := byKey[key]
		if !found {
			return nil, fmt.Errorf("Runtime Workspace role catalog is missing required role %q", key)
		}
		ordered = append(ordered, role)
	}
	return ordered, nil
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
