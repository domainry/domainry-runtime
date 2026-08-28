package validation

import (
	"strings"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func NewMetadataDefinitionValidationIssue(code, fieldPath, stepKey, operationKey string, params map[string]string) metadatamodel.MetadataDefinitionValidationIssue {
	contract := capabilitycontract.RuntimeAuthoringErrorContract(code, params)
	if strings.TrimSpace(fieldPath) == "" {
		fieldPath = contract.FieldPath
	}
	return metadatamodel.MetadataDefinitionValidationIssue{
		FieldPath: fieldPath, StepKey: stepKey, OperationKey: operationKey, ErrorCode: code, MessageKey: code,
		CapabilityKey: contract.CapabilityKey, ContractVersion: contract.ContractVersion, Params: params,
	}
}

func MetadataDefinitionValidationErrorFieldPath(code string, params map[string]string) string {
	switch code {
	case "backend.metadata.relation_target_required", "backend.metadata.relation_target_not_found":
		return "validation.target"
	case "backend.metadata.relation_cardinality_invalid":
		return "config.cardinality"
	case "backend.metadata.relation_on_delete_invalid", "backend.metadata.relation_set_null_required":
		return "config.on_delete"
	case "backend.metadata.relation_inverse_name_invalid":
		return "config.inverse_name"
	}
	return strings.TrimSpace(params["field"])
}
