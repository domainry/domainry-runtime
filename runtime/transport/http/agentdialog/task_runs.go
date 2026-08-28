package agentdialog

import (
	"net/http"
	"strings"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
)

type agentTaskRunProjection struct {
	ID                     string                      `json:"id"`
	ProcessID              string                      `json:"process_id,omitempty"`
	NodeInstanceID         string                      `json:"node_instance_id,omitempty"`
	InteractiveRunID       string                      `json:"interactive_run_id,omitempty"`
	TaskKey                string                      `json:"task_key"`
	TaskVersion            string                      `json:"task_version"`
	Status                 string                      `json:"status"`
	Outcome                string                      `json:"outcome,omitempty"`
	ProposalID             string                      `json:"proposal_id,omitempty"`
	ErrorCode              string                      `json:"error_code,omitempty"`
	ReconciliationRequired bool                        `json:"reconciliation_required"`
	ReconciliationState    string                      `json:"reconciliation_state,omitempty"`
	Identity               agentTaskIdentityProjection `json:"identity"`
	Output                 map[string]any              `json:"output,omitempty"`
	ActionReceipts         []string                    `json:"action_receipts,omitempty"`
	AuditRefs              []string                    `json:"audit_refs,omitempty"`
	Attempt                int                         `json:"attempt"`
	MaxAttempts            int                         `json:"max_attempts"`
	Revision               int64                       `json:"revision"`
	CreatedAt              string                      `json:"created_at"`
	UpdatedAt              string                      `json:"updated_at"`
}

type agentTaskIdentityProjection struct {
	Mode                string `json:"mode"`
	ExecutionUserID     string `json:"execution_user_id"`
	ExecutionRoleKey    string `json:"execution_role_key"`
	ServicePrincipalKey string `json:"service_principal_key,omitempty"`
}

func (h *AgentDialogHandler) agentDialogGetTaskRun(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	if h.taskRuns == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "agent.task.repository_unavailable")
		return
	}
	run, found, err := h.taskRuns.Get(r.Context(), principal.WorkspaceID, strings.TrimSpace(r.PathValue("taskRunID")))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if !found || run.WorkspaceID != principal.WorkspaceID || run.Identity.Initiator.UserID != principal.UserID || run.Identity.Initiator.RoleKey != principal.RoleKey || agentTaskAuthorizationStale(run, principal.AuthorizationRevision) {
		h.writeError(w, r, http.StatusNotFound, "agent.task.not_found")
		return
	}
	h.writeJSON(w, http.StatusOK, projectAgentTaskRun(run))
}

func agentTaskAuthorizationStale(run agentmodel.AgentTaskRun, currentRevision string) bool {
	stored, current := strings.TrimSpace(run.Identity.Initiator.AuthorizationRevision), strings.TrimSpace(currentRevision)
	return stored != "" && current != "" && stored != current
}

func projectAgentTaskRun(run agentmodel.AgentTaskRun) agentTaskRunProjection {
	proposalID := ""
	if run.Approval != nil {
		proposalID = run.Approval.ProposalID
	}
	return agentTaskRunProjection{
		ID: run.ID, ProcessID: run.ProcessID, NodeInstanceID: run.NodeInstanceID, InteractiveRunID: run.InteractiveRunID,
		TaskKey: run.TaskKey, TaskVersion: run.TaskVersion, Status: string(run.Status), Outcome: run.Outcome, ProposalID: proposalID,
		ErrorCode: run.LastErrorCode, Output: cloneStringAnyMap(run.Output), ActionReceipts: agentTaskActionReceipts(run), AuditRefs: append([]string(nil), run.Evidence.AuditRefs...),
		ReconciliationRequired: run.Reconciliation.Required, ReconciliationState: run.Reconciliation.State,
		Identity: agentTaskIdentityProjection{Mode: run.Identity.Mode, ExecutionUserID: run.Identity.Execution.UserID, ExecutionRoleKey: run.Identity.Execution.RoleKey, ServicePrincipalKey: run.Identity.ServicePrincipalKey},
		Attempt:  run.Attempt, MaxAttempts: run.MaxAttempts, Revision: run.Revision, CreatedAt: run.CreatedAt.UTC().Format(timeFormat), UpdatedAt: run.UpdatedAt.UTC().Format(timeFormat),
	}
}

func agentTaskActionReceipts(run agentmodel.AgentTaskRun) []string {
	refs := []string{}
	if run.Approval != nil {
		for _, key := range []string{"receipt_id", "action_receipt_id", "idempotency_receipt_id"} {
			if value := strings.TrimSpace(agentPolicyStringFromAny(run.Approval.Execution[key])); value != "" {
				refs = append(refs, value)
			}
		}
	}
	return refs
}

const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"
