package scheduler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	schedulerbusiness "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	"github.com/domainry/domainry-scheduler-sdk/dispatchgateway"
)

func (h *SchedulerHandler) getOpsSchedulerState(w http.ResponseWriter, r *http.Request) {
	state, err := h.service.OpsState(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if h.binding != nil {
		runs, bindingErr := h.binding.Runs(r.Context(), 500)
		if bindingErr != nil {
			h.writeServiceError(w, r, bindingErr)
			return
		}
		projected := make([]schedulerbusiness.OpsSchedulerRunDTO, 0, len(runs)+len(state.Runs))
		owned := make(map[string]struct{}, len(runs))
		for _, run := range runs {
			projected = append(projected, projectSDKRun(run))
			owned[run.Trigger.RunID] = struct{}{}
		}
		for _, run := range state.Runs {
			if _, replaced := owned[run.ID]; replaced {
				continue
			}
			projected = append(projected, run)
		}
		state.Provisioned = true
		state.Runs = projected
	}
	h.writeJSON(w, http.StatusOK, state)
}

func projectSDKRun(run schedulersdk.Run) schedulerbusiness.OpsSchedulerRunDTO {
	return schedulerbusiness.OpsSchedulerRunDTO{
		ID: run.Trigger.RunID, DefinitionKey: run.Trigger.DefinitionKey, Status: run.Status,
		Attempt: run.Trigger.Attempt, ScheduledFor: formatSchedulerTime(run.Trigger.ScheduledFor),
		ErrorMessage: run.LastError, LeaseOwner: run.Lease.Owner, LeaseExpiresAt: formatSchedulerTime(run.Lease.ExpiresAt),
		FencingToken: int(run.Lease.Token), CorrelationID: run.DownstreamReceipt.ID,
		CreatedAt: formatSchedulerTime(run.CreatedAt), UpdatedAt: formatSchedulerTime(run.UpdatedAt),
	}
}

func formatSchedulerTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

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

func triggerSchedulerNow(ctx context.Context, binding schedulersdk.Binding, definitionKey string) (schedulersdk.Run, error) {
	run, err := binding.TriggerNow(ctx, definitionKey, "operator requested scheduler.job.run")
	if err != nil {
		return run, err
	}
	return binding.Run(ctx, run.Trigger.RunID)
}
