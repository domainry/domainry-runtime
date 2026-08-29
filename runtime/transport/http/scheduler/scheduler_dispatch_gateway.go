package scheduler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	"github.com/domainry/domainry-scheduler-sdk/dispatchgateway"
)

func (h *SchedulerHandler) acceptSchedulerTrigger(w http.ResponseWriter, r *http.Request) {
	credential := strings.TrimSpace(r.Header.Get("X-Domainry-Service-Credential"))
	runtimeID := strings.TrimSpace(r.Header.Get("X-Domainry-Runtime-ID"))
	if h.dispatcher == nil || h.authenticateService == nil || credential == "" || runtimeID == "" || runtimeID != h.runtimeID || h.authenticateService(r.Context(), credential) != nil {
		h.writeDispatchGatewayError(w, http.StatusUnauthorized, "scheduler.service_credential_invalid")
		return
	}
	var request dispatchgateway.Request
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&request); err != nil {
		h.writeDispatchGatewayError(w, http.StatusBadRequest, "scheduler.dispatch_request_invalid")
		return
	}
	application := schedulersdk.ApplicationRef{RuntimeID: h.runtimeID}
	if err := request.Validate(application); err != nil || request.RuntimeID != runtimeID {
		h.writeDispatchGatewayError(w, http.StatusBadRequest, "scheduler.dispatch_request_invalid")
		return
	}
	receipt, err := h.dispatcher.Dispatch(r.Context(), request.Trigger)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, dispatchgateway.Receipt{RunID: request.Trigger.RunID, ID: receipt.ID, Owner: receipt.Owner, Status: receipt.Status})
}

func (h *SchedulerHandler) writeDispatchGatewayError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"status_code": status, "code": code})
}

type SchedulerServiceAuthenticator func(context.Context, string) error
