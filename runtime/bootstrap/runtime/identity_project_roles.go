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
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

// publishRuntimeProjectRoles projects application-owned authorization roles
// through Identity's deployment-neutral port. Older/remote bindings may omit
// the optional capability; they continue to own their role provisioning.
func publishRuntimeProjectRoles(ctx context.Context, binding identitysdk.Binding, objects []definitionmodel.ObjectSchema, roles []manifestmodel.RoleSchema, workspaceID, applicationKey string, handlerDescriptors ...runtimeext.HandlerDescriptor) error {
	if binding == nil {
		return nil
	}
	publisher, ok := binding.(identitysdk.ProjectRoleCatalogPublisher)
	if !ok {
		return nil
	}
	catalog, err := RuntimeWorkspaceProjectRoleCatalog(objects, roles, workspaceID, applicationKey, handlerDescriptors...)
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
func RuntimeWorkspaceProjectRoleCatalog(objects []definitionmodel.ObjectSchema, roles []manifestmodel.RoleSchema, workspaceID, applicationKey string, handlerDescriptors ...runtimeext.HandlerDescriptor) (identitysdk.ProjectRoleCatalog, error) {
	_, complete, err := runtimeWorkspaceRoleCatalogRoles(roles)
	if err != nil {
		return identitysdk.ProjectRoleCatalog{}, err
	}
	return runtimeProjectRoleCatalogWithCapabilityClosure(objects, complete, workspaceID, applicationKey, handlerDescriptors)
}

// RuntimeWorkspaceBootstrapRoleCatalog is the unbound Workspace-login subset
// used only while the first Workspace is created. Roles are selected by their
// authoring facts rather than by product-specific keys. Roles omitted from the
// subset are still validated and remain in RuntimeWorkspaceProjectRoleCatalog.
func RuntimeWorkspaceBootstrapRoleCatalog(objects []definitionmodel.ObjectSchema, roles []manifestmodel.RoleSchema, initialWorkspaceAdministratorRole, applicationKey string, handlerDescriptors ...runtimeext.HandlerDescriptor) (identitysdk.ProjectRoleCatalog, error) {
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
	catalog, err := runtimeProjectRoleCatalogWithCapabilityClosure(objects, bootstrap, "", applicationKey, handlerDescriptors)
	if err != nil {
		return identitysdk.ProjectRoleCatalog{}, err
	}
	catalog.InitialWorkspaceAdministratorRoleKey = administratorRole
	return catalog, nil
}

// runtimeProjectRoleCatalogWithCapabilityClosure adds only the non-HTTP,
// Runtime-owned Identity permissions needed by a frozen Handler descriptor.
// Project manifests continue to contain only business Action permissions.
// Each internal permission inherits the source Action grant's exact data scope.
// Multiple grants use only a least upper bound proven by Identity's filter
// semantics; scopes whose union is not representable fail closed.
func runtimeProjectRoleCatalogWithCapabilityClosure(objects []definitionmodel.ObjectSchema, roles []manifestmodel.RoleSchema, workspaceID, applicationKey string, handlerDescriptors []runtimeext.HandlerDescriptor) (identitysdk.ProjectRoleCatalog, error) {
	permissionsByAction, err := runtimeDownstreamCapabilityPermissions(handlerDescriptors)
	if err != nil {
		return identitysdk.ProjectRoleCatalog{}, err
	}
	catalog := identitysdk.ProjectRoleCatalog{
		Application: identitysdk.ApplicationRef{
			WorkspaceID:    identitysdk.WorkspaceID(strings.TrimSpace(workspaceID)),
			ApplicationKey: identitysdk.ApplicationKey(strings.TrimSpace(applicationKey)),
		},
		Objects: mustProjectRoleJSON(objects),
		Roles:   make([]identitysdk.ProjectRoleDefinition, 0, len(roles)),
	}
	for _, role := range roles {
		permissions, permissionErr := runtimeRolePermissionsWithCapabilityClosure(role.Key, role.Permissions, permissionsByAction)
		if permissionErr != nil {
			return identitysdk.ProjectRoleCatalog{}, permissionErr
		}
		definition := runtimeProjectRoleDefinition(role, permissions)
		catalog.Roles = append(catalog.Roles, definition)
	}
	return catalog, nil
}

func runtimeProjectRoleDefinition(role manifestmodel.RoleSchema, permissions []identitysdk.ProjectRolePermission) identitysdk.ProjectRoleDefinition {
	definition := identitysdk.ProjectRoleDefinition{
		Key:                   strings.TrimSpace(role.Key),
		Name:                  strings.TrimSpace(role.Name),
		Permissions:           permissions,
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
	return definition
}

var runtimeManagedDownstreamPermissions = map[string]bool{
	identitysdk.StoreOrganizationDeliveryCreatePermission:  true,
	identitysdk.StoreOrganizationDeliveryRenamePermission:  true,
	identitysdk.StoreOrganizationDeliveryDisablePermission: true,
	identitysdk.StoreOrganizationDeliveryResolvePermission: true,
	identitysdk.StoreOrganizationDeliveryListPermission:    true,
	identitysdk.HandlerDeliveryCreatePermission:            true,
	identitysdk.HandlerDeliveryUpdatePermission:            true,
	identitysdk.HandlerDeliveryDisablePermission:           true,
	identitysdk.HandlerDeliveryResolvePermission:           true,
	identitysdk.WorkspaceIdentityUsageAggregatePermission:  true,
}

func runtimeDownstreamCapabilityPermissions(descriptors []runtimeext.HandlerDescriptor) (map[string][]string, error) {
	result := make(map[string][]string, len(descriptors))
	for _, descriptor := range descriptors {
		if err := descriptor.Validate(); err != nil {
			return nil, fmt.Errorf("Runtime Handler descriptor %q cannot authorize downstream capabilities: %w", strings.TrimSpace(descriptor.ActionKey), err)
		}
		actionKey := strings.TrimSpace(descriptor.ActionKey)
		if _, duplicate := result[actionKey]; duplicate {
			return nil, fmt.Errorf("Runtime Handler descriptor %q is duplicated while compiling downstream capabilities", actionKey)
		}
		permissions := map[string]bool{}
		add := func(permission string) { permissions[permission] = true }
		if descriptor.TargetOrganization != nil {
			switch descriptor.TargetOrganization.Source {
			case runtimeext.TargetOrganizationSourceExplicit:
				add(identitysdk.StoreOrganizationDeliveryResolvePermission)
			case runtimeext.TargetOrganizationSourceExplicitOrSoleAuthorizedStore:
				add(identitysdk.StoreOrganizationDeliveryListPermission)
				add(identitysdk.StoreOrganizationDeliveryResolvePermission)
			case runtimeext.TargetOrganizationSourceProvisionedStore:
				add(identitysdk.StoreOrganizationDeliveryListPermission)
				add(identitysdk.StoreOrganizationDeliveryCreatePermission)
			}
		}
		if descriptor.StoreOrganizationCatalog != nil {
			add(identitysdk.StoreOrganizationDeliveryListPermission)
			add(identitysdk.StoreOrganizationDeliveryResolvePermission)
		}
		if descriptor.StoreOrganizationMutation != nil {
			for _, operation := range descriptor.StoreOrganizationMutation.Operations {
				switch operation {
				case runtimeext.StoreOrganizationMutationRename:
					add(identitysdk.StoreOrganizationDeliveryRenamePermission)
				case runtimeext.StoreOrganizationMutationDisable:
					add(identitysdk.StoreOrganizationDeliveryDisablePermission)
				}
			}
		}
		if descriptor.IdentityHandlerDelivery != nil {
			for _, operation := range descriptor.IdentityHandlerDelivery.Operations {
				switch operation {
				case runtimeext.IdentityHandlerCreate:
					add(identitysdk.HandlerDeliveryCreatePermission)
				case runtimeext.IdentityHandlerUpdate:
					add(identitysdk.HandlerDeliveryUpdatePermission)
				case runtimeext.IdentityHandlerDisable:
					add(identitysdk.HandlerDeliveryDisablePermission)
				case runtimeext.IdentityHandlerResolve:
					add(identitysdk.HandlerDeliveryResolvePermission)
				}
			}
		}
		if descriptor.WorkspaceIdentityUsage != nil {
			add(identitysdk.WorkspaceIdentityUsageAggregatePermission)
		}
		keys := make([]string, 0, len(permissions))
		for permission := range permissions {
			keys = append(keys, permission)
		}
		sort.Strings(keys)
		result[actionKey] = keys
	}
	return result, nil
}

func runtimeRolePermissionsWithCapabilityClosure(roleKey string, source []manifestmodel.RolePermission, permissionsByAction map[string][]string) ([]identitysdk.ProjectRolePermission, error) {
	result := runtimeRolePermissions(source)
	byKey := make(map[string]identitysdk.ProjectRolePermission, len(result))
	for _, permission := range result {
		if runtimeManagedDownstreamPermissions[permission.PermissionKey] {
			return nil, fmt.Errorf("Runtime Workspace role %q declares a Runtime-managed downstream capability permission", strings.TrimSpace(roleKey))
		}
		byKey[permission.PermissionKey] = permission
	}
	for _, businessGrant := range result {
		for _, capabilityPermission := range permissionsByAction[businessGrant.PermissionKey] {
			if existing, found := byKey[capabilityPermission]; found {
				mergedScope, representable := runtimeCapabilityDataScopeJoin(existing.DataScope, businessGrant.DataScope)
				if !representable {
					return nil, fmt.Errorf("Runtime Workspace role %q grants Actions with incompatible data scopes for one downstream capability", strings.TrimSpace(roleKey))
				}
				existing.DataScope = mergedScope
				existing.AuditDenial = existing.AuditDenial || businessGrant.AuditDenial
				byKey[capabilityPermission] = existing
				continue
			}
			byKey[capabilityPermission] = identitysdk.ProjectRolePermission{
				PermissionKey: capabilityPermission,
				DataScope:     businessGrant.DataScope,
				AuditDenial:   businessGrant.AuditDenial,
			}
		}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result = result[:0]
	for _, key := range keys {
		result = append(result, byKey[key])
	}
	return result, nil
}

// runtimeCapabilityDataScopeJoin mirrors Identity's concrete filter semantics:
// all is the top scope, and org is a subset of org_child because Identity's
// OrgScopeIDs tree always includes the principal's OrgID root. Owner and
// target_org are based on different principal facts and have no representable
// union with organization scopes (or each other) in one authored role grant.
func runtimeCapabilityDataScopeJoin(left, right identitysdk.DataScope) (identitysdk.DataScope, bool) {
	if left == right {
		return left, left.Valid()
	}
	if left == identitysdk.DataScopeAll || right == identitysdk.DataScopeAll {
		return identitysdk.DataScopeAll, left.Valid() && right.Valid()
	}
	if left == identitysdk.DataScopeOrg && right == identitysdk.DataScopeOrgChild || left == identitysdk.DataScopeOrgChild && right == identitysdk.DataScopeOrg {
		return identitysdk.DataScopeOrgChild, true
	}
	return "", false
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
