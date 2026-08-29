package runtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"
	auditcontract "github.com/domainry/domainry-runtime/runtime/domain/audit/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type AgentTaskRunApplicationService struct {
	repository agentrepository.AgentTaskRunRepository
	clock      workerplatform.Clock
	terminal   AgentTaskTerminalCommitter
	audit      auditcontract.AuditAppender
	wakeup     AgentTaskWakeup
}

func NewAgentTaskRunApplicationServiceWithAudit(repository agentrepository.AgentTaskRunRepository, clock workerplatform.Clock, terminal AgentTaskTerminalCommitter, audit auditcontract.AuditAppender) *AgentTaskRunApplicationService {
	service := NewAgentTaskRunApplicationService(repository, clock, terminal)
	service.audit = audit
	return service
}

type AgentTaskTerminalCommitter interface {
	CommitAgentTaskTerminal(context.Context, agentmodel.AgentTaskRun, string, int64) error
	CommitAgentTaskApprovalTerminal(context.Context, agentmodel.AgentTaskRun) error
}

func NewAgentTaskRunApplicationService(repository agentrepository.AgentTaskRunRepository, clock workerplatform.Clock, terminal ...AgentTaskTerminalCommitter) *AgentTaskRunApplicationService {
	if clock == nil {
		clock = workerplatform.SystemClock{}
	}
	service := &AgentTaskRunApplicationService{repository: repository, clock: clock}
	if len(terminal) > 0 {
		service.terminal = terminal[0]
	}
	return service
}

func (s *AgentTaskRunApplicationService) Create(ctx context.Context, run agentmodel.AgentTaskRun) (agentmodel.AgentTaskRun, bool, error) {
	if s == nil || s.repository == nil {
		return agentmodel.AgentTaskRun{}, false, apperror.New(apperror.KindUnavailable, "agent.task.repository_unavailable", nil, nil)
	}
	now := s.clock.Now().UTC()
	run.ID, run.WorkspaceID, run.TaskKey, run.TaskVersion, run.IdempotencyKey = strings.TrimSpace(run.ID), strings.TrimSpace(run.WorkspaceID), strings.TrimSpace(run.TaskKey), strings.TrimSpace(run.TaskVersion), strings.TrimSpace(run.IdempotencyKey)
	run.Status, run.Attempt, run.Revision = agentmodel.AgentTaskRunPending, 0, 1
	run.CreatedAt, run.UpdatedAt = now, now
	if run.MaxAttempts <= 0 {
		return agentmodel.AgentTaskRun{}, false, apperror.New(apperror.KindBadRequest, "agent.task.max_attempts_invalid", nil, nil)
	}
	if !run.ValidForCreate() {
		return agentmodel.AgentTaskRun{}, false, apperror.New(apperror.KindBadRequest, "agent.task.contract_invalid", nil, nil)
	}
	saved, replayed, err := s.repository.Create(ctx, run)
	if err == nil && (saved.Status == agentmodel.AgentTaskRunPending || saved.Status == agentmodel.AgentTaskRunRetryScheduled) {
		s.wake(saved)
	}
	return saved, replayed, err
}

func (s *AgentTaskRunApplicationService) Get(ctx context.Context, workspaceID, runID string) (agentmodel.AgentTaskRun, bool, error) {
	if s == nil || s.repository == nil {
		return agentmodel.AgentTaskRun{}, false, apperror.New(apperror.KindUnavailable, "agent.task.repository_unavailable", nil, nil)
	}
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return agentmodel.AgentTaskRun{}, false, apperror.New(apperror.KindBadRequest, "agent.task.query_invalid", err, nil)
	}
	if strings.TrimSpace(runID) == "" {
		return agentmodel.AgentTaskRun{}, false, apperror.New(apperror.KindBadRequest, "agent.task.query_invalid", err, nil)
	}
	return s.repository.Get(ctx, workspace.String(), strings.TrimSpace(runID))
}

