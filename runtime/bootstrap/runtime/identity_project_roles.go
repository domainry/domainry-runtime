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
			Key:                  strings.TrimSpace(role.Key),
			Name:                 strings.TrimSpace(role.Name),
			Permissions:          runtimeRolePermissions(role.Permissions),
			RecordScope:          strings.TrimSpace(role.RecordScope),
			DataPermissions:      mustProjectRoleJSON(role.DataPermissions),
			FieldPermissions:     mustProjectRoleJSON(role.FieldPermissions),
			ReferencePermissions: mustProjectRoleJSON(role.ReferencePermissions),
			ExportRules:          mustProjectRoleJSON(role.ExportRules),
			Audience:             strings.TrimSpace(role.Audience),
			RequiredBindingKey:   strings.TrimSpace(role.RequiredBindingKey),
			AssignmentMode:       strings.TrimSpace(role.AssignmentMode),
			RiskLevel:            strings.TrimSpace(role.RiskLevel),
			ConflictRoleKeys:     append([]string(nil), role.ConflictRoleKeys...),
			GrantableRoleKeys:    append([]string(nil), role.GrantableRoleKeys...),
			PermissionSetKeys:    append([]string(nil), role.PermissionSetKeys...),
			PermissionSetGroups:  append([]string(nil), role.PermissionSetGroups...),
			GuardrailKeys:        append([]string(nil), role.GuardrailKeys...),
			Guardrails:           mustProjectRoleJSON(role.Guardrails),
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

func runtimeRolePermissions(source []string) []string {
	result := append([]string(nil), source...)
	seen := make(map[string]bool, len(result))
	for _, permission := range result {
		seen[strings.TrimSpace(permission)] = true
	}
	add := func(values ...string) {
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" && !seen[value] {
				seen[value] = true
				result = append(result, value)
			}
		}
	}
	// Personal Inbox, preference, and delegation operations remain available to
	// every authenticated project role. Notification still scopes these grants
	// to the current Identity subject and workspace at its application boundary.
	add("notification_inbox.read", "notification_inbox.update", "notification_inbox.act",
		"notification_preference.read", "notification_preference.update",
		"notification_delegation.read", "notification_delegation.update", "notification_delegation.delete")
	if seen["workspace.admin"] {
		add("notification_team_mailbox.read", "notification_template.*", "notification_publication.*",
			"notification_delivery_policy.*", "notification_governance.read")
	}
	if seen["notification.template.read"] {
		add("notification_template.read", "notification_publication.read")
	}
	if seen["notification.template.manage"] {
		add("notification_template.draft", "notification_template.disable")
	}
	if seen["notification.template.test"] {
		add("notification_template.preview")
	}
	if seen["notification.template.publish"] {
		add("notification_publication.request", "notification_publication.cancel")
	}
	if seen["notification.template.approve"] {
		add("notification_publication.approve", "notification_publication.reject")
	}
	if seen["notification.policy.read"] {
		add("notification_delivery_policy.read", "notification_governance.read")
	}
	if seen["notification.policy.manage"] {
		add("notification_delivery_policy.update")
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
