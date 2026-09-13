package openapi

import "github.com/domainry/domainry-runtime/pkg/runtimeext"

func addWorkspaceProvisioningOpenAPIPaths(paths map[string]any, participant runtimeext.WorkspaceBootstrapParticipant) {
	commercialConfiguration := openAPIRequiredObject([]string{
		"plan", "included_user_limit", "max_user_limit", "included_customer_limit", "max_customer_limit",
		"included_store_limit", "max_stores", "contract_date", "billing_day", "billing_contact_name",
		"billing_contact_phone", "billing_contact_email", "billing_contact_address", "billing_contact_notes",
	}, map[string]any{
		"plan":                    map[string]any{"type": "string", "minLength": 1},
		"included_user_limit":     map[string]any{"type": "integer", "minimum": 0},
		"max_user_limit":          map[string]any{"type": "integer", "minimum": 0},
		"included_customer_limit": map[string]any{"type": "integer", "minimum": 0},
		"max_customer_limit":      map[string]any{"type": "integer", "minimum": 0},
		"included_store_limit":    map[string]any{"type": "integer", "minimum": 1},
		"max_stores":              map[string]any{"type": "integer", "minimum": 1},
		"contract_date":           map[string]any{"type": "string", "format": "date"},
		"billing_day":             map[string]any{"type": "integer", "minimum": 1, "maximum": 31},
		"billing_contact_name":    map[string]any{"type": "string"},
		"billing_contact_phone":   map[string]any{"type": "string"},
		"billing_contact_email":   map[string]any{"type": "string"},
		"billing_contact_address": map[string]any{"type": "string"},
		"billing_contact_notes":   map[string]any{"type": "string"},
	})
	commercialConfiguration["additionalProperties"] = false
	required := []string{"request_id", "workspace_code", "workspace_name", "first_store_code", "first_store_name", "admin_login_id", "admin_name", "commercial_configuration"}
	properties := map[string]any{
		"request_id":               map[string]any{"type": "string", "minLength": 1},
		"workspace_code":           map[string]any{"type": "string", "minLength": 2},
		"workspace_name":           map[string]any{"type": "string", "minLength": 1},
		"first_store_code":         map[string]any{"type": "string", "minLength": 2},
		"first_store_name":         map[string]any{"type": "string", "minLength": 1},
		"admin_login_id":           map[string]any{"type": "string", "format": "email"},
		"admin_name":               map[string]any{"type": "string", "minLength": 1},
		"commercial_configuration": commercialConfiguration,
	}
	if participant != nil {
		descriptor := participant.Descriptor()
		inputProperties := make(map[string]any, len(descriptor.InputFields))
		inputRequired := make([]string, 0, len(descriptor.InputFields))
		for _, field := range descriptor.InputFields {
			fieldSchema := map[string]any{"type": string(field.Type)}
			if field.Type == runtimeext.WorkspaceBootstrapInputExactDecimal {
				fieldSchema = map[string]any{
					"type": "string", "pattern": `^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`,
					"x-domainry-exact-decimal": true,
				}
			}
			if field.Default != nil {
				fieldSchema["default"] = field.Default
			}
			if len(field.Enum) != 0 {
				fieldSchema["enum"] = append([]string(nil), field.Enum...)
			}
			if field.Minimum != nil && field.Type != runtimeext.WorkspaceBootstrapInputExactDecimal {
				fieldSchema["minimum"] = *field.Minimum
			}
			if field.Maximum != nil && field.Type != runtimeext.WorkspaceBootstrapInputExactDecimal {
				fieldSchema["maximum"] = *field.Maximum
			}
			if field.Minimum != nil && field.Type == runtimeext.WorkspaceBootstrapInputExactDecimal {
				fieldSchema["x-domainry-minimum"] = *field.Minimum
			}
			if field.Maximum != nil && field.Type == runtimeext.WorkspaceBootstrapInputExactDecimal {
				fieldSchema["x-domainry-maximum"] = *field.Maximum
			}
			if field.MinLength != nil {
				fieldSchema["minLength"] = *field.MinLength
			}
			if field.MaxLength != nil {
				fieldSchema["maxLength"] = *field.MaxLength
			}
			if field.Pattern != "" {
				fieldSchema["pattern"] = field.Pattern
			}
			if field.Format != "" {
				fieldSchema["format"] = field.Format
			}
			inputProperties[field.Key] = fieldSchema
			if field.Required {
				inputRequired = append(inputRequired, field.Key)
			}
		}
		applicationBootstrap := openAPIRequiredObject(inputRequired, inputProperties)
		applicationBootstrap["additionalProperties"] = false
		applicationBootstrap["x-domainry-input-contract-sha256"] = descriptor.InputContractSHA256
		applicationBootstrap["x-domainry-participant-revision"] = descriptor.ParticipantRevision
		properties["application_bootstrap"] = applicationBootstrap
		required = append(required, "application_bootstrap")
	}
	request := openAPIRequiredObject(required, properties)
	request["additionalProperties"] = false
	result := openAPIRequiredObject([]string{"canonical_code", "admin_login_id", "must_change_password", "credential_delivery_status", "replayed"}, map[string]any{
		"canonical_code":             map[string]any{"type": "string"},
		"admin_login_id":             map[string]any{"type": "string"},
		"initial_password":           map[string]any{"type": "string", "writeOnly": true},
		"must_change_password":       map[string]any{"type": "boolean"},
		"credential_delivery_status": map[string]any{"type": "string", "enum": []string{"delivered", "unavailable_reset_required"}},
		"replayed":                   map[string]any{"type": "boolean"},
	})
	workspaceEntry := openAPIWorkspaceAdministrationEntry(commercialConfiguration)
	paths["/workspaces"] = map[string]any{
		"get": openAPIOperation(
			"listWorkspaces", "Workspace Administration", "List the installation Workspace catalog by sealed cursor",
			openAPIAdminSecurity(),
			openAPIQueryParameter("page_size", "Bounded page size (1-100)", map[string]any{"type": "integer", "minimum": 1, "maximum": 100}),
			openAPIQueryParameter("cursor", "Opaque sealed continuation cursor", map[string]any{"type": "string"}),
			openAPIJSONResponse("Workspace catalog page", openAPIWorkspaceAdministrationClosedObject([]string{"items"}, map[string]any{
				"items": map[string]any{"type": "array", "items": workspaceEntry}, "next_cursor": map[string]any{"type": "string"},
			})),
		),
		"post": openAPIOperation(
			"provisionWorkspace", "Workspace Administration", "Atomically provision a Workspace, Identity bootstrap graph, and typed commercial configuration",
			openAPISecurity{Items: []map[string]any{{"BearerAuth": []string{}}, {"WorkspaceProvisionSignature": []string{}}}},
			openAPIJSONRequest(request), openAPIJSONResponse("Workspace provisioning result", result),
		),
	}
	provision := paths["/workspaces"].(map[string]any)["post"].(map[string]any)
	provision["description"] = "Accepts the existing installation administrator session or a host-configured v2 signed request on the same endpoint. The signature binds method, path, Runtime instance, client, timestamp, request_id and exact body bytes. All signature headers are required together; query parameters and mixed Bearer/signature credentials are rejected. The timestamp window is five minutes. The existing durable provisioning receipt handles retries and never replays the initial password."
	for _, header := range []string{"X-Signature-Version", "X-Client-ID", "X-Timestamp", "X-Domainry-Runtime-ID", "Idempotency-Key"} {
		parameters, _ := provision["parameters"].([]map[string]any)
		provision["parameters"] = append(parameters, openAPIHeaderParameter(header, "Required when using WorkspaceProvisionSignature", false))
	}
	lifecycleRequest := openAPIRequiredObject([]string{"expected_revision"}, map[string]any{"expected_revision": map[string]any{"type": "integer", "minimum": 1}})
	lifecycleRequest["additionalProperties"] = false
	lifecycleResult := openAPIWorkspaceAdministrationClosedObject([]string{"workspace", "revoked_sessions", "replayed"}, map[string]any{
		"workspace": workspaceEntry, "revoked_sessions": map[string]any{"type": "integer", "minimum": 0}, "replayed": map[string]any{"type": "boolean"},
	})
	for _, transition := range []struct{ suffix, operationID, summary string }{
		{"suspend", "suspendWorkspace", "Suspend a Workspace by canonical code and atomically revoke its sessions"},
		{"reactivate", "reactivateWorkspace", "Reactivate a suspended Workspace by canonical code"},
	} {
		paths["/workspaces/{workspaceCode}/"+transition.suffix] = map[string]any{
			"post": openAPIOperation(transition.operationID, "Workspace Administration", transition.summary, openAPIAdminSecurity(),
				openAPIPathParameter("workspaceCode", "Canonical Workspace code"), openAPIWorkspaceAdministrationIdempotencyParameter(), openAPIJSONRequest(lifecycleRequest), openAPIJSONResponse("Workspace lifecycle result", lifecycleResult)),
		}
	}
	commercialUpdate := openAPIRequiredObject([]string{"expected_revision", "commercial_configuration"}, map[string]any{
		"expected_revision": map[string]any{"type": "integer", "minimum": 1}, "commercial_configuration": commercialConfiguration,
	})
	commercialUpdate["additionalProperties"] = false
	commercialResult := openAPIWorkspaceAdministrationClosedObject([]string{"workspace", "replayed"}, map[string]any{"workspace": workspaceEntry, "replayed": map[string]any{"type": "boolean"}})
	paths["/workspaces/{workspaceCode}/commercial-configuration"] = map[string]any{
		"put": openAPIOperation("updateWorkspaceCommercialConfiguration", "Workspace Administration", "CAS-update typed Workspace commercial configuration by canonical code",
			openAPIAdminSecurity(), openAPIPathParameter("workspaceCode", "Canonical Workspace code"), openAPIWorkspaceAdministrationIdempotencyParameter(), openAPIJSONRequest(commercialUpdate), openAPIJSONResponse("Updated Workspace", commercialResult)),
	}
}

func openAPIWorkspaceAdministrationClosedObject(required []string, properties map[string]any) map[string]any {
	result := openAPIRequiredObject(required, properties)
	result["additionalProperties"] = false
	return result
}

func openAPIWorkspaceAdministrationIdempotencyParameter() openAPIParameter {
	return openAPIParameter{Value: openAPIHeaderParameter("Idempotency-Key", "Stable logical Workspace administration operation key", true)}
}

func openAPIWorkspaceAdministrationEntry(commercialConfiguration map[string]any) map[string]any {
	commercial := cloneOpenAPIValue(commercialConfiguration).(map[string]any)
	commercial["additionalProperties"] = false
	entry := openAPIRequiredObject([]string{"canonical_code", "display_name", "status", "revision", "commercial_configuration"}, map[string]any{
		"canonical_code": map[string]any{"type": "string"}, "display_name": map[string]any{"type": "string"},
		"status": map[string]any{"type": "string", "enum": []string{"active", "suspended"}}, "revision": map[string]any{"type": "integer", "minimum": 1},
		"commercial_configuration": commercial,
	})
	entry["additionalProperties"] = false
	return entry
}
