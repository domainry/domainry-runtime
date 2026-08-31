package openapi

import (
	"net/http"
	"strings"

	endpointmodel "github.com/domainry/domainry-runtime/runtime/domain/endpoint/model"
)

type openAPIEndpointMetadata struct {
	ProtocolAudiences []string
}

func openAPIEndpointAccess() openAPIEndpointMetadata {
	return openAPIEndpointMetadata{}
}

func openAPIProtocolAudience(audiences ...string) openAPIEndpointMetadata {
	return openAPIEndpointMetadata{ProtocolAudiences: append([]string(nil), audiences...)}
}

func openAPIEndpointExtension(operationID string, metadata openAPIEndpointMetadata) map[string]any {
	return map[string]any{
		"contract_version":   endpointmodel.ContractVersion,
		"endpoint_identity":  operationID,
		"protocol_audiences": append([]string(nil), metadata.ProtocolAudiences...),
	}
}

func applyCompiledEndpointContracts(paths map[string]any) {
	for path, rawPathItem := range paths {
		pathItem, ok := rawPathItem.(map[string]any)
		if !ok {
			continue
		}
		for method, rawOperation := range pathItem {
			upperMethod := strings.ToUpper(method)
			switch upperMethod {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			default:
				continue
			}
			operation, ok := rawOperation.(map[string]any)
			if !ok {
				continue
			}
			contract, classified := endpointmodel.EndpointContracts[upperMethod+" "+path]
			if !classified {
				continue
			}
			operation["x-domainry-endpoint-contract"] = compiledEndpointExtension(contract, operation["operationId"])
			applyHighRiskOperationHeaders(operation, contract)
		}
	}
}

func applyHighRiskOperationHeaders(operation map[string]any, contract endpointmodel.RuntimeEndpointContractV1) {
	if contract.HighRiskPolicy == endpointmodel.HighRiskActionNone {
		return
	}
	parameters, _ := operation["parameters"].([]map[string]any)
	parameters = upsertOpenAPIHeaderParameter(parameters, openAPIHeaderParameter("X-Operation-Reason", "Human-supplied auditable operator reason", true))
	switch contract.HighRiskPolicy {
	case endpointmodel.HighRiskActionConfirmRequired:
		confirmation := openAPIHeaderParameter("X-Operation-Confirmation", "Explicit confirmation required by the compiled endpoint contract", true)
		confirmation["schema"] = map[string]any{"type": "string", "enum": []string{"confirmed"}}
		parameters = upsertOpenAPIHeaderParameter(parameters, confirmation)
	case endpointmodel.HighRiskActionBreakGlass:
		confirmation := openAPIHeaderParameter("X-Operation-Confirmation", "Explicit break-glass confirmation required by the compiled endpoint contract", true)
		confirmation["schema"] = map[string]any{"type": "string", "enum": []string{"break-glass"}}
		parameters = upsertOpenAPIHeaderParameter(parameters, confirmation)
	}
	operation["parameters"] = parameters
}

func upsertOpenAPIHeaderParameter(parameters []map[string]any, replacement map[string]any) []map[string]any {
	for index, parameter := range parameters {
		if parameter["in"] == "header" && parameter["name"] == replacement["name"] {
			parameters[index] = replacement
			return parameters
		}
	}
	return append(parameters, replacement)
}

func compiledEndpointExtension(contract endpointmodel.RuntimeEndpointContractV1, operationID any) map[string]any {
	exposures := make([]string, 0, len(contract.ListenerExposures))
	for _, exposure := range contract.ListenerExposures {
		exposures = append(exposures, string(exposure))
	}
	return map[string]any{
		"contract_version":        contract.ContractVersion,
		"endpoint_identity":       contract.EndpointIdentity,
		"operation_id":            operationID,
		"listener_exposures":      exposures,
		"protocol_audiences":      append([]string(nil), contract.ProtocolAudiences...),
		"required_permissions":    append([]string(nil), contract.RequiredPermissions...),
		"permission_policy_ref":   contract.PermissionPolicyRef,
		"effect_class":            string(contract.EffectClass),
		"high_risk_action_policy": string(contract.HighRiskPolicy),
		"idempotency_decision":    contract.IdempotencyDecision,
		"audit_class":             contract.AuditClass,
	}
}