func (s *AgentTaskRunApplicationService) List(ctx context.Context, workspaceID string, filter agentrepository.AgentTaskRunFilter) ([]agentmodel.AgentTaskRun, error) {
	if s == nil || s.repository == nil {
		return nil, apperror.New(apperror.KindUnavailable, "agent.task.repository_unavailable", nil, nil)
	}
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return nil, apperror.New(apperror.KindBadRequest, "agent.task.query_invalid", err, nil)
	}
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 500
	}
	return s.repository.List(ctx, workspace.String(), filter)
}

func (s *AgentTaskRunApplicationService) ClaimNext(ctx context.Context, workspaceID string, owner workerplatform.WorkerID, leaseDuration time.Duration) (agentrepository.AgentTaskClaim, bool, error) {
	if s == nil || s.repository == nil {
		return agentrepository.AgentTaskClaim{}, false, apperror.New(apperror.KindUnavailable, "agent.task.repository_unavailable", nil, nil)
	}
	workspace, workspaceErr := principalmodel.NewWorkspaceID(workspaceID)
	if workspaceErr != nil {
		return agentrepository.AgentTaskClaim{}, false, apperror.New(apperror.KindBadRequest, "agent.task.claim_invalid", nil, nil)
	}
	if strings.TrimSpace(owner.String()) == "" || leaseDuration <= 0 {
		return agentrepository.AgentTaskClaim{}, false, apperror.New(apperror.KindBadRequest, "agent.task.claim_invalid", nil, nil)
	}
	return s.repository.ClaimNext(ctx, workspace.String(), owner.String(), s.clock.Now().UTC(), leaseDuration)
}

func (s *AgentTaskRunApplicationService) ClaimNextForWorker(ctx context.Context, scope principalmodel.SystemScope, owner workerplatform.WorkerID, leaseDuration time.Duration) (agentrepository.AgentTaskClaim, bool, error) {
	if s == nil {
		return agentrepository.AgentTaskClaim{}, false, apperror.New(apperror.KindUnavailable, "agent.task.system_worker_unavailable", nil, nil)
	}
	repository, ok := s.repository.(agentrepository.AgentTaskRunSystemWorkerRepository)
	if !ok {
		return agentrepository.AgentTaskClaim{}, false, apperror.New(apperror.KindUnavailable, "agent.task.system_worker_unavailable", nil, nil)
	}
	if !scope.Valid() || scope.Kind != principalmodel.SystemScopeRuntimeGlobal || strings.TrimSpace(owner.String()) == "" || leaseDuration <= 0 {
		return agentrepository.AgentTaskClaim{}, false, apperror.New(apperror.KindBadRequest, "agent.task.claim_invalid", nil, nil)
	}
	return repository.ClaimNextAgentTaskRunForWorker(ctx, scope, owner.String(), s.clock.Now().UTC(), leaseDuration)
}

func (s *AgentTaskRunApplicationService) ListForWorker(ctx context.Context, scope principalmodel.SystemScope, filter agentrepository.AgentTaskRunFilter) ([]agentmodel.AgentTaskRun, error) {
	if s == nil {
		return nil, apperror.New(apperror.KindUnavailable, "agent.task.system_worker_unavailable", nil, nil)
	}
	repository, ok := s.repository.(agentrepository.AgentTaskRunSystemWorkerRepository)
	if !ok {
		return nil, apperror.New(apperror.KindUnavailable, "agent.task.system_worker_unavailable", nil, nil)
	}
	if !scope.Valid() || scope.Kind != principalmodel.SystemScopeRuntimeGlobal {
		return nil, apperror.New(apperror.KindBadRequest, "agent.task.query_invalid", nil, nil)
	}
	return repository.ListAgentTaskRunsForWorker(ctx, scope, filter)
}

