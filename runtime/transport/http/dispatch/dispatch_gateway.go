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
	runtimeID := r.Header.Get(schedulergateway.RuntimeIDHeader)
	if h.executor == nil || !h.targetAvailable || runtimeID == "" || runtimeID != strings.TrimSpace(runtimeID) || runtimeID != h.runtimeID {
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
	if request.RuntimeID != runtimeID || strings.TrimSpace(request.ExecutionID) == "" || strings.TrimSpace(request.IdempotencyKey) == "" || !validExecutionTarget(request.Target) {
		h.writeDispatchError(w, http.StatusBadRequest, "dispatch.request_invalid")
		return
	}
	signedRequest := schedulergateway.SignedRequest{
		Method: r.Method, Path: r.URL.EscapedPath(), RuntimeID: runtimeID, IdempotencyKey: request.IdempotencyKey,
	}
	if schedulergateway.VerifyRequest(body, signedRequest, schedulerSignature(r), h.signingSecret, h.now().UTC()) != nil {
		h.writeDispatchError(w, http.StatusUnauthorized, "dispatch.signature_invalid")
		return
	}
	identity, err := schedulergateway.NewCallbackRequestIdentity(body, signedRequest)
	if err != nil {
		h.writeDispatchError(w, http.StatusBadRequest, "dispatch.request_invalid")
		return
	}
	result, err := h.executor.Execute(r.Context(), dispatchapplication.CallbackExecutionRequest{
		Identity: dispatchapplication.CallbackIdentity{
			Method: identity.Method, Path: identity.Path, RuntimeID: identity.RuntimeID,
			IdempotencyKey: identity.IdempotencyKey, BodySHA256: identity.BodySHA256,
		},
		Execution: dispatchapplication.ExecutionRequest{
			ExecutionID: request.ExecutionID, IdempotencyKey: request.IdempotencyKey, DueAt: request.DueAt,
			Target: dispatchapplication.Target{Type: request.Target.Type, Owner: request.Target.Owner, Operation: request.Target.Operation, ConnectionKey: request.Target.ConnectionKey, Payload: append([]byte(nil), request.Target.Payload...)},
		},
	})
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, executionReceipt{ExecutionID: result.ExecutionID, ID: result.Receipt.ID, Owner: result.Receipt.Owner, Status: result.Receipt.Status, Replay: result.Replay})
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
