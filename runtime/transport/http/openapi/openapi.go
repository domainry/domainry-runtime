package openapi

import appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

import (
	"fmt"
	"regexp"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	"github.com/domainry/domainry-runtime/runtime/platform/productbrand"
)

func Build(snapshot appschemamodel.ApplicationSchemaSnapshot) map[string]any {
	return BuildWithProductBrand(snapshot, productbrand.DefaultName)
}

func BuildWithProductBrand(snapshot appschemamodel.ApplicationSchemaSnapshot, productBrandName string) map[string]any {
	return BuildWithModuleHTTPSurfaces(snapshot, productBrandName, nil)
}

func BuildWithModuleHTTPSurfaces(snapshot appschemamodel.ApplicationSchemaSnapshot, productBrandName string, surfaces []modulehttp.Surface) map[string]any {
	productBrandName = productbrand.ResolveName(productBrandName)
	paths := map[string]any{}
	components := map[string]any{
		"securitySchemes": map[string]any{
			"BearerAuth": map[string]any{
				"type":         "http",
				"scheme":       "bearer",
				"bearerFormat": "JWT",
			},
			"IntegrationAPIKey": map[string]any{
				"type": "apiKey",
				"in":   "header",
				"name": "X-API-Key",
			},
			"ServiceCredential": map[string]any{
				"type": "apiKey",
				"in":   "header",
				"name": "X-Domainry-Service-Credential",
			},
		},
		"schemas": openAPISchemas(snapshot),
	}
	paths["/health"] = map[string]any{
		"get": openAPIOperation("getHealth", "Health", "Controlled Runtime diagnostic snapshot", openAPIAdminSecurity(), openAPIJSONResponse("Health payload", openAPIObject(nil))),
	}
	for path, operation := range map[string]string{"/live": "getLiveness", "/ready": "getReadiness", "/startup": "getStartup"} {
		paths[path] = map[string]any{
			"get": openAPIOperation(operation, "Health", "Orchestrator probe", openAPIProtocolAudience("anonymous"), openAPIPublicSecurity(), openAPIJSONResponse("Probe snapshot", openAPIObject(nil))),
		}
	}
	paths["/operations/idempotency/receipts"] = map[string]any{
		"get": openAPIOperation("listIdempotencyReceipts", "Operations", "List sanitized idempotency receipts", openAPIAdminSecurity(), openAPIJSONResponse("Receipt list", openAPIObject(nil))),
	}
	paths["/operations"] = map[string]any{
		"get":  openAPIOperation("listRuntimeOperations", "Operations", "List workspace-scoped durable operation receipts", openAPIAdminSecurity(), openAPIJSONResponse("Operation receipts", openAPIObject(nil))),
		"post": openAPIOperation("submitRuntimeOperation", "Operations", "Register an authorized durable operation command", openAPIAdminSecurity(), openAPIJSONRequest(openAPIRequiredObject([]string{"kind", "permission", "resource_type", "reason"}, map[string]any{"kind": map[string]any{"type": "string"}, "permission": map[string]any{"type": "string"}, "resource_type": map[string]any{"type": "string"}, "resource_id": map[string]any{"type": "string"}, "reason": map[string]any{"type": "string"}, "reference": map[string]any{"type": "string"}, "payload": openAPIObject(nil)})), openAPIJSONResponse("Durable operation receipt", openAPIObject(nil))),
	}
	paths["/operations/{operationID}"] = map[string]any{
		"get": openAPIOperation("getRuntimeOperation", "Operations", "Get a workspace-scoped durable operation receipt", openAPIAdminSecurity(), openAPIPathParameter("operationID", "Operation ID"), openAPIJSONResponse("Durable operation receipt", openAPIObject(nil))),
	}
	paths["/operations/catalog"] = map[string]any{
		"get": openAPIOperation("listRuntimeOperationDefinitions", "Operations", "List registered operation permission, scope, precondition, idempotency, audit, and receipt contracts", openAPIAdminSecurity(), openAPIJSONResponse("Operation definitions", openAPIObject(nil))),
	}
	paths["/operations/controls"] = map[string]any{
		"get": openAPIOperation("listRuntimeOperationControls", "Operations", "List durable maintenance, owner-pause, and instance-drain desired state", openAPIAdminSecurity(), openAPIJSONResponse("Operation controls", openAPIObject(nil))),
	}
	paths["/operations/controls/{controlKind}/{owner}"] = map[string]any{
		"put": openAPIOperation("setRuntimeOperationControl", "Operations", "Apply a revision-fenced durable control and return its operation receipt", openAPIAdminSecurity(), openAPIPathParameter("controlKind", "maintenance, worker_pause, or instance_drain"), openAPIPathParameter("owner", "runtime, worker owner, or stable Runtime instance ID"), openAPIJSONRequest(openAPIRequiredObject([]string{"active", "reason", "expected_revision"}, map[string]any{"active": map[string]any{"type": "boolean"}, "reason": map[string]any{"type": "string"}, "reference": map[string]any{"type": "string"}, "expected_revision": map[string]any{"type": "integer", "format": "int64"}})), openAPIJSONResponse("Applied control and durable receipt", openAPIObject(nil))),
	}
	paths["/operations/leases/{owner}/{resourceID}/force-release"] = map[string]any{
		"post": openAPIOperation("forceReleaseRuntimeLease", "Operations", "Release only an expired or independently verified stuck registered-owner lease and increment fencing", openAPIAdminSecurity(), openAPIPathParameter("owner", "Registered lease owner"), openAPIPathParameter("resourceID", "Owner resource ID"), openAPIJSONRequest(openAPIRequiredObject([]string{"expected_lease_owner", "expected_fencing_token", "reason"}, map[string]any{"expected_lease_owner": map[string]any{"type": "string"}, "expected_fencing_token": map[string]any{"type": "integer", "format": "int64"}, "verified_stuck": map[string]any{"type": "boolean"}, "verification_evidence": map[string]any{"type": "string"}, "reason": map[string]any{"type": "string"}, "reference": map[string]any{"type": "string"}})), openAPIJSONResponse("Fenced lease release receipt", openAPIObject(nil))),
	}
	paths["/operations/dead-letters/{owner}/{deadLetterID}"] = map[string]any{
		"get": openAPIOperation("inspectRuntimeDeadLetter", "Operations", "Inspect a redacted owner-controlled dead-letter item", openAPIAdminSecurity(), openAPIPathParameter("owner", "Registered dead-letter owner"), openAPIPathParameter("deadLetterID", "Owner dead-letter identity"), openAPIJSONResponse("Dead-letter item", openAPIObject(nil))),
	}
	paths["/operations/dead-letters/{owner}/{deadLetterID}/{action}"] = map[string]any{
		"post": openAPIOperation("actOnRuntimeDeadLetter", "Operations", "Resolve, retry, or acknowledge through the registered owner and return a durable receipt", openAPIAdminSecurity(), openAPIPathParameter("owner", "Registered dead-letter owner"), openAPIPathParameter("deadLetterID", "Owner dead-letter identity"), openAPIPathParameter("action", "resolve, retry, or ack"), openAPIJSONRequest(openAPIRequiredObject([]string{"reason"}, map[string]any{"reason": map[string]any{"type": "string"}, "reference": map[string]any{"type": "string"}})), openAPIJSONResponse("Owner result and durable operation receipt", openAPIObject(nil))),
	}
	paths["/operations/bulk/dead-letters/dry-run"] = map[string]any{
		"post": openAPIOperation("dryRunBulkDeadLetters", "Operations", "Resolve a bounded explicit-ID filter into per-item eligibility and a short-lived confirmation token", openAPIAdminSecurity(), openAPIJSONRequest(openAPIRequiredObject([]string{"owner", "action", "filter", "limit", "reason"}, map[string]any{"owner": map[string]any{"type": "string"}, "action": map[string]any{"type": "string", "enum": []string{"resolve", "retry", "ack"}}, "filter": openAPIObject(map[string]any{"ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "status": map[string]any{"type": "string"}}), "limit": map[string]any{"type": "integer", "maximum": 100}, "reason": map[string]any{"type": "string"}, "reference": map[string]any{"type": "string"}})), openAPIJSONResponse("Bounded bulk dry-run plan", openAPIObject(nil))),
	}
	paths["/operations/bulk/dead-letters/apply"] = map[string]any{
		"post": openAPIOperation("applyBulkDeadLetters", "Operations", "Apply the exact unexpired dry-run candidate set and return every item outcome", openAPIAdminSecurity(), openAPIJSONRequest(openAPIRequiredObject([]string{"dry_run_operation_id", "confirmation_token", "confirm", "reason"}, map[string]any{"dry_run_operation_id": map[string]any{"type": "string"}, "confirmation_token": map[string]any{"type": "string"}, "confirm": map[string]any{"type": "boolean"}, "reason": map[string]any{"type": "string"}, "reference": map[string]any{"type": "string"}})), openAPIJSONResponse("Per-item bulk result and durable receipt", openAPIObject(nil))),
	}
	paths["/operations/diagnostics/snapshots"] = map[string]any{
		"post": openAPIOperation("captureRuntimeDiagnostics", "Operations", "Capture a redacted paginated bounded-cost snapshot from registered diagnostic sections", openAPIAdminSecurity(), openAPIJSONRequest(openAPIRequiredObject([]string{"sections", "reason"}, map[string]any{"sections": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"schema_migration", "db_pool", "worker_lease", "queue_lag", "dlq", "backup_age"}}}, "page": map[string]any{"type": "integer"}, "page_size": map[string]any{"type": "integer", "maximum": 50}, "reason": map[string]any{"type": "string"}, "reference": map[string]any{"type": "string"}})), openAPIJSONResponse("Diagnostic snapshot and durable receipt", openAPIObject(nil))),
	}
	paths["/operations/runbooks/{category}"] = map[string]any{
		"get": openAPIOperation("getRuntimeOperationsRunbook", "Operations", "Resolve a stable Operations error code to machine-readable next actions", openAPIAdminSecurity(), openAPIPathParameter("category", "Runbook category"), openAPIJSONResponse("Runbook link", openAPIObject(nil))),
	}
	paths["/operations/break-glass"] = map[string]any{
		"get":  openAPIOperation("listRuntimeBreakGlass", "Operations", "List workspace-isolated time-limited break-glass grants", openAPIAdminSecurity(), openAPIJSONResponse("Break-glass grants", openAPIObject(nil))),
		"post": openAPIOperation("enableRuntimeBreakGlass", "Operations", "Enable a maximum one-hour grant with two independent approvers, durable audit and alert target", openAPIAdminSecurity(), openAPIJSONRequest(openAPIRequiredObject([]string{"duration_seconds", "approver_ids", "reason", "incident_ref", "alert_target"}, map[string]any{"duration_seconds": map[string]any{"type": "integer", "maximum": 3600}, "approver_ids": map[string]any{"type": "array", "minItems": 2, "items": map[string]any{"type": "string"}}, "reason": map[string]any{"type": "string"}, "incident_ref": map[string]any{"type": "string"}, "alert_target": map[string]any{"type": "string"}})), openAPIJSONResponse("Break-glass grant and durable receipt", openAPIObject(nil))),
	}
	paths["/operations/break-glass/{grantID}/disable"] = map[string]any{
		"post": openAPIOperation("disableRuntimeBreakGlass", "Operations", "Revision-fenced revocation with audit alert", openAPIAdminSecurity(), openAPIPathParameter("grantID", "Break-glass grant ID"), openAPIJSONRequest(openAPIRequiredObject([]string{"expected_revision", "reason", "incident_ref"}, map[string]any{"expected_revision": map[string]any{"type": "integer", "format": "int64"}, "reason": map[string]any{"type": "string"}, "incident_ref": map[string]any{"type": "string"}})), openAPIJSONResponse("Revoked grant and durable receipt", openAPIObject(nil))),
	}
	addOperationsControlOpenAPIPaths(paths)
	for _, operation := range []string{"retry", "reset"} {
		path := "/operations/idempotency/receipts/{owner}/{receiptID}/" + operation
		paths[path] = map[string]any{"post": openAPIOperation(operation+"IdempotencyReceipt", "Operations", "Safely "+operation+" an eligible idempotency receipt", openAPIAdminSecurity(), openAPIJSONResponse("Operation result", openAPIObject(nil)))}
	}
	paths["/tenant-admin/metadata/manifests/current"] = map[string]any{"get": openAPIOperation("getCurrentRuntimeManifest", "Provision", "Authenticated read of the installed Runtime-native manifest", openAPIAdminSecurity(), openAPIJSONResponse("Installed manifest", openAPIObject(nil)))}
	paths["/permissions/effective"] = map[string]any{
		"get": openAPIOperation("getEffectivePermissions", "Permissions", "Effective generated-app principal permissions; optional object_key and record_id apply the same record RLS decision used by Action execution", openAPIAdminSecurity(), openAPIJSONResponse("Effective permissions", openAPIObject(nil))),
	}
	paths["/openapi.json"] = map[string]any{
		"get": openAPIOperation("getOpenAPI", "OpenAPI", "Authenticated generated OpenAPI document", openAPIAdminSecurity(), openAPIJSONResponse("OpenAPI document", openAPIObject(nil))),
	}
	paths["/events/business"] = map[string]any{
		"get": openAPIOperation(
			"subscribeBusinessEvents", "Events", "Subscribe to tenant-scoped refresh signals; refetch durable state through authorized Runtime APIs",
			openAPIAdminSecurity(),
			openAPIParameter{Value: map[string]any{"name": "objects", "in": "query", "required": false, "description": "Comma-separated object keys, maximum 32", "schema": map[string]any{"type": "string"}}},
			openAPIParameter{Value: map[string]any{"name": "types", "in": "query", "required": false, "description": "Comma-separated event types; currently refresh", "schema": map[string]any{"type": "string"}}},
			openAPIParameter{Value: map[string]any{"name": "Last-Event-ID", "in": "header", "required": false, "description": "Opaque cursor from the last processed event", "schema": map[string]any{"type": "string", "maxLength": 256}}},
			openAPIResponse("SSE refresh/resync stream", "text/event-stream", map[string]any{"type": "string"}),
		),
	}
	addRuntimeContractOpenAPIPaths(paths)
	addLifecycleOpenAPIPaths(paths)
	addBusinessBuilderOpenAPIPaths(paths)
	addAdminListenerOpenAPIPaths(paths)
	for _, object := range snapshot.Objects {
		addObjectOpenAPIPaths(paths, object)
	}
	for _, action := range snapshot.Actions {
		addActionOpenAPIPath(paths, action)
	}
	addPublicationHandoffOpenAPIPaths(paths)
	paths["/v1/scheduler-triggers:accept"] = map[string]any{"post": openAPIOperation("acceptSchedulerTrigger", "Scheduler Dispatch Gateway", "Identity-authenticated execution callback for one Scheduler-owned run", openAPIProtocolAudience("scheduler_service_service"), openAPIServiceCredentialSecurity(), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Stable downstream receipt", openAPIObject(nil)))}
	addOwnerOperationsReceiptOpenAPIContracts(paths)
	applyCompiledEndpointContracts(paths)
	annotateStaticModuleOwnerFallbacks(paths)
	// Capability-owned OpenAPI and governance are authoritative for mounted
	// module routes. Static Runtime contracts remain only for Runtime-owned
	// endpoints and the temporary fallback used before a module is bound.
	annotateModuleOwnedOpenAPIPaths(paths, surfaces)
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "Generated " + valueOrDefault(snapshot.TemplateID, productBrandName) + " API",
			"version": valueOrDefault(snapshot.TemplateVersion, "0.1.0"),
		},
		"servers": []map[string]string{{"url": "/"}},
		"security": []map[string]any{
			{"BearerAuth": []string{}},
			{"IntegrationAPIKey": []string{}},
		},
		"paths":      paths,
		"components": components,
	}
}

func annotateModuleOwnedOpenAPIPaths(paths map[string]any, surfaces []modulehttp.Surface) {
	for _, surface := range surfaces {
		if surface == nil {
			continue
		}
		owner, surfaceName := strings.TrimSpace(surface.Owner()), strings.TrimSpace(surface.Name())
		for _, route := range surface.Routes() {
			pattern := strings.TrimSpace(route.Pattern())
			method, path, found := strings.Cut(pattern, " ")
			if !found || owner == "" || !isOpenAPIHTTPMethod(method) {
				continue
			}
			method, path = strings.ToLower(strings.TrimSpace(method)), strings.TrimSpace(path)
			pathSpec, _ := paths[path].(map[string]any)
			if pathSpec == nil {
				pathSpec = map[string]any{}
				paths[path] = pathSpec
			}
			operation, _ := pathSpec[method].(map[string]any)
			if provider, ok := surface.(modulehttp.OpenAPIProvider); ok {
				if owned := provider.OpenAPIOperations()[pattern]; len(owned) != 0 {
					operation = cloneOpenAPIOperation(owned)
					pathSpec[method] = operation
				}
			}
			if operation == nil {
				arguments := []any{moduleHTTPRouteSecurity(route)}
				for _, parameter := range moduleHTTPPathParameters(path) {
					arguments = append(arguments, openAPIPathParameter(parameter, moduleHTTPLabel(parameter)))
				}
				arguments = append(arguments, openAPIJSONResponse(moduleHTTPLabel(owner)+" response", openAPIObject(nil)))
				operation = openAPIOperation(
					moduleHTTPOperationID(method, path),
					moduleHTTPLabel(owner),
					moduleHTTPLabel(owner)+" "+strings.ReplaceAll(surfaceName, "_", " "),
					arguments...,
				)
				pathSpec[method] = operation
			}
			ensureModuleHTTPPathParameters(operation, path)
			exposures := make([]string, 0, len(route.Action.Exposures))
			for _, exposure := range route.Action.Exposures {
				exposures = append(exposures, string(exposure))
			}
			permission := ""
			if route.Action.Permission != nil {
				permission = route.Action.Permission.Key
			}
			operation["x-domainry-module-owner"] = owner
			operation["x-domainry-module-route"] = map[string]any{
				"contract_version": routeModuleHTTPContractVersion(surface),
				"owner":            owner,
				"surface":          surfaceName,
				"action_key":       route.Action.Key,
				"exposures":        exposures,
				"authorization":    string(route.Action.Authorization.Strategy),
				"policy_key":       route.Action.Authorization.PolicyKey,
				"permission":       permission,
				"governance": map[string]any{
					"effect_class":         string(route.Action.EffectClass),
					"risk_level":           string(route.Action.RiskLevel),
					"approval_policies":    append([]actioncontract.ApprovalPolicy(nil), route.Action.ApprovalPolicies...),
					"idempotency_decision": route.Action.IdempotencyDecision,
					"audit_class":          route.Action.AuditClass,
				},
			}
			applyModuleHTTPGovernanceHeaders(operation, route.Action)
		}
	}
}

func ensureModuleHTTPPathParameters(operation map[string]any, path string) {
	parameters := moduleHTTPOpenAPIParameters(operation)
	for _, name := range moduleHTTPPathParameters(path) {
		found := false
		for _, parameter := range parameters {
			if parameter["in"] == "path" && parameter["name"] == name {
				found = true
				break
			}
		}
		if !found {
			parameters = append(parameters, openAPIPathParameter(name, moduleHTTPLabel(name)).Value)
		}
	}
	if len(parameters) != 0 {
		operation["parameters"] = parameters
	}
}

func moduleHTTPOpenAPIParameters(operation map[string]any) []map[string]any {
	if operation == nil {
		return nil
	}
	if parameters, ok := operation["parameters"].([]map[string]any); ok {
		return append([]map[string]any(nil), parameters...)
	}
	values, ok := operation["parameters"].([]any)
	if !ok {
		return nil
	}
	parameters := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if parameter, ok := value.(map[string]any); ok {
			parameters = append(parameters, parameter)
		}
	}
	return parameters
}

func cloneOpenAPIOperation(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = cloneOpenAPIValue(value)
	}
	return clone
}

func cloneOpenAPIValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		clone := make(map[string]any, len(typed))
		for key, item := range typed {
			clone[key] = cloneOpenAPIValue(item)
		}
		return clone
	case []map[string]any:
		clone := make([]map[string]any, len(typed))
		for index, item := range typed {
			clone[index] = cloneOpenAPIOperation(item)
		}
		return clone
	case []any:
		clone := make([]any, len(typed))
		for index, item := range typed {
			clone[index] = cloneOpenAPIValue(item)
		}
		return clone
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}