func (s *AgentTaskRunApplicationService) Heartbeat(ctx context.Context, workspaceID, runID string, owner workerplatform.WorkerID, token workerplatform.FencingToken, leaseDuration time.Duration) (workerplatform.HeartbeatResult, error) {
	if s == nil || s.repository == nil {
		return workerplatform.HeartbeatResult{}, apperror.New(apperror.KindUnavailable, "agent.task.repository_unavailable", nil, nil)
	}
	workspace, workspaceErr := principalmodel.NewWorkspaceID(workspaceID)
	if workspaceErr != nil {
		return workerplatform.HeartbeatResult{}, apperror.New(apperror.KindBadRequest, "agent.task.heartbeat_invalid", nil, nil)
	}
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(owner.String()) == "" || !token.Valid() || leaseDuration <= 0 {
		return workerplatform.HeartbeatResult{}, apperror.New(apperror.KindBadRequest, "agent.task.heartbeat_invalid", nil, nil)
	}
	result, err := s.repository.Heartbeat(ctx, workspace.String(), runID, owner.String(), int64(token), s.clock.Now().UTC(), leaseDuration)
	if err != nil {
		return workerplatform.HeartbeatResult{}, err
	}
	state := workerplatform.HeartbeatRenewed
	if result.Lost {
		state = workerplatform.HeartbeatLeaseLost
	}
	return workerplatform.HeartbeatResult{State: state, Lease: workerplatform.Lease{Owner: workerplatform.WorkerID(result.Lease.Owner), Token: workerplatform.FencingToken(result.Lease.FencingToken), ExpiresAt: result.Lease.ExpiresAt}}, nil
}

type AgentTaskRunCompletion struct {
	Status         agentmodel.AgentTaskRunStatus
	Outcome        string
	Output         map[string]any
	ExternalRunID  string
	RawEvidenceRef string
	ErrorCode      string
	Evidence       agentmodel.AgentTaskExecutionEvidence
	Reconciliation agentmodel.AgentTaskReconciliation
}

func (s *AgentTaskRunApplicationService) WaitForApproval(ctx context.Context, run agentmodel.AgentTaskRun, owner workerplatform.WorkerID, token workerplatform.FencingToken, proposalID string, evidence agentmodel.AgentTaskExecutionEvidence) (agentmodel.AgentTaskRun, error) {
	if s == nil || s.repository == nil {
		return agentmodel.AgentTaskRun{}, apperror.New(apperror.KindUnavailable, "agent.task.repository_unavailable", nil, nil)
	}
	proposalID = strings.TrimSpace(proposalID)
	if run.Status != agentmodel.AgentTaskRunRunning || !run.Lease.Matches(owner.String(), int64(token)) || proposalID == "" {
		return agentmodel.AgentTaskRun{}, apperror.New(apperror.KindConflict, "agent.task.terminal_fence_rejected", nil, nil)
	}
	now := s.clock.Now().UTC()
	run.Status, run.Evidence, run.UpdatedAt, run.Revision = agentmodel.AgentTaskRunWaitingApproval, evidence, now, run.Revision+1
	run.Approval = &agentmodel.AgentTaskApproval{ProposalID: proposalID, Status: "pending", RequestedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
	if err := s.repository.SaveRunning(ctx, run, owner.String(), int64(token)); err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	return run, nil
}

type AgentTaskApprovalResolution struct {
	ProposalID string
	Decision   string
	Actor      string
	Reason     string
	Execution  map[string]any
}

func (s *AgentTaskRunApplicationService) ResolveApproval(ctx context.Context, workspaceID, runID string, resolution AgentTaskApprovalResolution) (agentmodel.AgentTaskRun, bool, error) {
	if s == nil || s.repository == nil {
		return agentmodel.AgentTaskRun{}, false, apperror.New(apperror.KindUnavailable, "agent.task.repository_unavailable", nil, nil)
	}
	run, found, err := s.Get(ctx, workspaceID, runID)
	if err != nil || !found {
		return run, false, err
	}
	resolution.ProposalID, resolution.Decision = strings.TrimSpace(resolution.ProposalID), strings.TrimSpace(resolution.Decision)
	if run.Status.Terminal() && run.Approval != nil && run.Approval.ProposalID == resolution.ProposalID && run.Approval.Decision == resolution.Decision {
		return run, true, nil
	}
	if run.Status != agentmodel.AgentTaskRunWaitingApproval || run.Approval == nil || run.Approval.ProposalID != resolution.ProposalID {
		return run, false, apperror.New(apperror.KindConflict, "agent.task.approval_state_conflict", nil, nil)
	}
	now, expectedRevision := s.clock.Now().UTC(), run.Revision
	run.Approval.Status, run.Approval.Decision, run.Approval.Actor, run.Approval.Reason = "resolved", resolution.Decision, strings.TrimSpace(resolution.Actor), strings.TrimSpace(resolution.Reason)
	run.Approval.Execution, run.Approval.ResolvedAt = cloneAgentTaskMap(resolution.Execution), &now
	run.Output = map[string]any{"proposal_id": resolution.ProposalID, "decision": resolution.Decision, "execution": cloneAgentTaskMap(resolution.Execution)}
	switch resolution.Decision {
	case "approved":
		if strings.TrimSpace(fmt.Sprint(resolution.Execution["status"])) == "failed" {
			run.Status, run.Outcome, run.LastErrorCode = agentmodel.AgentTaskRunFailed, "error", "agent.task.approval_execution_failed"
		} else {
			run.Status, run.Outcome = agentmodel.AgentTaskRunSucceeded, "success"
		}
	case "rejected", "returned":
		run.Status, run.Outcome = agentmodel.AgentTaskRunRejected, "rejected"
	case "timed_out":
		run.Status, run.Outcome, run.LastErrorCode = agentmodel.AgentTaskRunFailed, "error", "agent.task.approval_timeout"
	case "cancelled":
		run.Status, run.Outcome, run.LastErrorCode = agentmodel.AgentTaskRunCancelled, "error", "agent.task.approval_cancelled"
	default:
		return run, false, apperror.New(apperror.KindBadRequest, "agent.task.approval_decision_invalid", nil, nil)
	}
	run.UpdatedAt, run.CompletedAt, run.Revision = now, &now, run.Revision+1
	if len(run.Attempts) > 0 && run.Attempts[len(run.Attempts)-1].FinishedAt == nil {
		run.Attempts[len(run.Attempts)-1].FinishedAt = &now
	}
	if s.terminal != nil && run.ProcessID != "" && run.NodeInstanceID != "" {
		err = s.terminal.CommitAgentTaskApprovalTerminal(ctx, run)
	} else {
		err = s.repository.SaveWaitingApproval(ctx, run, expectedRevision)
	}
	if err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	return run, false, nil
}

func cloneAgentTaskMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}

