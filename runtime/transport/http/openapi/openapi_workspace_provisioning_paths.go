package openapi

func addWorkspaceProvisioningOpenAPIPaths(paths map[string]any) {
	request := openAPIRequiredObject([]string{"request_id", "tenant_code", "tenant_name", "admin_login_id", "admin_name", "store_configuration"}, map[string]any{
		"request_id":          map[string]any{"type": "string", "minLength": 1},
		"tenant_code":         map[string]any{"type": "string", "minLength": 2},
		"tenant_name":         map[string]any{"type": "string", "minLength": 1},
		"admin_login_id":      map[string]any{"type": "string", "format": "email"},
		"admin_name":          map[string]any{"type": "string", "minLength": 1},
		"store_configuration": openAPIObject(nil),
	})
	result := openAPIRequiredObject([]string{"tenant_registry_id", "workspace_id", "canonical_code", "admin_login_id", "must_change_password", "replayed"}, map[string]any{
		"tenant_registry_id":         map[string]any{"type": "string"},
		"workspace_id":               map[string]any{"type": "string"},
		"canonical_code":             map[string]any{"type": "string"},
		"admin_login_id":             map[string]any{"type": "string"},
		"initial_password":           map[string]any{"type": "string", "writeOnly": true},
		"must_change_password":       map[string]any{"type": "boolean"},
		"application_projection_ids": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
		"replayed":                   map[string]any{"type": "boolean"},
	})
	paths["/tenant-admin/workspaces/provision"] = map[string]any{
		"post": openAPIOperation(
			"provisionWorkspace", "Workspace Administration", "Atomically provision a tenant workspace, administrator identity, configuration, and application projections",
			openAPIAdminSecurity(), openAPIJSONRequest(request), openAPIJSONResponse("Workspace provisioning result", result),
		),
	}
	paths["/tenant-admin/workspaces/{workspaceID}/roles/reconcile"] = map[string]any{
		"post": openAPIOperation(
			"reconcileWorkspaceRoles", "Workspace Administration", "Reconcile application-declared tenant login roles inside one host transaction",
			openAPIAdminSecurity(), openAPIPathParameter("workspaceID", "Managed workspace ID"),
			openAPIJSONResponse("Workspace role reconciliation result", openAPIRequiredObject([]string{"workspace_id", "provisioned_roles"}, map[string]any{
				"workspace_id": map[string]any{"type": "string"}, "provisioned_roles": map[string]any{"type": "integer"},
			})),
		),
	}
}
