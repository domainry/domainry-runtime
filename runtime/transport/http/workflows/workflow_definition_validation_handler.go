package workflows

import (
	"net/http"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func (h *WorkflowsHandler) validateWorkflowDefinition(w http.ResponseWriter, r *http.Request) {
	request := struct {
		Payload definitionmodel.WorkflowSchema `json:"payload"`
	}{}
	if !h.decodeJSON(w, r, &request) {
		return
	}
	workflowKey := strings.TrimSpace(r.PathValue("workflowKey"))
	if workflowKey == "" || strings.TrimSpace(request.Payload.Key) != workflowKey {
		h.writeError(w, r, http.StatusBadRequest, "backend.workflow.definition_identity_required", "workflow_key", workflowKey)
		return
	}
	report, err := h.definitions.ValidateWorkflowDefinition(r.Context(), request.Payload, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, report)
}
