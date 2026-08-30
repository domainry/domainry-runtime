package runtime

import (
	"context"
	"strconv"
	"strings"

	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-foundation/apperror"
	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *AgentTaskRunApplicationService) Operate(ctx context.Context, workspaceID, runID, kind, idempotencyKey, reason string, actor principalmodel.Principal) (agentmodel.AgentTaskRun, bool, error) {
	kind, idempotencyKey, reason = strings.TrimSpace(kind), strings.TrimSpace(idempotencyKey), strings.TrimSpace(reason)
	if s == nil || s.repository == nil || s.audit == nil {
		return agentmodel.AgentTaskRun{}, false, apperror.New(apperror.KindUnavailable, "agent.task.operation_unavailable", nil, nil)
	}
	if !actor.Known || actor.WorkspaceID != strings.TrimSpace(workspaceID) {
		return agentmodel.AgentTaskRun{}, false, apperror.New(apperror.KindBadRequest, "agent.task.operation_evidence_required", nil, nil)
	}
	if actor.UserID == "" || idempotencyKey == "" || reason == "" {
		return agentmodel.AgentTaskRun{}, false, apperror.New(apperror.KindBadRequest, "agent.task.operation_evidence_required", nil, nil)
	}
	run, found, err := s.Get(ctx, workspaceID, runID)
	if err != nil || !found {
		return run, false, err
	}
	for _, operation := range run.Operations {
		if operation.IdempotencyKey == idempotencyKey {
			if operation.Kind != kind {
				return run, false, apperror.New(apperror.KindConflict, "backend.idempotency.key_reused", nil, nil)
			}
			return run, true, nil
		}
	}
	previousStatus, expectedRevision, now := run.Status, run.Revision, s.clock.Now().UTC()
	switch kind {
	case "retry":
		if run.Status != agentmodel.AgentTaskRunFailed && run.Status != agentmodel.AgentTaskRunDeadLetter && run.Status != agentmodel.AgentTaskRunCancelled {
			return run, false, apperror.New(apperror.KindConflict, "agent.task.retry_state_invalid", nil, nil)
		}
		run.Status, run.Outcome, run.CompletedAt, run.NextAttemptAt, run.CancelRequestedAt, run.CancellationReason = agentmodel.AgentTaskRunPending, "", nil, nil, nil, ""
		run.Reconciliation = agentmodel.AgentTaskReconciliation{State: "force_restart"}
	case "reconcile":
		if !run.Reconciliation.Required || strings.TrimSpace(run.Reconciliation.ExternalRunID) == "" {
			return run, false, apperror.New(apperror.KindConflict, "agent.task.reconcile_state_invalid", nil, nil)
		}
		run.Status, run.Outcome, run.CompletedAt, run.NextAttemptAt = agentmodel.AgentTaskRunPending, "", nil, nil
		run.Reconciliation.State, run.Reconciliation.Reason = "poll_required", "operator requested reconciliation"
	case "resolve":
		if !run.Status.Terminal() || !run.Reconciliation.Required {
			return run, false, apperror.New(apperror.KindConflict, "agent.task.resolve_state_invalid", nil, nil)
		}
		run.Status, run.Outcome, run.LastErrorCode = agentmodel.AgentTaskRunManualReview, "manual_review", ""
		run.Reconciliation.Required, run.Reconciliation.State, run.Reconciliation.Reason = false, "manually_resolved", reason
	default:
		return run, false, apperror.New(apperror.KindBadRequest, "agent.task.operation_invalid", nil, nil)
	}
	auditRef := "agent-task-operation:" + run.ID + ":" + kind + ":" + strconv.FormatInt(expectedRevision+1, 10)
	if err := s.audit.AppendAudit(ctx, auditcontract.AuditAppendRequest{Event: "agent_task_" + kind, ObjectKey: "agent_task_run", RecordID: run.ID, Principal: actor, Summary: "Agent Task operator action", Before: map[string]any{"status": previousStatus}, After: map[string]any{"status": run.Status}, Metadata: map[string]any{"audit_ref": auditRef, "reason": reason, "idempotency_key": idempotencyKey, "process_id": run.ProcessID}}); err != nil {
		return run, false, err
	}
	run.Operations = append(run.Operations, agentmodel.AgentTaskOperationEvidence{Kind: kind, IdempotencyKey: idempotencyKey, Actor: actor.UserID, Reason: reason, AuditRef: auditRef, CreatedAt: now})
	run.Evidence.AuditRefs = append(run.Evidence.AuditRefs, auditRef)
	run.UpdatedAt, run.Revision = now, run.Revision+1
	if err := s.repository.SaveOperationalTransition(ctx, run, previousStatus, expectedRevision); err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	if run.Status == agentmodel.AgentTaskRunPending || run.Status == agentmodel.AgentTaskRunRetryScheduled {
		s.wake(run)
	}
	return run, false, nil
}
