package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

// publishRuntimeProjectRoles projects application-owned authorization roles
// through Identity's deployment-neutral port. Older/remote bindings may omit
// the optional capability; they continue to own their role provisioning.
func publishRuntimeProjectRoles(ctx context.Context, binding identitysdk.Binding, roles []manifestmodel.RoleSchema, workspaceID, applicationKey string) error {
	if binding == nil {
		return nil
	}
	publisher, ok := binding.(identitysdk.ProjectRoleCatalogPublisher)
	if !ok {
		return nil
	}
	_, err := publisher.PublishProjectRoles(ctx, runtimeProjectRoleCatalog(roles, workspaceID, applicationKey))
	return err
}

func runtimeProjectRoleCatalog(roles []manifestmodel.RoleSchema, workspaceID, applicationKey string) identitysdk.ProjectRoleCatalog {
	catalog := identitysdk.ProjectRoleCatalog{
		Application: identitysdk.ApplicationRef{
			WorkspaceID:    identitysdk.WorkspaceID(strings.TrimSpace(workspaceID)),
			ApplicationKey: identitysdk.ApplicationKey(strings.TrimSpace(applicationKey)),
		},
		Roles: make([]identitysdk.ProjectRoleDefinition, 0, len(roles)),
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