func applyModuleHTTPGovernanceHeaders(operation map[string]any, action actioncontract.ActionDefinition) {
	parameters := moduleHTTPOpenAPIParameters(operation)
	if action.IdempotencyDecision == "caller_key_required" {
		parameters = upsertOpenAPIHeaderParameter(parameters, openAPIHeaderParameter("Idempotency-Key", "Caller-supplied idempotency key", true))
	}
	if len(action.ApprovalPolicies) != 0 {
		parameters = upsertOpenAPIHeaderParameter(parameters, openAPIHeaderParameter("X-Operation-Reason", "Human-supplied auditable operator reason", true))
	}
	if actionHasApprovalPolicy(action, actioncontract.ApprovalConfirmation) {
		confirmation := openAPIHeaderParameter("X-Operation-Confirmation", "Explicit confirmation required by the module route contract", true)
		confirmation["schema"] = map[string]any{"type": "string", "enum": []string{"confirmed"}}
		parameters = upsertOpenAPIHeaderParameter(parameters, confirmation)
	}
	if actionHasApprovalPolicy(action, actioncontract.ApprovalBreakGlass) {
		confirmation := openAPIHeaderParameter("X-Operation-Confirmation", "Explicit break-glass confirmation required by the module route contract", true)
		confirmation["schema"] = map[string]any{"type": "string", "enum": []string{"break-glass"}}
		parameters = upsertOpenAPIHeaderParameter(parameters, confirmation)
	}
	if len(parameters) != 0 {
		operation["parameters"] = parameters
	}
}

