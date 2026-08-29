package openapi

import (
	"fmt"
	"strings"
	"testing"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"
)

func TestOpenAPIOperationsPublishSurfaceAndAudience(t *testing.T) {
	spec := Build(metadatamodel.ApplicationSchemaSnapshot{})
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI paths are missing")
	}
	for path, pathValue := range paths {
		operations, ok := pathValue.(map[string]any)
		if !ok {
			t.Fatalf("path %s has invalid operations", path)
		}
		for method, operationValue := range operations {
			operation, ok := operationValue.(map[string]any)
			if !ok {
				continue
			}
			extension, ok := operation["x-domainry-surface-contract"].(map[string]any)
			if !ok {
				t.Fatalf("%s %s has no Surface contract", method, path)
			}
			if extension["contract_version"] != surfacemodel.ContractVersion {
				t.Fatalf("%s %s contract version=%v", method, path, extension["contract_version"])
			}
			if fmt.Sprint(extension["endpoint_identity"]) == "" {
				t.Fatalf("%s %s has no endpoint identity", method, path)
			}
			if extension["endpoint_identity"] != fmt.Sprintf("%s %s", strings.ToUpper(method), path) {
				t.Fatalf("%s %s endpoint identity=%v", method, path, extension["endpoint_identity"])
			}
			audiences, ok := extension["actor_audiences"].([]string)
			if !ok || len(audiences) == 0 {
				t.Fatalf("%s %s has no actor audiences: %#v", method, path, extension["actor_audiences"])
			}
			surfaces, ok := extension["target_surfaces"].([]string)
			if !ok {
				t.Fatalf("%s %s has invalid target surfaces: %#v", method, path, extension["target_surfaces"])
			}
			for _, surface := range surfaces {
				if _, valid := surfacemodel.ParseProductSurface(surface); !valid {
					t.Fatalf("%s %s has unknown Surface %q", method, path, surface)
				}
			}
			if fmt.Sprint(extension["permission_policy_ref"]) == "" {
				t.Fatalf("%s %s has no operation permission policy reference", method, path)
			}
			if _, ok := extension["required_permissions"].([]string); !ok {
				t.Fatalf("%s %s has invalid required permissions: %#v", method, path, extension["required_permissions"])
			}
			if _, exists := extension["surface_access_permissions"]; exists {
				t.Fatalf("%s %s must not publish Surface access permissions", method, path)
			}
			if effect := extension["effect_class"]; effect != "read" && effect != "write" {
				t.Fatalf("%s %s effect=%v", method, path, effect)
			}
			switch extension["high_risk_action_policy"] {
			case "none", "reason_required", "confirmation_required", "break_glass_required":
			default:
				t.Fatalf("%s %s high-risk policy=%v", method, path, extension["high_risk_action_policy"])
			}
			if extension["high_risk_action_policy"] != "none" {
				parameters, _ := operation["parameters"].([]map[string]any)
				reason := openAPIHeaderByName(parameters, "X-Operation-Reason")
				if reason == nil || reason["required"] != true {
					t.Fatalf("%s %s must publish required X-Operation-Reason", method, path)
				}
				if extension["high_risk_action_policy"] == "confirmation_required" || extension["high_risk_action_policy"] == "break_glass_required" {
					confirmation := openAPIHeaderByName(parameters, "X-Operation-Confirmation")
					if confirmation == nil || confirmation["required"] != true {
						t.Fatalf("%s %s must publish required X-Operation-Confirmation", method, path)
					}
				}
			}
			if fmt.Sprint(extension["idempotency_decision"]) == "" || fmt.Sprint(extension["audit_class"]) == "" {
				t.Fatalf("%s %s lacks idempotency/audit contract: %#v", method, path, extension)
			}
		}
	}
}

func TestOpenAPISurfaceMetadataCoversLegacyAndAggregateTags(t *testing.T) {
	for _, tag := range []string{
		"Integrations",
		"Audit",
		"Open API",
		"OpenAPI",
		"Schema",
		"Workflow",
		"Workflows",
		"Scheduler",
	} {
		metadata, classified := openAPISurfaceMetadataForTag(tag)
		if !classified || len(metadata.Audiences) == 0 {
			t.Fatalf("tag %q metadata=%+v classified=%t", tag, metadata, classified)
		}
	}
	metadata := openAPIProductSurfaces(
		surfacemodel.ProductSurface("unsupported"),
		surfacemodel.ProductSurfaceAdminConsole,
	)
	if len(metadata.Surfaces) != 2 || len(metadata.Audiences) != 1 {
		t.Fatalf("invalid Surface filtering metadata=%+v", metadata)
	}
}

func TestApplyCompiledEndpointSurfaceContractsIgnoresMalformedPathEntries(t *testing.T) {
	paths := map[string]any{
		"/not-a-path-item": "invalid",
		"/unsupported-method": map[string]any{
			"options": map[string]any{"operationId": "optionsOperation"},
		},
		"/not-an-operation": map[string]any{
			"get": "invalid",
		},
	}
	applyCompiledEndpointSurfaceContracts(paths)
	if paths["/not-a-path-item"] != "invalid" {
		t.Fatalf("malformed path item changed: %#v", paths["/not-a-path-item"])
	}
	if got := paths["/unsupported-method"].(map[string]any)["options"].(map[string]any); got["x-domainry-surface-contract"] != nil {
		t.Fatalf("unsupported method classified: %#v", got)
	}
	if paths["/not-an-operation"].(map[string]any)["get"] != "invalid" {
		t.Fatalf("malformed operation changed: %#v", paths["/not-an-operation"])
	}
}

func openAPIHeaderByName(parameters []map[string]any, name string) map[string]any {
	for _, parameter := range parameters {
		if parameter["in"] == "header" && parameter["name"] == name {
			return parameter
		}
	}
	return nil
}
