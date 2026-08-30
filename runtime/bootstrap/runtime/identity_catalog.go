package runtime

import (
	"context"
	"sort"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"
)

func publishRuntimeIdentityCatalog(ctx context.Context, binding identitysdk.Binding, snapshot appschemamodel.ApplicationSchemaSnapshot, workspaceID, applicationKey string, redirectURLs []string) error {
	if binding == nil {
		return nil
	}
	catalog := runtimeIdentityCatalog(snapshot, workspaceID, applicationKey, redirectURLs)
	if err := binding.Catalog().Validate(ctx, catalog); err != nil {
		return err
	}
	_, err := binding.Catalog().Publish(ctx, catalog)
	return err
}

func runtimeIdentityCatalog(snapshot appschemamodel.ApplicationSchemaSnapshot, workspaceID, applicationKey string, redirectURLs []string) identitysdk.AuthorizationCatalog {
	catalog := identitysdk.AuthorizationCatalog{ContractVersion: identitysdk.CatalogVersionV1, Application: identitysdk.ApplicationRef{WorkspaceID: identitysdk.WorkspaceID(strings.TrimSpace(workspaceID)), ApplicationKey: identitysdk.ApplicationKey(applicationKey), RedirectURLs: append([]string(nil), redirectURLs...)}}
	resources := make(map[string]identitysdk.ResourceDefinition, len(snapshot.Objects))
	for _, object := range snapshot.Objects {
		key := strings.TrimSpace(object.Key)
		if key == "" {
			continue
		}
		resource := identitysdk.ResourceDefinition{Key: identitysdk.ResourceType(key), Fields: []string{"id", "created_at", "updated_at"}}
		factSet := map[string]bool{
			"id": true, "owner_id": true, "department_id": true, "department_path": true,
			"team_id": true, "store_id": true, "territory_id": true, "warehouse_id": true,
			"created_at": true, "updated_at": true, "created_by": true, "updated_by": true,
		}
		for _, field := range object.Fields {
			if strings.TrimSpace(field.DisabledAt) != "" || strings.TrimSpace(field.Key) == "" {
				continue
			}
			resource.Fields = append(resource.Fields, field.Key)
			factSet[field.Key] = true
			if target := referenceTarget(field); target != "" {
				reference := identitysdk.ReferenceDefinition{Key: field.Key, TargetResource: identitysdk.ResourceType(target)}
				if target == string(identitysdk.IdentityUserResource) || target == string(identitysdk.IdentityDepartmentResource) {
					reference.TargetAuthority = identitysdk.ReferenceTargetIdentity
				}
				resource.References = append(resource.References, reference)
			}
		}
		for fact := range factSet {
			resource.SupportedFacts = append(resource.SupportedFacts, fact)
		}
		sort.Strings(resource.Fields)
		sort.Strings(resource.SupportedFacts)
		sort.Slice(resource.References, func(left, right int) bool { return resource.References[left].Key < resource.References[right].Key })
		resources[key] = resource
	}
	actions := map[string]identitysdk.ActionDefinition{}
	ensureResource := func(resource string) bool {
		resource = strings.TrimSpace(resource)
		if resource == "" {
			return false
		}
		if _, ok := resources[resource]; !ok {
			// Identity materializes workspace-admin all_records authority as an
			// `id exists` predicate. Permission-only resources (for example
			// workflow.task) are added after the object pass, but still participate
			// in that catalog projection, so they must publish the universal fact.
			resources[resource] = identitysdk.ResourceDefinition{
				Key:            identitysdk.ResourceType(resource),
				SupportedFacts: []string{"id"},
			}
		}
		return true
	}
	addAction := func(resource, action, risk string) {
		resource, action = strings.TrimSpace(resource), strings.TrimSpace(action)
		if !ensureResource(resource) || action == "" {
			return
		}
		key := resource + "\x00" + action
		actions[key] = identitysdk.ActionDefinition{Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Risk: strings.TrimSpace(risk)}
	}
	addPermission := func(permission, risk string) {
		permission = strings.TrimSpace(permission)
		separator := strings.LastIndex(permission, ".")
		if separator <= 0 || separator == len(permission)-1 {
			return
		}
		addAction(permission[:separator], permission[separator+1:], risk)
	}
	resourceKeys := make([]string, 0, len(resources))
	for key := range resources {
		resourceKeys = append(resourceKeys, key)
	}
	sort.Strings(resourceKeys)
	for _, resource := range resourceKeys {
		for _, action := range []string{"create", "read", "update", "delete", "export"} {
			addAction(resource, action, "")
		}
	}
	for _, action := range snapshot.Actions {
		resource, operation := definitionmodel.ActionPermissionSubject(action)
		addAction(resource, operation, action.RiskLevel)
	}
	for _, report := range snapshot.Reports {
		for _, permission := range report.RequiredPermissions {
			addPermission(permission, "")
		}
	}
	for _, entrypoint := range snapshot.EntryPoints {
		for _, permission := range entrypoint.RequiredPermissions {
			addPermission(permission, "")
		}
	}
	for _, entrypoint := range snapshot.AgentEntrypoints {
		for _, permission := range entrypoint.RequiredPermissions {
			addPermission(permission, "")
		}
	}
	for _, extension := range snapshot.IdentityProfileExtensions {
		for _, permission := range extension.RequiredPermissions {
			addPermission(permission, "")
		}
	}
	for _, contract := range surfacemodel.EndpointContracts {
		for _, permission := range contract.RequiredPermissions {
			addPermission(permission, "")
		}
	}
	for _, domain := range capabilityapplication.RuntimeAuthoringCapabilities().Domains {
		for _, capability := range domain.Capabilities {
			for _, permission := range capability.Permissions {
				addPermission(permission, "")
			}
		}
	}
	// Embedded Notification shares Runtime's Identity application. Publish its
	// source-owned authorization vocabulary in the same atomic application
	// catalog; the standalone Notification SaaS publishes the identical entries
	// for its own application/audience.
	notificationFacts := []string{"tenant_id", "workspace_id", "application_key"}
	for _, entry := range []struct {
		resource string
		actions  []string
	}{
		{resource: "notification_event", actions: []string{"publish"}},
		{resource: "notification_inbox", actions: []string{"read", "update", "act"}},
		{resource: "notification_template", actions: []string{"read", "draft", "preview", "disable"}},
		{resource: "notification_publication", actions: []string{"read", "request", "approve", "reject", "cancel"}},
		{resource: "notification_delivery_policy", actions: []string{"read", "update"}},
		{resource: "notification_preference", actions: []string{"read", "update"}},
		{resource: "notification_team_mailbox", actions: []string{"read"}},
		{resource: "notification_delegation", actions: []string{"read", "update", "delete"}},
		{resource: "notification_governance", actions: []string{"read"}},
	} {
		resources[entry.resource] = identitysdk.ResourceDefinition{Key: identitysdk.ResourceType(entry.resource), SupportedFacts: append([]string(nil), notificationFacts...)}
		for _, action := range entry.actions {
			addAction(entry.resource, action, "")
		}
	}
	resourceKeys = resourceKeys[:0]
	for key := range resources {
		resourceKeys = append(resourceKeys, key)
	}
	sort.Strings(resourceKeys)
	for _, key := range resourceKeys {
		catalog.Resources = append(catalog.Resources, resources[key])
	}
	actionKeys := make([]string, 0, len(actions))
	for key := range actions {
		actionKeys = append(actionKeys, key)
	}
	sort.Strings(actionKeys)
	for _, key := range actionKeys {
		catalog.Actions = append(catalog.Actions, actions[key])
	}
	return catalog
}

func referenceTarget(field definitionmodel.FieldSchema) string {
	fieldType := strings.ToLower(strings.TrimSpace(field.Type))
	if !strings.Contains(fieldType, "relation") && !strings.Contains(fieldType, "reference") && !strings.Contains(fieldType, "lookup") {
		return ""
	}
	for _, key := range []string{"target", "target_object", "object_key", "reference_object"} {
		if target, ok := field.Config[key].(string); ok && strings.TrimSpace(target) != "" {
			return strings.TrimSpace(target)
		}
	}
	return strings.TrimSpace(field.Validation.Target)
}
