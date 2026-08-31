package openapi

import appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

import (
	"fmt"
	"regexp"
	"strings"

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
	addAdminCapabilityDisclosureOpenAPIPaths(paths)
	for _, object := range snapshot.Objects {
		addObjectOpenAPIPaths(paths, object)
	}
	for _, action := range snapshot.Actions {
		addActionOpenAPIPath(paths, action)
	}
	addPublicationHandoffOpenAPIPaths(paths)
	paths["/v1/scheduler-triggers:accept"] = map[string]any{"post": openAPIOperation("acceptSchedulerTrigger", "Scheduler Dispatch Gateway", "Identity-authenticated execution callback for one Scheduler-owned run", openAPIProtocolAudience("scheduler_service_service"), openAPIServiceCredentialSecurity(), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Stable downstream receipt", openAPIObject(nil)))}
	addOwnerOperationsReceiptOpenAPIContracts(paths)
	annotateModuleOwnedOpenAPIPaths(paths, surfaces)
	applyCompiledEndpointSurfaceContracts(paths)
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
		owner := strings.TrimSpace(surface.Owner())
		for _, route := range surface.Routes() {
			method, path, found := strings.Cut(strings.TrimSpace(route.Pattern), " ")
			if !found || owner == "" || !isOpenAPIHTTPMethod(method) {
				continue
			}
			pathSpec, _ := paths[strings.TrimSpace(path)].(map[string]any)
			operation, _ := pathSpec[strings.ToLower(strings.TrimSpace(method))].(map[string]any)
			if operation != nil {
				operation["x-domainry-module-owner"] = owner
			}
		}
	}
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
	surfaceMetadata, surfaceClassified := openAPISurfaceMetadataForTag(tag)
	for _, arg := range args {
		switch value := arg.(type) {
		case openAPISurfaceMetadata:
			surfaceMetadata, surfaceClassified = value, true
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
	if surfaceClassified {
		op["x-domainry-surface-contract"] = openAPISurfaceExtension(operationID, surfaceMetadata)
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