func actionHasApprovalPolicy(action actioncontract.ActionDefinition, wanted actioncontract.ApprovalPolicy) bool {
	for _, policy := range action.ApprovalPolicies {
		if policy == wanted {
			return true
		}
	}
	return false
}

func moduleHTTPPathParameters(path string) []string {
	var parameters []string
	for _, segment := range strings.Split(path, "/") {
		segment = strings.TrimSpace(segment)
		if len(segment) > 2 && strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			parameters = append(parameters, strings.TrimSpace(segment[1:len(segment)-1]))
		}
	}
	return parameters
}

func routeModuleHTTPContractVersion(surface modulehttp.Surface) string {
	if surface == nil {
		return ""
	}
	return surface.ContractVersion()
}

func moduleHTTPRouteSecurity(route modulehttp.Route) openAPISecurity {
	switch route.Action.Authorization.Strategy {
	case actioncontract.AuthorizationAnonymousProtocol, actioncontract.AuthorizationDelegatedCredential:
		return openAPIPublicSecurity()
	case actioncontract.AuthorizationServiceIdentity:
		return openAPIServiceCredentialSecurity()
	default:
		return openAPIAdminSecurity()
	}
}

func moduleHTTPOperationID(method, path string) string {
	segments := strings.FieldsFunc(path, func(value rune) bool {
		return !(value >= 'a' && value <= 'z') && !(value >= 'A' && value <= 'Z') && !(value >= '0' && value <= '9')
	})
	if len(segments) > 1 && strings.EqualFold(segments[0], "operations") {
		segments = segments[1:]
	}
	result := strings.ToLower(strings.TrimSpace(method))
	for _, segment := range segments {
		result += moduleHTTPLabel(segment)
	}
	return result
}

