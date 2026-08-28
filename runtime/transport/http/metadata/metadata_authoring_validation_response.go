package metadata

import (
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	"net/http"
	"strings"
)

type metadataAuthoringValidationFailure struct {
	metadatamodel.MetadataDefinitionValidationResult
	Error           string            `json:"error"`
	Code            string            `json:"code"`
	MessageKey      string            `json:"message_key"`
	FieldPath       string            `json:"field_path,omitempty"`
	CapabilityKey   string            `json:"capability_key,omitempty"`
	ContractVersion string            `json:"contract_version"`
	Params          map[string]string `json:"params,omitempty"`
	ErrorClass      string            `json:"error_class"`
	RepairAction    string            `json:"repair_action"`
	Retryable       bool              `json:"retryable"`
}

func (h *MetadataHandler) writeMetadataAuthoringValidationResult(w http.ResponseWriter, r *http.Request, result metadatamodel.MetadataDefinitionValidationResult) {
	if result.Valid || strings.TrimSpace(operationscontract.BuilderTaskID(r.Context())) == "" || len(result.Errors) == 0 {
		h.writeJSON(w, http.StatusOK, result)
		return
	}
	issue := result.Errors[0]
	h.writeJSON(w, http.StatusUnprocessableEntity, metadataAuthoringValidationFailure{
		MetadataDefinitionValidationResult: result,
		Error:                              issue.ErrorCode, Code: issue.ErrorCode, MessageKey: issue.MessageKey,
		FieldPath: issue.FieldPath, CapabilityKey: issue.CapabilityKey,
		ContractVersion: issue.ContractVersion, Params: issue.Params,
		ErrorClass: "repairable", RepairAction: "repair_capability_payload", Retryable: true,
	})
}
