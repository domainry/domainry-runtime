package metadatamodel

import "encoding/json"

type MetadataDefinitionValidationResult struct {
	Valid             bool                                `json:"valid"`
	ResourceType      string                              `json:"resource_type"`
	ResourceKey       string                              `json:"resource_key"`
	NormalizedPayload json.RawMessage                     `json:"normalized_payload,omitempty"`
	Errors            []MetadataDefinitionValidationIssue `json:"errors"`
}

type MetadataDefinitionValidationIssue struct {
	FieldPath       string            `json:"field_path"`
	StepKey         string            `json:"step_key,omitempty"`
	OperationKey    string            `json:"operation_key,omitempty"`
	ErrorCode       string            `json:"error_code"`
	MessageKey      string            `json:"message_key"`
	CapabilityKey   string            `json:"capability_key,omitempty"`
	ContractVersion string            `json:"contract_version"`
	Params          map[string]string `json:"params,omitempty"`
}