func moduleHTTPLabel(value string) string {
	parts := strings.FieldsFunc(strings.TrimSpace(value), func(character rune) bool {
		return character == '_' || character == '-' || character == ' '
	})
	var result strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		result.WriteString(strings.ToUpper(part[:1]))
		result.WriteString(part[1:])
	}
	return result.String()
}

func isOpenAPIHTTPMethod(method string) bool {
	switch strings.ToLower(method) {
	case "get", "post", "put", "patch", "delete", "head", "options":
		return true
	default:
		return false
	}
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func openAPIOperation(operationID string, tag string, summary string, args ...any) map[string]any {
	op := map[string]any{
		"operationId": operationID,
		"summary":     summary,
		"tags":        []string{valueOrDefault(tag, "Generated API")},
		"responses":   map[string]any{"default": openAPIJSONResponse("Error", openAPIRef("Error")).Value},
	}
	parameters := []map[string]any{}
	var endpointMetadata openAPIEndpointMetadata
	endpointClassified := false
	for _, arg := range args {
		switch value := arg.(type) {
		case openAPIEndpointMetadata:
			endpointMetadata, endpointClassified = value, true
		case openAPISecurity:
			if value.Items != nil {
				op["security"] = value.Items
			}
		case openAPIRequestBody:
			op["requestBody"] = value.Value
		case openAPIResponseArg:
			op["responses"] = map[string]any{"200": value.Value, "default": openAPIJSONResponse("Error", openAPIRef("Error")).Value}
		case openAPIParameter:
			parameters = append(parameters, value.Value)
		}
	}
	if len(parameters) > 0 {
		op["parameters"] = parameters
	}
	if endpointClassified {
		op["x-domainry-endpoint-contract"] = openAPIEndpointExtension(operationID, endpointMetadata)
	}
	return op
}

type openAPISecurity struct{ Items []map[string]any }
type openAPIRequestBody struct{ Value map[string]any }
type openAPIResponseArg struct{ Value map[string]any }
type openAPIParameter struct{ Value map[string]any }

func openAPIAdminSecurity() openAPISecurity {
	return openAPISecurity{Items: []map[string]any{{"BearerAuth": []string{}}}}
}

func openAPIPublicSecurity() openAPISecurity {
	return openAPISecurity{Items: []map[string]any{}}
}

func openAPIIntegrationSecurity() openAPISecurity {
	return openAPISecurity{Items: []map[string]any{{"IntegrationAPIKey": []string{}}, {"BearerAuth": []string{}}}}
}

func openAPIServiceCredentialSecurity() openAPISecurity {
	return openAPISecurity{Items: []map[string]any{{"ServiceCredential": []string{}}}}
}

func openAPIPathParameter(name string, description string) openAPIParameter {
	return openAPIParameter{Value: map[string]any{
		"name":        name,
		"in":          "path",
		"required":    true,
		"description": description,
		"schema":      map[string]any{"type": "string"},
	}}
}

func openAPIQueryParameter(name string, description string, schema map[string]any) openAPIParameter {
	return openAPIParameter{Value: map[string]any{
		"name":        name,
		"in":          "query",
		"required":    false,
		"description": description,
		"schema":      schema,
	}}
}

func openAPIRequiredQueryParameter(name string, description string, schema map[string]any) openAPIParameter {
	parameter := openAPIQueryParameter(name, description, schema)
	parameter.Value["required"] = true
	return parameter
}

func openAPIJSONRequest(schema map[string]any) openAPIRequestBody {
	return openAPIRequestBody{Value: map[string]any{
		"required": true,
		"content":  map[string]any{"application/json": map[string]any{"schema": schema}},
	}}
}

func openAPIJSONResponse(description string, schema map[string]any) openAPIResponseArg {
	return openAPIResponseArg{Value: openAPIResponseValue(description, "application/json", schema)}
}

func openAPIResponseValue(description string, contentType string, schema map[string]any) map[string]any {
	return map[string]any{"description": description, "content": map[string]any{contentType: map[string]any{"schema": schema}}}
}

func openAPIResponse(description string, contentType string, schema map[string]any) openAPIResponseArg {
	return openAPIResponseArg{Value: openAPIResponseValue(description, contentType, schema)}
}

func openAPIRef(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}

func openAPIObject(properties map[string]any) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	return map[string]any{"type": "object", "additionalProperties": true, "properties": properties}
}

func openAPIArray(item map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": item}
}

func openAPIObjectSchemaName(key string) string {
	return openAPIOperationName(key)
}

func openAPIOperationName(key string) string {
	parts := regexp.MustCompile(`[^A-Za-z0-9]+`).Split(strings.TrimSpace(key), -1)
	out := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		lower := strings.ToLower(part)
		out += strings.ToUpper(lower[:1]) + lower[1:]
	}
	if out == "" {
		return "Resource"
	}
	return out
}

func openAPIConfigString(config map[string]any, key string) string {
	if config == nil {
		return ""
	}
	value, ok := config[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