func (s *AgentTaskRunApplicationService) FailAttempt(ctx context.Context, run agentmodel.AgentTaskRun, owner workerplatform.WorkerID, token workerplatform.FencingToken, errorClass, errorCode string, retryable bool, nextAttemptAt time.Time) (agentmodel.AgentTaskRun, error) {
	if s == nil || s.repository == nil {
		return agentmodel.AgentTaskRun{}, apperror.New(apperror.KindUnavailable, "agent.task.repository_unavailable", nil, nil)
	}
	if run.Status != agentmodel.AgentTaskRunRunning || !run.Lease.Matches(owner.String(), int64(token)) {
		return agentmodel.AgentTaskRun{}, apperror.New(apperror.KindConflict, "agent.task.terminal_fence_rejected", nil, nil)
	}
	now := s.clock.Now().UTC()
	run.LastErrorCode, run.UpdatedAt, run.Revision = strings.TrimSpace(errorCode), now, run.Revision+1
	if len(run.Attempts) > 0 {
		attempt := &run.Attempts[len(run.Attempts)-1]
		attempt.FinishedAt, attempt.ErrorClass, attempt.ErrorCode, attempt.Retryable = &now, strings.TrimSpace(errorClass), run.LastErrorCode, retryable
	}
	if retryable && run.Attempt < run.MaxAttempts && nextAttemptAt.After(now) {
		next := nextAttemptAt.UTC()
		run.Status, run.NextAttemptAt = agentmodel.AgentTaskRunRetryScheduled, &next
	} else {
		run.Status, run.Outcome, run.CompletedAt = agentmodel.AgentTaskRunDeadLetter, "error", &now
	}
	var err error
	if run.Status.Terminal() && s.terminal != nil && run.ProcessID != "" && run.NodeInstanceID != "" {
		run, err = s.commitWorkflowTerminal(ctx, run, owner, token)
	} else {
		err = s.repository.SaveRunning(ctx, run, owner.String(), int64(token))
	}
	if err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	return run, nil
}

