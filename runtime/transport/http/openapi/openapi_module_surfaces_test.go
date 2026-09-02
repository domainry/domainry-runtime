package openapi

import (
	"net/http"
	"strings"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

type openAPIModuleSurface struct {
	owner      string
	routes     []modulehttp.Route
	operations map[string]map[string]any
}

func (openAPIModuleSurface) ContractVersion() string { return modulehttp.ContractVersion }
func (s openAPIModuleSurface) Owner() string         { return s.owner }
func (openAPIModuleSurface) Name() string            { return "test" }
func (s openAPIModuleSurface) Routes() []modulehttp.Route {
	return append([]modulehttp.Route(nil), s.routes...)
}
func (openAPIModuleSurface) Handler() http.Handler { return http.NotFoundHandler() }
func (s openAPIModuleSurface) OpenAPIOperations() map[string]map[string]any {
	return s.operations
}

func TestOpenAPIModuleOwnershipComesFromSurfaceRoutes(t *testing.T) {
	surface := openAPIModuleSurface{owner: "example-module", routes: []modulehttp.Route{
		{Action: openAPITestAction("example.metrics.read", "GET /operations/monitoring/metrics", []actioncontract.Exposure{actioncontract.ExposureOps}, actioncontract.AuthorizationAuthenticatedPrincipal, actioncontract.EffectRead, "not_applicable", nil)},
		{Action: openAPITestAction("example.jobs.get", "GET /data-exchange/jobs/{jobID}", []actioncontract.Exposure{actioncontract.ExposurePublic}, actioncontract.AuthorizationAuthenticatedPrincipal, actioncontract.EffectRead, "not_applicable", nil)},
	}}
	spec := BuildWithModuleHTTPSurfaces(appschemamodel.ApplicationSchemaSnapshot{}, "Domainry", []modulehttp.Surface{surface})
	paths := spec["paths"].(map[string]any)
	operation := paths["/operations/monitoring/metrics"].(map[string]any)["get"].(map[string]any)
	if operation["x-domainry-module-owner"] != "example-module" {
		t.Fatalf("module owner=%v", operation["x-domainry-module-owner"])
	}
	if operation["operationId"] != "getMonitoringMetrics" {
		t.Fatalf("module operation id=%v", operation["operationId"])
	}
	route := operation["x-domainry-module-route"].(map[string]any)
	if route["contract_version"] != modulehttp.ContractVersion || route["authorization"] != string(actioncontract.AuthorizationAuthenticatedPrincipal) {
		t.Fatalf("module route=%#v", route)
	}
	jobOperation := paths["/data-exchange/jobs/{jobID}"].(map[string]any)["get"].(map[string]any)
	parameters := jobOperation["parameters"].([]map[string]any)
	if len(parameters) != 1 || parameters[0]["name"] != "jobID" || parameters[0]["in"] != "path" || parameters[0]["required"] != true {
		t.Fatalf("module path parameters=%#v", parameters)
	}

	withoutSurfaces := Build(appschemamodel.ApplicationSchemaSnapshot{})
	if _, hardcoded := withoutSurfaces["paths"].(map[string]any)["/operations/monitoring/metrics"]; hardcoded {
		t.Fatal("OpenAPI must not publish a module route without its Surface")
	}
}

func TestOpenAPIModuleUsesOwnerOperationAndGovernanceContract(t *testing.T) {
	pattern := "POST /example/{exampleID}"
	surface := openAPIModuleSurface{
		owner:  "example-module",
		routes: []modulehttp.Route{{Action: openAPITestAction("example.write", pattern, []actioncontract.Exposure{actioncontract.ExposureOps}, actioncontract.AuthorizationExactRolePermission, actioncontract.EffectWrite, "caller_key_required", []actioncontract.ApprovalPolicy{actioncontract.ApprovalReason, actioncontract.ApprovalConfirmation})}},
		operations: map[string]map[string]any{pattern: {
			"operationId": "applyExample", "summary": "Owner summary",
			"parameters": []any{map[string]any{"in": "query", "name": "dry_run", "required": false, "schema": map[string]any{"type": "boolean"}}},
			"responses":  map[string]any{"204": map[string]any{"description": "Applied"}},
		}},
	}
	spec := BuildWithModuleHTTPSurfaces(appschemamodel.ApplicationSchemaSnapshot{}, "Domainry", []modulehttp.Surface{surface})
	operation := spec["paths"].(map[string]any)["/example/{exampleID}"].(map[string]any)["post"].(map[string]any)
	if operation["operationId"] != "applyExample" || operation["summary"] != "Owner summary" {
		t.Fatalf("owner operation=%#v", operation)
	}
	parameters := operation["parameters"].([]map[string]any)
	required := map[string]bool{}
	for _, parameter := range parameters {
		if parameter["required"] == true {
			required[parameter["name"].(string)] = true
		}
	}
	for _, header := range []string{"Idempotency-Key", "X-Operation-Reason", "X-Operation-Confirmation"} {
		if !required[header] {
			t.Fatalf("required headers=%#v", required)
		}
	}
	if !required["exampleID"] || len(parameters) != 5 || parameters[0]["name"] != "dry_run" {
		t.Fatalf("owner parameters were not preserved while adding path/governance parameters: %#v", parameters)
	}
	governance := operation["x-domainry-module-route"].(map[string]any)["governance"].(map[string]any)
	if governance["effect_class"] != "write" || governance["audit_class"] != "mutation_audit_required" {
		t.Fatalf("governance=%#v", governance)
	}
}

func openAPITestAction(key, pattern string, exposures []actioncontract.Exposure, strategy actioncontract.AuthorizationStrategy, effect actioncontract.EffectClass, idempotency string, approvals []actioncontract.ApprovalPolicy) actioncontract.ActionDefinition {
	method, path, _ := strings.Cut(pattern, " ")
	separator := strings.LastIndex(key, ".")
	action := actioncontract.ActionDefinition{
		Key: key, Owner: "module:example", SourceKind: "module_surface", CapabilityKey: "example.product", CapabilityLabel: "Example",
		OperationKey: key[separator+1:], OperationLabel: key, Label: key, Exposures: exposures, Authorization: actioncontract.Authorization{Strategy: strategy},
		HTTP: &actioncontract.HTTPBinding{Method: method, RouteTemplate: path}, EffectClass: effect, RiskLevel: actioncontract.RiskLow,
		ApprovalPolicies: approvals, IdempotencyDecision: idempotency, AuditClass: "mutation_audit_required", LifecycleStatus: actioncontract.LifecycleActive,
	}
	if strategy == actioncontract.AuthorizationExactRolePermission {
		action.Permission = &actioncontract.PermissionDefinition{Key: key, Owner: action.Owner, ResourceKey: key[:separator], OperationKey: key[separator+1:], Label: key, Category: "Example", LifecycleStatus: actioncontract.LifecycleActive}
	} else if strategy != actioncontract.AuthorizationAuthenticatedPrincipal {
		action.Authorization.PolicyKey = "example.policy"
	}
	return action
}

var _ modulehttp.Surface = openAPIModuleSurface{}
var _ modulehttp.OpenAPIProvider = openAPIModuleSurface{}
