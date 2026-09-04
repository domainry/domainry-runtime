package openapi

import (
	"fmt"
	"strings"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	endpointmodel "github.com/domainry/domainry-runtime/runtime/domain/endpoint/model"
)

func TestOpenAPIOperationsPublishEndpointPolicy(t *testing.T) {
	spec := Build(appschemamodel.ApplicationSchemaSnapshot{})
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
			extension, ok := operation["x-domainry-endpoint-contract"].(map[string]any)
			if !ok {
				if owner, _ := operation["x-domainry-module-owner-fallback"].(string); owner == "" {
					t.Errorf("%s %s has neither a Runtime endpoint contract nor an explicit module-owner fallback", method, path)
				}
				continue
			}
			if extension["contract_version"] != endpointmodel.ContractVersion {
				t.Fatalf("%s %s contract version=%v", method, path, extension["contract_version"])
			}
			if fmt.Sprint(extension["endpoint_identity"]) == "" {
				t.Fatalf("%s %s has no endpoint identity", method, path)
			}
			if extension["endpoint_identity"] != fmt.Sprintf("%s %s", strings.ToUpper(method), path) {
				t.Fatalf("%s %s endpoint identity=%v", method, path, extension["endpoint_identity"])
			}
			exposures, ok := extension["listener_exposures"].([]string)
			if !ok || len(exposures) == 0 {
				t.Fatalf("%s %s has invalid listener exposures: %#v", method, path, extension["listener_exposures"])
			}
			for _, exposure := range exposures {
				switch endpointmodel.ListenerExposure(exposure) {
				case endpointmodel.ListenerExposurePublic, endpointmodel.ListenerExposureManagement, endpointmodel.ListenerExposureOps:
				default:
					t.Fatalf("%s %s has unknown listener exposure %q", method, path, exposure)
				}
			}
			if fmt.Sprint(extension["permission_policy_ref"]) == "" {
				t.Fatalf("%s %s has no operation permission policy reference", method, path)
			}
			if _, ok := extension["required_permissions"].([]string); !ok {
				t.Fatalf("%s %s has invalid required permissions: %#v", method, path, extension["required_permissions"])
			}
			if _, exists := extension["target_surfaces"]; exists {
				t.Fatalf("%s %s must not publish frontend Surface metadata", method, path)
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

func TestOpenAPIProtocolAudienceIsEndpointMetadata(t *testing.T) {
	metadata := openAPIProtocolAudience("signed_webhook")
	if len(metadata.ProtocolAudiences) != 1 || metadata.ProtocolAudiences[0] != "signed_webhook" {
		t.Fatalf("protocol metadata=%+v", metadata)
	}
}

func TestApplyCompiledEndpointContractsIgnoresMalformedPathEntries(t *testing.T) {
	paths := map[string]any{
		"/not-a-path-item": "invalid",
		"/unsupported-method": map[string]any{
			"options": map[string]any{"operationId": "optionsOperation"},
		},
		"/not-an-operation": map[string]any{
			"get": "invalid",
		},
	}
	applyCompiledEndpointContracts(paths)
	if paths["/not-a-path-item"] != "invalid" {
		t.Fatalf("malformed path item changed: %#v", paths["/not-a-path-item"])
	}
	if got := paths["/unsupported-method"].(map[string]any)["options"].(map[string]any); got["x-domainry-endpoint-contract"] != nil {
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