func (s *AgentTaskRunApplicationService) Complete(ctx context.Context, run agentmodel.AgentTaskRun, owner workerplatform.WorkerID, token workerplatform.FencingToken, completion AgentTaskRunCompletion) (agentmodel.AgentTaskRun, error) {
	if s == nil || s.repository == nil {
		return agentmodel.AgentTaskRun{}, apperror.New(apperror.KindUnavailable, "agent.task.repository_unavailable", nil, nil)
	}
	if run.Status != agentmodel.AgentTaskRunRunning || !run.Lease.Matches(owner.String(), int64(token)) || !completion.Status.Terminal() {
		return agentmodel.AgentTaskRun{}, apperror.New(apperror.KindConflict, "agent.task.terminal_fence_rejected", nil, nil)
	}
	now := s.clock.Now().UTC()
	run.Status, run.Outcome, run.Output, run.RawEvidenceRef, run.LastErrorCode = completion.Status, strings.TrimSpace(completion.Outcome), completion.Output, strings.TrimSpace(completion.RawEvidenceRef), strings.TrimSpace(completion.ErrorCode)
	run.Evidence, run.Reconciliation, run.UpdatedAt, run.CompletedAt, run.Revision = completion.Evidence, completion.Reconciliation, now, &now, run.Revision+1
	if len(run.Attempts) > 0 {
		run.Attempts[len(run.Attempts)-1].FinishedAt = &now
		run.Attempts[len(run.Attempts)-1].ErrorCode = run.LastErrorCode
		run.Attempts[len(run.Attempts)-1].ExternalRunID = strings.TrimSpace(completion.ExternalRunID)
	}
	var err error
	if s.terminal != nil && run.ProcessID != "" && run.NodeInstanceID != "" {
		run, err = s.commitWorkflowTerminal(ctx, run, owner, token)
	} else {
		err = s.repository.SaveRunning(ctx, run, owner.String(), int64(token))
	}
	if err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	return run, nil
}

func (s *AgentTaskRunApplicationService) commitWorkflowTerminal(ctx context.Context, run agentmodel.AgentTaskRun, owner workerplatform.WorkerID, token workerplatform.FencingToken) (agentmodel.AgentTaskRun, error) {
	err := s.terminal.CommitAgentTaskTerminal(ctx, run, owner.String(), int64(token))
	if apperror.CodeOf(err) != "backend.workflow.agent_task_node_not_waiting" {
		return run, err
	}
	run.Status, run.Outcome, run.LastErrorCode = agentmodel.AgentTaskRunManualReview, "manual_review", "agent.task.workflow_terminal_conflict"
	run.Reconciliation = agentmodel.AgentTaskReconciliation{Required: true, State: "workflow_terminal_conflict", Reason: "workflow process completed before the retried Agent Task reached a terminal result"}
	if saveErr := s.repository.SaveRunning(ctx, run, owner.String(), int64(token)); saveErr != nil {
		return agentmodel.AgentTaskRun{}, saveErr
	}
	return run, nil
}

func (s *AgentTaskRunApplicationService) RequestCancel(ctx context.Context, workspaceID, runID, reason string) (agentmodel.AgentTaskRun, bool, error) {
	if s == nil || s.repository == nil {
		return agentmodel.AgentTaskRun{}, false, apperror.New(apperror.KindUnavailable, "agent.task.repository_unavailable", nil, nil)
	}
	run, replayed, err := s.repository.RequestCancel(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(runID), strings.TrimSpace(reason), s.clock.Now().UTC())
	if err != nil || run.Status != agentmodel.AgentTaskRunWaitingApproval || run.Approval == nil {
		return run, replayed, err
	}
	return s.ResolveApproval(ctx, run.WorkspaceID, run.ID, AgentTaskApprovalResolution{ProposalID: run.Approval.ProposalID, Decision: "cancelled", Actor: "system", Reason: run.CancellationReason})
}

