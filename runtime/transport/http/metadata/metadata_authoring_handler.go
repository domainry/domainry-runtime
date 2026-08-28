package metadata

import (
	"net/http"
	"strings"

	"github.com/domainry/domainry-foundation/idempotency"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func (h *MetadataHandler) applyMetadataAuthoringHeaders(w http.ResponseWriter, r *http.Request, request *metadatamodel.MetadataDefinitionUpsertRequest) bool {
	builderTaskID := strings.TrimSpace(r.Header.Get("Builder-Task-ID"))
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	expectedHeader := strings.TrimSpace(r.Header.Get("Expected-Schema-Hash"))
	expectedHash := expectedHeader
	if expectedHeader == "empty" {
		expectedHash = ""
	}
	if request.ExpectedSchemaHash != nil && expectedHeader != "" && strings.TrimSpace(*request.ExpectedSchemaHash) != expectedHash {
		h.writeError(w, r, http.StatusBadRequest, "backend.metadata.expected_schema_hash_mismatch")
		return false
	}
	if request.ExpectedSchemaHash == nil && expectedHeader != "" {
		request.ExpectedSchemaHash = &expectedHash
	}
	if builderTaskID == "" {
		return true
	}
	if idempotencyKey == "" {
		h.writeError(w, r, http.StatusBadRequest, idempotency.ErrorCodeMissingKey)
		return false
	}
	if request.ExpectedSchemaHash == nil || expectedHeader == "" {
		h.writeError(w, r, http.StatusBadRequest, "backend.metadata.expected_schema_hash_required")
		return false
	}
	if strings.TrimSpace(request.SourceKind) == "" {
		request.SourceKind = "builder_v4"
	}
	if strings.TrimSpace(request.SourceID) == "" {
		request.SourceID = builderTaskID + ":" + idempotencyKey
	}
	w.Header().Set("Builder-Task-ID", builderTaskID)
	w.Header().Set("Idempotency-Key", idempotencyKey)
	return true
}
