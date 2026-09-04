package dispatch

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	dispatchapplication "github.com/domainry/domainry-runtime/runtime/application/dispatch"
	schedulergateway "github.com/domainry/domainry-scheduler-sdk/dispatchgateway"
)

func (h *ExecutionHandler) acceptExecution(w http.ResponseWriter, r *http.Request) {
	runtimeID := strings.TrimSpace(r.Header.Get("X-Domainry-Runtime-ID"))
	if h.executor == nil || runtimeID == "" || runtimeID != h.runtimeID {
		h.writeDispatchError(w, http.StatusUnauthorized, "dispatch.signature_invalid")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (8<<20)+1))
	if err != nil || len(body) > 8<<20 {
		h.writeDispatchError(w, http.StatusBadRequest, "dispatch.request_invalid")
		return
	}
	var request executionRequest
	if err := json.Unmarshal(body, &request); err != nil {
		h.writeDispatchError(w, http.StatusBadRequest, "dispatch.request_invalid")
		return
	}
	signature := schedulerSignature(r)
	if signature.ClientID != schedulergateway.SchedulerClientID || schedulergateway.Verify(body, request.ExecutionID, signature, h.signingSecret, h.now().UTC()) != nil {
		h.writeDispatchError(w, http.StatusUnauthorized, "dispatch.signature_invalid")
		return
	}
	if request.RuntimeID != runtimeID || strings.TrimSpace(request.ExecutionID) == "" || strings.TrimSpace(request.IdempotencyKey) == "" || !validExecutionTarget(request.Target) {
		h.writeDispatchError(w, http.StatusBadRequest, "dispatch.request_invalid")
		return
	}
	receipt, err := h.executor.Execute(r.Context(), dispatchapplication.ExecutionRequest{
		ExecutionID: request.ExecutionID, IdempotencyKey: request.IdempotencyKey, DueAt: request.DueAt,
		Target: dispatchapplication.Target{Type: request.Target.Type, Owner: request.Target.Owner, Operation: request.Target.Operation, ConnectionKey: request.Target.ConnectionKey, Payload: append([]byte(nil), request.Target.Payload...)},
	})
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, executionReceipt{ExecutionID: request.ExecutionID, ID: receipt.ID, Owner: receipt.Owner, Status: receipt.Status})
}

func validExecutionTarget(target executionTarget) bool {
	targetType := strings.TrimSpace(target.Type)
	if targetType == "" {
		targetType = "runtime_operation"
	}
	if strings.TrimSpace(target.Operation) == "" || (targetType == "runtime_operation" && strings.TrimSpace(target.Owner) == "") || (targetType == "http" && strings.TrimSpace(target.ConnectionKey) == "") {
		return false
	}
	return targetType == "runtime_operation" || targetType == "http"
}

func (h *ExecutionHandler) writeDispatchError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"status_code": status, "code": code})
}
