package scheduler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	"github.com/domainry/domainry-scheduler-sdk/dispatchgateway"
)

func (h *SchedulerHandler) getOpsSchedulerState(w http.ResponseWriter, r *http.Request) {
	if err := h.service.AuthorizeOpsRead(r.Context(), h.principal(r)); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	state := opsSchedulerStateDTO{Runs: []opsSchedulerRunDTO{}, DeadLetters: []opsSchedulerDeadLetterDTO{}}
	if h.binding != nil {
		runs, bindingErr := h.binding.Runs(r.Context(), 500)
		if bindingErr != nil {
			h.writeServiceError(w, r, bindingErr)
			return
		}
		projected := make([]opsSchedulerRunDTO, 0, len(runs))
		for _, run := range runs {
			projected = append(projected, projectSDKRun(run))
		}
		deadLetters, bindingErr := h.binding.DeadLetters(r.Context(), 500)
		if bindingErr != nil {
			h.writeServiceError(w, r, bindingErr)
			return
		}
		projectedDeadLetters := make([]opsSchedulerDeadLetterDTO, 0, len(deadLetters))
		for _, deadLetter := range deadLetters {
			projectedDeadLetters = append(projectedDeadLetters, projectSDKDeadLetter(deadLetter))
		}
		state.Provisioned = true
		state.Runs = projected
		state.DeadLetters = projectedDeadLetters
	}
	h.writeJSON(w, http.StatusOK, state)
}

type opsSchedulerStateDTO struct {
	Provisioned bool                        `json:"provisioned"`
	Runs        []opsSchedulerRunDTO        `json:"runs"`
	DeadLetters []opsSchedulerDeadLetterDTO `json:"dead_letters"`
}

type opsSchedulerRunDTO struct {
	ID             string `json:"id"`
	DefinitionKey  string `json:"definition_key,omitempty"`
	Status         string `json:"status"`
	Attempt        int    `json:"attempt,omitempty"`
	ScheduledFor   string `json:"scheduled_for,omitempty"`
	ErrorMessage   string `json:"error_message,omitempty"`
	LeaseOwner     string `json:"lease_owner,omitempty"`
	LeaseExpiresAt string `json:"lease_expires_at,omitempty"`
	FencingToken   int    `json:"fencing_token,omitempty"`
	CorrelationID  string `json:"correlation_id,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type opsSchedulerDeadLetterDTO struct {
	ID            string `json:"id"`
	RunID         string `json:"run_id"`
	DefinitionKey string `json:"definition_key,omitempty"`
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
	FailedAt      string `json:"failed_at,omitempty"`
	ResolvedAt    string `json:"resolved_at,omitempty"`
}

func projectSDKRun(run schedulersdk.Run) opsSchedulerRunDTO {
	return opsSchedulerRunDTO{
		ID: run.Trigger.RunID, DefinitionKey: run.Trigger.DefinitionKey, Status: run.Status,
		Attempt: run.Trigger.Attempt, ScheduledFor: formatSchedulerTime(run.Trigger.ScheduledFor),
		ErrorMessage: run.LastError, LeaseOwner: run.Lease.Owner, LeaseExpiresAt: formatSchedulerTime(run.Lease.ExpiresAt),
		FencingToken: int(run.Lease.Token), CorrelationID: run.DownstreamReceipt.ID,
		CreatedAt: formatSchedulerTime(run.CreatedAt), UpdatedAt: formatSchedulerTime(run.UpdatedAt),
	}
}

func projectSDKDeadLetter(deadLetter schedulersdk.DeadLetter) opsSchedulerDeadLetterDTO {
	return opsSchedulerDeadLetterDTO{
		ID: deadLetter.RunID, RunID: deadLetter.RunID, DefinitionKey: deadLetter.DefinitionKey,
		Status: deadLetter.Status, Reason: deadLetter.Reason,
		FailedAt: formatSchedulerTime(deadLetter.FailedAt), ResolvedAt: formatSchedulerTime(deadLetter.ResolvedAt),
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
