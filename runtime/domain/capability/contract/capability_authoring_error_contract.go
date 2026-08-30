package contract

import "strings"

type CapabilityAuthoringErrorContract struct {
	CapabilityKey   string `json:"capability_key,omitempty"`
	ContractVersion string `json:"contract_version"`
	FieldPath       string `json:"field_path,omitempty"`
}

func RuntimeAuthoringErrorContract(code string, params map[string]string) CapabilityAuthoringErrorContract {
	result := CapabilityAuthoringErrorContract{ContractVersion: RuntimeAuthoringContractVersion, FieldPath: runtimeErrorFieldPath(params)}
	code = strings.TrimSpace(code)
	if code == "backend.metadata.definition_version_conflict" {
		result.CapabilityKey = metadataResourceCapabilityKey(params["resource_type"])
		return result
	}
	result.CapabilityKey = fallbackAuthoringErrorCapability(code)
	return result
}

func fallbackAuthoringErrorCapability(code string) string {
	switch {
	case strings.HasPrefix(code, "backend.action."):
		return "action.definition"
	case strings.HasPrefix(code, "backend.automation."):
		return "automation.rule"
	case strings.HasPrefix(code, "backend.workflow."):
		return "workflow.graph_v2"
	case strings.HasPrefix(code, "backend.scheduler."):
		return "scheduler.business_job"
	case strings.HasPrefix(code, "backend.report."):
		return "report.definition"
	case strings.HasPrefix(code, "backend.integration.binding."):
		return "integration.binding_validation"
	case strings.HasPrefix(code, "backend.integration.connector.operation_") || strings.HasPrefix(code, "backend.integration.connector.protocol_field_") || strings.HasPrefix(code, "backend.integration.connector.compensation_") || code == "backend.integration.connector.reserve_contract_incomplete":
		return "integration.connector_operation"
	case strings.HasPrefix(code, "backend.integration.connector."):
		return "integration.connector_definition"
	case strings.HasPrefix(code, "backend.integration.connection."), strings.HasPrefix(code, "backend.integration.secret"), strings.HasPrefix(code, "backend.integration.webhook_signature."):
		return "integration.connection"
	case strings.HasPrefix(code, "backend.integration.outbox."):
		return "integration.outbox"
	default:
		return ""
	}
}

func metadataResourceCapabilityKey(resourceType string) string {
	switch strings.TrimSpace(resourceType) {
	case "field":
		return "schema.field"
	case "action":
		return "action.definition"
	case "automation_rule":
		return "automation.rule"
	case "connector":
		return "integration.connector_definition"
	case "report":
		return "report.definition"
	default:
		return ""
	}
}

func runtimeErrorFieldPath(params map[string]string) string {
	for _, key := range []string{"field_path", "field", "parameter_path", "path"} {
		if value := strings.TrimSpace(params[key]); value != "" {
			return value
		}
	}
	return ""
}