func (s *AgentTaskRunApplicationService) ExpireApprovals(ctx context.Context, workspaceID string, before time.Time) (int, error) {
	runs, err := s.List(ctx, workspaceID, agentrepository.AgentTaskRunFilter{Statuses: []agentmodel.AgentTaskRunStatus{agentmodel.AgentTaskRunWaitingApproval}, Limit: 500})
	if err != nil {
		return 0, err
	}
	count := 0
	for _, run := range runs {
		if run.Approval == nil || run.Approval.ExpiresAt.IsZero() || run.Approval.ExpiresAt.After(before) {
			continue
		}
		if _, replayed, resolveErr := s.ResolveApproval(ctx, run.WorkspaceID, run.ID, AgentTaskApprovalResolution{ProposalID: run.Approval.ProposalID, Decision: "timed_out", Actor: "system", Reason: "approval deadline exceeded"}); resolveErr != nil {
			return count, resolveErr
		} else if !replayed {
			count++
		}
	}
	return count, nil
}

func (s *AgentTaskRunApplicationService) OverrideOutput(ctx context.Context, workspaceID, runID string, output map[string]any, actor principalmodel.Principal, reason string) (agentmodel.AgentTaskRun, error) {
	if s == nil || s.repository == nil {
		return agentmodel.AgentTaskRun{}, apperror.New(apperror.KindUnavailable, "agent.task.repository_unavailable", nil, nil)
	}
	reason = strings.TrimSpace(reason)
	if !actor.Known || strings.TrimSpace(actor.UserID) == "" || reason == "" {
		return agentmodel.AgentTaskRun{}, apperror.New(apperror.KindBadRequest, "agent.task.override_evidence_required", nil, nil)
	}
	if strings.TrimSpace(actor.WorkspaceID) != strings.TrimSpace(workspaceID) {
		return agentmodel.AgentTaskRun{}, apperror.New(apperror.KindBadRequest, "agent.task.override_evidence_required", nil, nil)
	}
	run, found, err := s.Get(ctx, workspaceID, runID)
	if err != nil || !found {
		return run, err
	}
	if !run.Status.Terminal() {
		return run, apperror.New(apperror.KindConflict, "agent.task.override_terminal_required", nil, nil)
	}
	now, expectedRevision := s.clock.Now().UTC(), run.Revision
	if s.audit == nil {
		return run, apperror.New(apperror.KindUnavailable, "agent.task.override_audit_unavailable", nil, nil)
	}
	auditRef := "agent-task-override:" + run.ID + ":" + strconv.FormatInt(expectedRevision+1, 10)
	if err := s.audit.AppendAudit(ctx, auditcontract.AuditAppendRequest{Event: "agent_task_output_overridden", ObjectKey: "agent_task_run", RecordID: run.ID, Principal: actor, Summary: "Agent Task output manually overridden", Before: map[string]any{"output_hash": agentStableHash(run.Output)}, After: map[string]any{"output_hash": agentStableHash(output)}, Metadata: map[string]any{"audit_ref": auditRef, "reason": reason, "task_key": run.TaskKey, "task_version": run.TaskVersion, "process_id": run.ProcessID}}); err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	run.ManualOverrides = append(run.ManualOverrides, agentmodel.AgentTaskManualOverride{OriginalOutput: cloneAgentTaskMap(run.Output), NewOutput: cloneAgentTaskMap(output), Actor: actor.UserID, Reason: reason, AuditRef: auditRef, CreatedAt: now})
	run.Evidence.AuditRefs = append(run.Evidence.AuditRefs, auditRef)
	run.Output, run.UpdatedAt, run.Revision = cloneAgentTaskMap(output), now, run.Revision+1
	if err := s.repository.SaveTerminalOverride(ctx, run, expectedRevision); err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	return run, nil
}
