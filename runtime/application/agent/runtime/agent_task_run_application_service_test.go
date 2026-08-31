package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type agentTaskClock struct{ now time.Time }

func (clock agentTaskClock) Now() time.Time { return clock.now }

type agentTaskRunRepositoryStub struct {
	created                                                                       agentmodel.AgentTaskRun
	saved                                                                         agentmodel.AgentTaskRun
	claim                                                                         agentpersistence.AgentTaskClaim
	found                                                                         bool
	heartbeat                                                                     workerplatform.HeartbeatResult
	listed                                                                        []agentmodel.AgentTaskRun
	current                                                                       agentmodel.AgentTaskRun
	createErr, getErr, listErr, claimErr, heartbeatErr                            error
	saveRunningErr, saveApprovalErr, saveOverrideErr, saveOperationErr, cancelErr error
	createReplayed, cancelReplayed                                                bool
	getCalls, getErrAfter, missingAfter                                           int
	directCalls                                                                   int
	directWorkspaceID, directRunID                                                string
}

type agentTaskSystemRepositoryStub struct {
	*agentTaskRunRepositoryStub
	systemClaim agentpersistence.AgentTaskClaim
	systemRuns  []agentmodel.AgentTaskRun
	systemFound bool
	systemErr   error
}

func (s *agentTaskSystemRepositoryStub) ClaimNextAgentTaskRunForWorker(context.Context, agentpersistence.SystemScope, string, time.Time, time.Duration) (agentpersistence.AgentTaskClaim, bool, error) {
	return s.systemClaim, s.systemFound, s.systemErr
}

func (s *agentTaskSystemRepositoryStub) ListAgentTaskRunsForWorker(context.Context, agentpersistence.SystemScope, agentpersistence.AgentTaskRunFilter) ([]agentmodel.AgentTaskRun, error) {
	return s.systemRuns, s.systemErr
}

type agentTaskTerminalCommitterStub struct {
	run   agentmodel.AgentTaskRun
	owner string
	token int64
	err   error
}

type agentTaskAuditStub struct {
	request auditcontract.AuditAppendRequest
	err     error
}

func (s *agentTaskAuditStub) AppendAudit(_ context.Context, request auditcontract.AuditAppendRequest) error {
	s.request = request
	return s.err
}

func (s *agentTaskTerminalCommitterStub) CommitAgentTaskTerminal(_ context.Context, run agentmodel.AgentTaskRun, owner string, token int64) error {
	s.run, s.owner, s.token = run, owner, token
	return s.err
}
func (s *agentTaskTerminalCommitterStub) CommitAgentTaskApprovalTerminal(_ context.Context, run agentmodel.AgentTaskRun) error {
	s.run, s.owner, s.token = run, "", 0
	return s.err
}

func (s *agentTaskRunRepositoryStub) Create(_ context.Context, run agentmodel.AgentTaskRun) (agentmodel.AgentTaskRun, bool, error) {
	s.created = run
	return run, s.createReplayed, s.createErr
}
func (s *agentTaskRunRepositoryStub) Get(context.Context, string, string) (agentmodel.AgentTaskRun, bool, error) {
	s.getCalls++
	if s.getErrAfter > 0 && s.getCalls >= s.getErrAfter {
		return agentmodel.AgentTaskRun{}, false, s.getErr
	}
	if s.getErrAfter > 0 {
		return s.current, s.current.ID != "", nil
	}
	if s.missingAfter > 0 && s.getCalls >= s.missingAfter {
		return agentmodel.AgentTaskRun{}, false, nil
	}
	return s.current, s.current.ID != "", s.getErr
}
func (s *agentTaskRunRepositoryStub) List(context.Context, string, agentpersistence.AgentTaskRunFilter) ([]agentmodel.AgentTaskRun, error) {
	return append([]agentmodel.AgentTaskRun(nil), s.listed...), s.listErr
}
func (s *agentTaskRunRepositoryStub) ClaimNext(context.Context, string, string, time.Time, time.Duration) (agentpersistence.AgentTaskClaim, bool, error) {
	return s.claim, s.found, s.claimErr
}
func (s *agentTaskRunRepositoryStub) ClaimAgentTaskRun(_ context.Context, workspaceID, runID, _ string, _ time.Time, _ time.Duration) (agentpersistence.AgentTaskClaim, bool, error) {
	s.directCalls++
	s.directWorkspaceID, s.directRunID = workspaceID, runID
	return s.claim, s.found, s.claimErr
}
func (s *agentTaskRunRepositoryStub) Heartbeat(context.Context, string, string, string, int64, time.Time, time.Duration) (agentpersistence.AgentTaskHeartbeatResult, error) {
	return agentpersistence.AgentTaskHeartbeatResult{Lost: s.heartbeat.State == workerplatform.HeartbeatLeaseLost, Lease: agentmodel.AgentTaskLease{Owner: s.heartbeat.Lease.Owner.String(), FencingToken: int64(s.heartbeat.Lease.Token), ExpiresAt: s.heartbeat.Lease.ExpiresAt}}, s.heartbeatErr
}
func (s *agentTaskRunRepositoryStub) SaveRunning(_ context.Context, run agentmodel.AgentTaskRun, _ string, _ int64) error {
	s.saved, s.current = run, run
	return s.saveRunningErr
}
func (s *agentTaskRunRepositoryStub) SaveWaitingApproval(_ context.Context, run agentmodel.AgentTaskRun, _ int64) error {
	s.saved, s.current = run, run
	return s.saveApprovalErr
}
func (s *agentTaskRunRepositoryStub) SaveTerminalOverride(_ context.Context, run agentmodel.AgentTaskRun, _ int64) error {
	s.saved, s.current = run, run
	return s.saveOverrideErr
}
func (s *agentTaskRunRepositoryStub) SaveOperationalTransition(_ context.Context, run agentmodel.AgentTaskRun, _ agentmodel.AgentTaskRunStatus, _ int64) error {
	s.saved, s.current = run, run
	return s.saveOperationErr
}
func (s *agentTaskRunRepositoryStub) RequestCancel(_ context.Context, _, _ string, reason string, now time.Time) (agentmodel.AgentTaskRun, bool, error) {
	if s.current.ID == "" {
		return agentmodel.AgentTaskRun{Status: agentmodel.AgentTaskRunCancelled}, s.cancelReplayed, s.cancelErr
	}
	s.current.CancelRequestedAt, s.current.CancellationReason = &now, reason
	s.current.Revision++
	return s.current, s.cancelReplayed, s.cancelErr
}

func runningAgentTaskRun(now time.Time) (agentmodel.AgentTaskRun, workerplatform.WorkerID, workerplatform.FencingToken) {
	owner, token := workerplatform.WorkerID("worker-a"), workerplatform.FencingToken(4)
	return agentmodel.AgentTaskRun{
		ID: "run-a", WorkspaceID: "workspace-a", TaskKey: "task", TaskVersion: "v1", Status: agentmodel.AgentTaskRunRunning,
		Attempt: 1, MaxAttempts: 2, Revision: 2, Lease: agentmodel.AgentTaskLease{Owner: owner.String(), FencingToken: int64(token), ExpiresAt: now.Add(time.Minute)},
		Attempts: []agentmodel.AgentTaskAttempt{{Number: 1, StartedAt: now.Add(-time.Minute)}},
	}, owner, token
}

func TestAgentTaskRunApplicationCreateAndInputValidation(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	repository := &agentTaskRunRepositoryStub{}
	audit := &agentTaskAuditStub{}
	service := NewAgentTaskRunApplicationServiceWithAudit(repository, agentTaskClock{now: now}, nil, audit)
	wakeups := []AgentTaskLocator{}
	BindAgentTaskRunWakeup(service, func(locator AgentTaskLocator) { wakeups = append(wakeups, locator) })
	run, replayed, err := service.Create(t.Context(), agentmodel.AgentTaskRun{ID: " run ", WorkspaceID: " workspace ", TaskKey: " task ", TaskVersion: " v1 ", IdempotencyKey: " idem ", MaxAttempts: 2})
	if err != nil || replayed || run.Status != agentmodel.AgentTaskRunPending || run.Revision != 1 || !run.CreatedAt.Equal(now) || repository.created.ID != "run" || len(wakeups) != 1 || wakeups[0] != (AgentTaskLocator{WorkspaceID: "workspace", RunID: "run"}) {
		t.Fatalf("run=%#v replayed=%v err=%v", run, replayed, err)
	}
	repository.createErr = errors.New("create failed")
	if _, _, err := service.Create(t.Context(), agentmodel.AgentTaskRun{ID: "failed", WorkspaceID: "workspace", TaskKey: "task", TaskVersion: "v1", IdempotencyKey: "failed", MaxAttempts: 1}); err == nil || len(wakeups) != 1 {
		t.Fatalf("failed create emitted wakeup: wakeups=%#v err=%v", wakeups, err)
	}
	repository.createErr = nil
	if _, _, err := service.Create(t.Context(), agentmodel.AgentTaskRun{ID: "run", WorkspaceID: "workspace", TaskKey: "task", TaskVersion: "v1", IdempotencyKey: "idem"}); apperror.CodeOf(err) != "agent.task.max_attempts_invalid" {
		t.Fatalf("max attempts err=%v", err)
	}
	if _, _, err := (*AgentTaskRunApplicationService)(nil).Create(t.Context(), agentmodel.AgentTaskRun{}); apperror.CodeOf(err) != "agent.task.repository_unavailable" {
		t.Fatalf("nil service err=%v", err)
	}
}

func TestAgentTaskRunApplicationTransitionsRequireLiveFence(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	repository := &agentTaskRunRepositoryStub{}
	service := NewAgentTaskRunApplicationService(repository, agentTaskClock{now: now})
	run, owner, token := runningAgentTaskRun(now)
	if _, err := service.WaitForApproval(t.Context(), run, owner, token+1, "proposal", agentmodel.AgentTaskExecutionEvidence{}); apperror.CodeOf(err) != "agent.task.terminal_fence_rejected" {
		t.Fatalf("stale wait err=%v", err)
	}
	waiting, err := service.WaitForApproval(t.Context(), run, owner, token, "proposal", agentmodel.AgentTaskExecutionEvidence{TaskVersion: "v1"})
	if err != nil || waiting.Status != agentmodel.AgentTaskRunWaitingApproval || waiting.Approval == nil || waiting.Approval.ProposalID != "proposal" || repository.saved.Evidence.TaskVersion != "v1" {
		t.Fatalf("waiting=%#v err=%v", waiting, err)
	}
	retryAt := now.Add(time.Minute)
	retry, err := service.FailAttempt(t.Context(), run, owner, token, "provider_5xx", "provider.unavailable", true, retryAt)
	if err != nil || retry.Status != agentmodel.AgentTaskRunRetryScheduled || retry.NextAttemptAt == nil || retry.Attempts[0].ErrorClass != "provider_5xx" {
		t.Fatalf("retry=%#v err=%v", retry, err)
	}
	run.Attempt = run.MaxAttempts
	dead, err := service.FailAttempt(t.Context(), run, owner, token, "timeout", "provider.timeout", true, retryAt)
	if err != nil || dead.Status != agentmodel.AgentTaskRunDeadLetter || dead.CompletedAt == nil {
		t.Fatalf("dead=%#v err=%v", dead, err)
	}
	completed, err := service.Complete(t.Context(), runningAgentTaskRunValue(now), owner, token, AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunSucceeded, Outcome: "success", Output: map[string]any{"score": 90}})
	if err != nil || completed.Status != agentmodel.AgentTaskRunSucceeded || completed.CompletedAt == nil {
		t.Fatalf("completed=%#v err=%v", completed, err)
	}
}

func TestAgentTaskRunApplicationUsesAtomicTerminalCommitterForWorkflowRun(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	repository, terminal := &agentTaskRunRepositoryStub{}, &agentTaskTerminalCommitterStub{}
	service := NewAgentTaskRunApplicationService(repository, agentTaskClock{now: now}, terminal)
	run, owner, token := runningAgentTaskRun(now)
	run.ProcessID, run.NodeInstanceID = "process", "node"
	completed, err := service.Complete(t.Context(), run, owner, token, AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunSucceeded, Outcome: "success"})
	if err != nil || terminal.run.ID != run.ID || terminal.run.Status != agentmodel.AgentTaskRunSucceeded || terminal.owner != owner.String() || terminal.token != int64(token) || repository.saved.ID != "" {
		t.Fatalf("completed=%#v terminal=%#v saved=%#v err=%v", completed, terminal, repository.saved, err)
	}
	run.Attempt = run.MaxAttempts
	dead, err := service.FailAttempt(t.Context(), run, owner, token, "timeout", "provider.timeout", true, now.Add(time.Minute))
	if err != nil || dead.Status != agentmodel.AgentTaskRunDeadLetter || terminal.run.Status != agentmodel.AgentTaskRunDeadLetter {
		t.Fatalf("dead=%#v terminal=%#v err=%v", dead, terminal, err)
	}
}

func TestAgentTaskRunApplicationQuarantinesOrphanedWorkflowTerminalResult(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	repository := &agentTaskRunRepositoryStub{}
	terminal := &agentTaskTerminalCommitterStub{err: apperror.New(apperror.KindConflict, "backend.workflow.agent_task_node_not_waiting", nil, nil)}
	service := NewAgentTaskRunApplicationService(repository, agentTaskClock{now: now}, terminal)
	run, owner, token := runningAgentTaskRun(now)
	run.ProcessID, run.NodeInstanceID = "completed-process", "terminal-node"
	quarantined, err := service.Complete(t.Context(), run, owner, token, AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunSucceeded, Outcome: "success", Output: map[string]any{"decision": "agreed"}})
	if err != nil || quarantined.Status != agentmodel.AgentTaskRunManualReview || quarantined.LastErrorCode != "agent.task.workflow_terminal_conflict" || !quarantined.Reconciliation.Required || quarantined.Reconciliation.State != "workflow_terminal_conflict" {
		t.Fatalf("quarantined=%#v err=%v", quarantined, err)
	}
	if repository.saved.Status != agentmodel.AgentTaskRunManualReview || terminal.run.Status != agentmodel.AgentTaskRunSucceeded {
		t.Fatalf("saved=%#v terminal=%#v", repository.saved, terminal.run)
	}
	wantErr := errors.New("save quarantined terminal")
	repository.saveRunningErr = wantErr
	if _, err := service.Complete(t.Context(), run, owner, token, AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunSucceeded}); !errors.Is(err, wantErr) {
		t.Fatalf("quarantine save error=%v", err)
	}
}

func TestAgentTaskRunApplicationResolvesApprovalOutcomesAndReplay(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		decision, executionStatus string
		status                    agentmodel.AgentTaskRunStatus
		outcome, code             string
	}{
		{decision: "approved", executionStatus: "applied", status: agentmodel.AgentTaskRunSucceeded, outcome: "success"},
		{decision: "approved", executionStatus: "failed", status: agentmodel.AgentTaskRunFailed, outcome: "error", code: "agent.task.approval_execution_failed"},
		{decision: "rejected", status: agentmodel.AgentTaskRunRejected, outcome: "rejected"},
		{decision: "returned", status: agentmodel.AgentTaskRunRejected, outcome: "rejected"},
		{decision: "timed_out", status: agentmodel.AgentTaskRunFailed, outcome: "error", code: "agent.task.approval_timeout"},
		{decision: "cancelled", status: agentmodel.AgentTaskRunCancelled, outcome: "error", code: "agent.task.approval_cancelled"},
	}
	for _, test := range tests {
		t.Run(test.decision+test.executionStatus, func(t *testing.T) {
			run, _, _ := runningAgentTaskRun(now)
			run.Status, run.Revision = agentmodel.AgentTaskRunWaitingApproval, 5
			run.Approval = &agentmodel.AgentTaskApproval{ProposalID: "proposal", Status: "pending", RequestedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}
			repository := &agentTaskRunRepositoryStub{current: run}
			service := NewAgentTaskRunApplicationService(repository, agentTaskClock{now: now})
			resolved, replayed, err := service.ResolveApproval(t.Context(), run.WorkspaceID, run.ID, AgentTaskApprovalResolution{ProposalID: "proposal", Decision: test.decision, Actor: "reviewer", Reason: "reviewed", Execution: map[string]any{"status": test.executionStatus}})
			if err != nil || replayed || resolved.Status != test.status || resolved.Outcome != test.outcome || resolved.LastErrorCode != test.code || resolved.Approval.Actor != "reviewer" || resolved.CompletedAt == nil {
				t.Fatalf("resolved=%#v replayed=%v err=%v", resolved, replayed, err)
			}
			replayedRun, replayed, err := service.ResolveApproval(t.Context(), run.WorkspaceID, run.ID, AgentTaskApprovalResolution{ProposalID: "proposal", Decision: test.decision})
			if err != nil || !replayed || replayedRun.Status != test.status {
				t.Fatalf("replay=%#v replayed=%v err=%v", replayedRun, replayed, err)
			}
		})
	}
}

func TestAgentTaskRunApprovalCancelExpiryAndManualOverride(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	waiting, _, _ := runningAgentTaskRun(now)
	waiting.Status, waiting.Revision = agentmodel.AgentTaskRunWaitingApproval, 5
	waiting.Approval = &agentmodel.AgentTaskApproval{ProposalID: "proposal", Status: "pending", RequestedAt: now.Add(-25 * time.Hour), ExpiresAt: now.Add(-time.Hour)}
	repository := &agentTaskRunRepositoryStub{current: waiting, listed: []agentmodel.AgentTaskRun{waiting}}
	audit := &agentTaskAuditStub{}
	service := NewAgentTaskRunApplicationServiceWithAudit(repository, agentTaskClock{now: now}, nil, audit)
	if count, err := service.ExpireApprovals(t.Context(), waiting.WorkspaceID, now); err != nil || count != 1 || repository.current.Status != agentmodel.AgentTaskRunFailed {
		t.Fatalf("expire count=%d current=%#v err=%v", count, repository.current, err)
	}

	repository.current = waiting
	cancelled, replayed, err := service.RequestCancel(t.Context(), waiting.WorkspaceID, waiting.ID, "operator cancelled")
	if err != nil || replayed || cancelled.Status != agentmodel.AgentTaskRunCancelled || cancelled.CancellationReason != "operator cancelled" {
		t.Fatalf("cancelled=%#v replayed=%v err=%v", cancelled, replayed, err)
	}

	repository.current = cancelled
	if _, err := service.OverrideOutput(t.Context(), waiting.WorkspaceID, waiting.ID, map[string]any{"corrected": true}, principalmodel.Principal{}, "reason"); apperror.CodeOf(err) != "agent.task.override_evidence_required" {
		t.Fatalf("missing actor err=%v", err)
	}
	actor := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: waiting.WorkspaceID, UserID: "operator"}}, accessfixture.Bundle{Key: "ops"})
	overridden, err := service.OverrideOutput(t.Context(), waiting.WorkspaceID, waiting.ID, map[string]any{"corrected": true}, actor, "manual correction")
	if err != nil || overridden.Output["corrected"] != true || len(overridden.ManualOverrides) != 1 || overridden.ManualOverrides[0].OriginalOutput["decision"] != "cancelled" || overridden.Evidence.AuditRefs[0] == "" || audit.request.Event != "agent_task_output_overridden" || audit.request.Metadata["audit_ref"] != overridden.Evidence.AuditRefs[0] {
		t.Fatalf("overridden=%#v err=%v", overridden, err)
	}
}

func TestAgentTaskRunApprovalResolutionUsesAtomicWorkflowCommitter(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	run, _, _ := runningAgentTaskRun(now)
	run.Status, run.Revision, run.ProcessID, run.NodeInstanceID = agentmodel.AgentTaskRunWaitingApproval, 5, "process", "node"
	run.Approval = &agentmodel.AgentTaskApproval{ProposalID: "proposal", Status: "pending", RequestedAt: now, ExpiresAt: now.Add(time.Hour)}
	repository, terminal := &agentTaskRunRepositoryStub{current: run}, &agentTaskTerminalCommitterStub{}
	service := NewAgentTaskRunApplicationService(repository, agentTaskClock{now: now}, terminal)
	resolved, _, err := service.ResolveApproval(t.Context(), run.WorkspaceID, run.ID, AgentTaskApprovalResolution{ProposalID: "proposal", Decision: "approved", Execution: map[string]any{"status": "applied"}})
	if err != nil || resolved.Status != agentmodel.AgentTaskRunSucceeded || terminal.run.Status != agentmodel.AgentTaskRunSucceeded || terminal.owner != "" || repository.saved.ID != "" {
		t.Fatalf("resolved=%#v terminal=%#v saved=%#v err=%v", resolved, terminal, repository.saved, err)
	}
}

func TestAgentTaskRunManualOverrideRequiresDurableAudit(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	run, _, _ := runningAgentTaskRun(now)
	run.Status, run.Outcome, run.Output = agentmodel.AgentTaskRunSucceeded, "success", map[string]any{"original": true}
	actor := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: run.WorkspaceID, UserID: "operator"}}, accessfixture.Bundle{Key: "ops"})
	repository := &agentTaskRunRepositoryStub{current: run}
	if _, err := NewAgentTaskRunApplicationService(repository, agentTaskClock{now: now}).OverrideOutput(t.Context(), run.WorkspaceID, run.ID, map[string]any{}, actor, "fix"); apperror.CodeOf(err) != "agent.task.override_audit_unavailable" || repository.saved.ID != "" {
		t.Fatalf("missing audit err=%v saved=%#v", err, repository.saved)
	}
	auditErr := errors.New("audit unavailable")
	audit := &agentTaskAuditStub{err: auditErr}
	service := NewAgentTaskRunApplicationServiceWithAudit(repository, agentTaskClock{now: now}, nil, audit)
	if _, err := service.OverrideOutput(t.Context(), run.WorkspaceID, run.ID, map[string]any{}, actor, "fix"); !errors.Is(err, auditErr) || repository.saved.ID != "" {
		t.Fatalf("audit failure err=%v saved=%#v", err, repository.saved)
	}
}

func TestAgentTaskRunOperationsAreAuditedIdempotentAndStateChecked(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	run, _, _ := runningAgentTaskRun(now)
	run.Status, run.Outcome, run.CompletedAt, run.Revision = agentmodel.AgentTaskRunDeadLetter, "error", &now, 7
	repository, audit := &agentTaskRunRepositoryStub{current: run}, &agentTaskAuditStub{}
	service := NewAgentTaskRunApplicationServiceWithAudit(repository, agentTaskClock{now: now.Add(time.Minute)}, nil, audit)
	actor := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: run.WorkspaceID, UserID: "operator"}}, accessfixture.Bundle{Key: "ops"})
	retried, replayed, err := service.Operate(t.Context(), run.WorkspaceID, run.ID, "retry", "op-1", "incident repaired", actor)
	if err != nil || replayed || retried.Status != agentmodel.AgentTaskRunPending || retried.Reconciliation.State != "force_restart" || audit.request.Event != "agent_task_retry" || len(retried.Operations) != 1 || retried.Operations[0].AuditRef == "" {
		t.Fatalf("retried=%#v replayed=%v audit=%#v err=%v", retried, replayed, audit.request, err)
	}
	if replay, wasReplay, replayErr := service.Operate(t.Context(), run.WorkspaceID, run.ID, "retry", "op-1", "incident repaired", actor); replayErr != nil || !wasReplay || replay.ID != run.ID {
		t.Fatalf("replay=%#v replayed=%v err=%v", replay, wasReplay, replayErr)
	}
	if _, _, conflict := service.Operate(t.Context(), run.WorkspaceID, run.ID, "resolve", "op-1", "different", actor); apperror.CodeOf(conflict) != "backend.idempotency.key_reused" {
		t.Fatalf("key reuse err=%v", conflict)
	}

	reconcile := run
	reconcile.Reconciliation = agentmodel.AgentTaskReconciliation{Required: true, ExternalRunID: "provider-1", State: "poll_required"}
	reconcile.Operations = nil
	repository.current = reconcile
	reconciled, _, err := service.Operate(t.Context(), run.WorkspaceID, run.ID, "reconcile", "op-2", "poll provider", actor)
	if err != nil || reconciled.Status != agentmodel.AgentTaskRunPending || reconciled.Reconciliation.State != "poll_required" {
		t.Fatalf("reconciled=%#v err=%v", reconciled, err)
	}
	repository.current = reconcile
	resolved, _, err := service.Operate(t.Context(), run.WorkspaceID, run.ID, "resolve", "op-3", "accepted for manual review", actor)
	if err != nil || resolved.Status != agentmodel.AgentTaskRunManualReview || resolved.Reconciliation.Required {
		t.Fatalf("resolved=%#v err=%v", resolved, err)
	}
	if _, _, invalid := service.Operate(t.Context(), run.WorkspaceID, run.ID, "invalid", "op-4", "reason", actor); apperror.CodeOf(invalid) != "agent.task.operation_invalid" {
		t.Fatalf("invalid operation err=%v", invalid)
	}
}

func TestAgentTaskRunOperationRequiresIdentityKeyReasonAndAudit(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	run, _, _ := runningAgentTaskRun(now)
	run.Status = agentmodel.AgentTaskRunFailed
	repository := &agentTaskRunRepositoryStub{current: run}
	actor := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: run.WorkspaceID, UserID: "operator"}}
	if _, _, err := NewAgentTaskRunApplicationService(repository, agentTaskClock{now: now}).Operate(t.Context(), run.WorkspaceID, run.ID, "retry", "key", "reason", actor); apperror.CodeOf(err) != "agent.task.operation_unavailable" {
		t.Fatalf("missing audit err=%v", err)
	}
	service := NewAgentTaskRunApplicationServiceWithAudit(repository, agentTaskClock{now: now}, nil, &agentTaskAuditStub{})
	for _, input := range []struct {
		key, reason string
		actor       principalmodel.Principal
	}{{"", "reason", actor}, {"key", "", actor}, {"key", "reason", principalmodel.Principal{}}} {
		if _, _, err := service.Operate(t.Context(), run.WorkspaceID, run.ID, "retry", input.key, input.reason, input.actor); apperror.CodeOf(err) != "agent.task.operation_evidence_required" {
			t.Fatalf("input=%#v err=%v", input, err)
		}
	}
}

func TestAgentTaskRunOperationBoundaryMatrix(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	run, _, _ := runningAgentTaskRun(now)
	run.Status = agentmodel.AgentTaskRunFailed
	actor := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: run.WorkspaceID, UserID: "operator"}}
	wantErr := errors.New("operation failure")
	validService := func(repository *agentTaskRunRepositoryStub, audit *agentTaskAuditStub) *AgentTaskRunApplicationService {
		return NewAgentTaskRunApplicationServiceWithAudit(repository, agentTaskClock{now: now}, nil, audit)
	}
	if _, _, err := (*AgentTaskRunApplicationService)(nil).Operate(t.Context(), run.WorkspaceID, run.ID, "retry", "key", "reason", actor); apperror.CodeOf(err) != "agent.task.operation_unavailable" {
		t.Fatalf("nil operation=%v", err)
	}
	if _, _, err := (&AgentTaskRunApplicationService{audit: &agentTaskAuditStub{}}).Operate(t.Context(), run.WorkspaceID, run.ID, "retry", "key", "reason", actor); apperror.CodeOf(err) != "agent.task.operation_unavailable" {
		t.Fatalf("missing repository=%v", err)
	}
	wrongWorkspace := actor
	wrongWorkspace.WorkspaceID = "other"
	if _, _, err := validService(&agentTaskRunRepositoryStub{current: run}, &agentTaskAuditStub{}).Operate(t.Context(), run.WorkspaceID, run.ID, "retry", "key", "reason", wrongWorkspace); apperror.CodeOf(err) != "agent.task.operation_evidence_required" {
		t.Fatalf("workspace evidence=%v", err)
	}
	missingUser := actor
	missingUser.UserID = ""
	if _, _, err := validService(&agentTaskRunRepositoryStub{current: run}, &agentTaskAuditStub{}).Operate(t.Context(), run.WorkspaceID, run.ID, "retry", "key", "reason", missingUser); apperror.CodeOf(err) != "agent.task.operation_evidence_required" {
		t.Fatalf("user evidence=%v", err)
	}
	if _, _, err := validService(&agentTaskRunRepositoryStub{getErr: wantErr}, &agentTaskAuditStub{}).Operate(t.Context(), run.WorkspaceID, run.ID, "retry", "key", "reason", actor); !errors.Is(err, wantErr) {
		t.Fatalf("get error=%v", err)
	}
	if _, _, err := validService(&agentTaskRunRepositoryStub{}, &agentTaskAuditStub{}).Operate(t.Context(), run.WorkspaceID, run.ID, "retry", "key", "reason", actor); err != nil {
		t.Fatalf("missing run should remain non-disclosing: %v", err)
	}
	for name, status := range map[string]agentmodel.AgentTaskRunStatus{
		"failed": agentmodel.AgentTaskRunFailed, "dead": agentmodel.AgentTaskRunDeadLetter, "cancelled": agentmodel.AgentTaskRunCancelled,
	} {
		t.Run("retry "+name, func(t *testing.T) {
			candidate := run
			candidate.Status = status
			if got, _, err := validService(&agentTaskRunRepositoryStub{current: candidate}, &agentTaskAuditStub{}).Operate(t.Context(), run.WorkspaceID, run.ID, "retry", "key-"+name, "reason", actor); err != nil || got.Status != agentmodel.AgentTaskRunPending {
				t.Fatalf("run=%#v err=%v", got, err)
			}
		})
	}
	for _, status := range []agentmodel.AgentTaskRunStatus{agentmodel.AgentTaskRunPending, agentmodel.AgentTaskRunRunning} {
		candidate := run
		candidate.Status = status
		if _, _, err := validService(&agentTaskRunRepositoryStub{current: candidate}, &agentTaskAuditStub{}).Operate(t.Context(), run.WorkspaceID, run.ID, "retry", "retry-invalid-"+string(status), "reason", actor); apperror.CodeOf(err) != "agent.task.retry_state_invalid" {
			t.Fatalf("retry status %s=%v", status, err)
		}
	}
	for name, reconciliation := range map[string]agentmodel.AgentTaskReconciliation{
		"not required":     {},
		"missing external": {Required: true},
	} {
		candidate := run
		candidate.Reconciliation = reconciliation
		if _, _, err := validService(&agentTaskRunRepositoryStub{current: candidate}, &agentTaskAuditStub{}).Operate(t.Context(), run.WorkspaceID, run.ID, "reconcile", "rec-"+name, "reason", actor); apperror.CodeOf(err) != "agent.task.reconcile_state_invalid" {
			t.Fatalf("%s=%v", name, err)
		}
	}
	for name, candidate := range map[string]agentmodel.AgentTaskRun{
		"nonterminal": run,
		"not required": func() agentmodel.AgentTaskRun {
			value := run
			value.Status = agentmodel.AgentTaskRunFailed
			value.Reconciliation = agentmodel.AgentTaskReconciliation{}
			return value
		}(),
	} {
		if name == "nonterminal" {
			candidate.Status = agentmodel.AgentTaskRunRunning
			candidate.Reconciliation.Required = true
		}
		if _, _, err := validService(&agentTaskRunRepositoryStub{current: candidate}, &agentTaskAuditStub{}).Operate(t.Context(), run.WorkspaceID, run.ID, "resolve", "resolve-"+name, "reason", actor); apperror.CodeOf(err) != "agent.task.resolve_state_invalid" {
			t.Fatalf("%s=%v", name, err)
		}
	}
	if _, _, err := validService(&agentTaskRunRepositoryStub{current: run}, &agentTaskAuditStub{err: wantErr}).Operate(t.Context(), run.WorkspaceID, run.ID, "retry", "audit", "reason", actor); !errors.Is(err, wantErr) {
		t.Fatalf("audit error=%v", err)
	}
	if _, _, err := validService(&agentTaskRunRepositoryStub{current: run, saveOperationErr: wantErr}, &agentTaskAuditStub{}).Operate(t.Context(), run.WorkspaceID, run.ID, "retry", "save", "reason", actor); !errors.Is(err, wantErr) {
		t.Fatalf("save error=%v", err)
	}
}

func runningAgentTaskRunValue(now time.Time) agentmodel.AgentTaskRun {
	run, _, _ := runningAgentTaskRun(now)
	return run
}

func TestAgentTaskRunApplicationClaimHeartbeatAndCancelValidation(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	repository := &agentTaskRunRepositoryStub{found: true, claim: agentpersistence.AgentTaskClaim{Run: agentmodel.AgentTaskRun{ID: "run"}}}
	service := NewAgentTaskRunApplicationService(repository, agentTaskClock{now: now})
	if _, _, err := service.ClaimNext(t.Context(), "", "", 0); apperror.CodeOf(err) != "agent.task.claim_invalid" {
		t.Fatalf("claim validation err=%v", err)
	}
	if claim, found, err := service.ClaimNext(t.Context(), "workspace", workerplatform.WorkerID("worker"), time.Minute); err != nil || !found || claim.Run.ID != "run" {
		t.Fatalf("claim=%#v found=%v err=%v", claim, found, err)
	}
	if _, err := service.Heartbeat(t.Context(), "workspace", "run", workerplatform.WorkerID("worker"), 0, time.Minute); apperror.CodeOf(err) != "agent.task.heartbeat_invalid" {
		t.Fatalf("heartbeat validation err=%v", err)
	}
	if result, err := service.Heartbeat(t.Context(), "workspace", "run", workerplatform.WorkerID("worker"), 1, time.Minute); err != nil || result.State != workerplatform.HeartbeatRenewed {
		t.Fatalf("heartbeat=%#v err=%v", result, err)
	}
	if run, replayed, err := service.RequestCancel(t.Context(), " workspace ", " run ", " stop "); err != nil || replayed || run.Status != agentmodel.AgentTaskRunCancelled {
		t.Fatalf("cancel=%#v replayed=%v err=%v", run, replayed, err)
	}
}

func TestAgentTaskRunApplicationServiceBoundaryMatrix(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	wantErr := errors.New("repository failure")
	valid := agentmodel.AgentTaskRun{ID: "run", WorkspaceID: "workspace", TaskKey: "task", TaskVersion: "v1", IdempotencyKey: "idem", MaxAttempts: 1}
	if service := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{}, nil); service.clock == nil {
		t.Fatal("nil clock was not defaulted")
	}
	if _, _, err := NewAgentTaskRunApplicationService(nil, agentTaskClock{now}).Create(t.Context(), valid); apperror.CodeOf(err) != "agent.task.repository_unavailable" {
		t.Fatalf("missing repository create=%v", err)
	}
	if _, _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{}, agentTaskClock{now}).Create(t.Context(), agentmodel.AgentTaskRun{ID: "run", MaxAttempts: 1}); apperror.CodeOf(err) != "agent.task.contract_invalid" {
		t.Fatalf("invalid contract=%v", err)
	}
	if _, _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{createErr: wantErr}, agentTaskClock{now}).Create(t.Context(), valid); !errors.Is(err, wantErr) {
		t.Fatalf("create error=%v", err)
	}

	for _, service := range []*AgentTaskRunApplicationService{nil, NewAgentTaskRunApplicationService(nil, agentTaskClock{now})} {
		if _, _, err := service.Get(t.Context(), "workspace", "run"); apperror.CodeOf(err) != "agent.task.repository_unavailable" {
			t.Fatalf("get unavailable=%v", err)
		}
		if _, err := service.List(t.Context(), "workspace", agentpersistence.AgentTaskRunFilter{}); apperror.CodeOf(err) != "agent.task.repository_unavailable" {
			t.Fatalf("list unavailable=%v", err)
		}
		if _, _, err := service.ClaimNext(t.Context(), "workspace", "worker", time.Minute); apperror.CodeOf(err) != "agent.task.repository_unavailable" {
			t.Fatalf("claim unavailable=%v", err)
		}
		if _, err := service.Heartbeat(t.Context(), "workspace", "run", "worker", 1, time.Minute); apperror.CodeOf(err) != "agent.task.repository_unavailable" {
			t.Fatalf("heartbeat unavailable=%v", err)
		}
	}
	repository := &agentTaskRunRepositoryStub{current: valid, listed: []agentmodel.AgentTaskRun{valid}}
	service := NewAgentTaskRunApplicationService(repository, agentTaskClock{now})
	if _, _, err := service.Get(t.Context(), "", "run"); apperror.CodeOf(err) != "agent.task.query_invalid" {
		t.Fatalf("invalid get workspace=%v", err)
	}
	if _, _, err := service.Get(t.Context(), "workspace", " "); apperror.CodeOf(err) != "agent.task.query_invalid" {
		t.Fatalf("invalid get run=%v", err)
	}
	if _, err := service.List(t.Context(), "", agentpersistence.AgentTaskRunFilter{}); apperror.CodeOf(err) != "agent.task.query_invalid" {
		t.Fatalf("invalid list workspace=%v", err)
	}
	for _, limit := range []int{-1, 501} {
		if runs, err := service.List(t.Context(), "workspace", agentpersistence.AgentTaskRunFilter{Limit: limit}); err != nil || len(runs) != 1 {
			t.Fatalf("list limit %d runs=%v err=%v", limit, runs, err)
		}
	}
	if _, _, err := service.ClaimNext(t.Context(), "", "worker", time.Minute); apperror.CodeOf(err) != "agent.task.claim_invalid" {
		t.Fatalf("invalid claim workspace=%v", err)
	}
	for _, input := range []struct {
		owner workerplatform.WorkerID
		lease time.Duration
	}{{"", time.Minute}, {"worker", 0}} {
		if _, _, err := service.ClaimNext(t.Context(), "workspace", input.owner, input.lease); apperror.CodeOf(err) != "agent.task.claim_invalid" {
			t.Fatalf("invalid claim=%v", err)
		}
	}
	if _, _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{claimErr: wantErr}, agentTaskClock{now}).ClaimNext(t.Context(), "workspace", "worker", time.Minute); !errors.Is(err, wantErr) {
		t.Fatalf("claim error=%v", err)
	}

	if _, _, err := (*AgentTaskRunApplicationService)(nil).ClaimNextForWorker(t.Context(), principalmodel.SystemScope{}, "worker", time.Minute); apperror.CodeOf(err) != "agent.task.system_worker_unavailable" {
		t.Fatalf("nil system claim=%v", err)
	}
	if _, _, err := service.ClaimNextForWorker(t.Context(), principalmodel.SystemScope{}, "worker", time.Minute); apperror.CodeOf(err) != "agent.task.system_worker_unavailable" {
		t.Fatalf("unsupported system claim=%v", err)
	}
	system := &agentTaskSystemRepositoryStub{agentTaskRunRepositoryStub: repository, systemClaim: agentpersistence.AgentTaskClaim{Run: valid}, systemFound: true}
	systemService := NewAgentTaskRunApplicationService(system, agentTaskClock{now})
	validScope := principalmodel.SystemScope{Kind: principalmodel.SystemScopeRuntimeGlobal, Purpose: "agent task worker"}
	for _, input := range []struct {
		scope principalmodel.SystemScope
		owner workerplatform.WorkerID
		lease time.Duration
	}{{principalmodel.SystemScope{}, "worker", time.Minute}, {principalmodel.SystemScope{Kind: principalmodel.SystemScopeBootstrap, Purpose: "bootstrap"}, "worker", time.Minute}, {validScope, "", time.Minute}, {validScope, "worker", 0}} {
		if _, _, err := systemService.ClaimNextForWorker(t.Context(), input.scope, input.owner, input.lease); apperror.CodeOf(err) != "agent.task.claim_invalid" {
			t.Fatalf("invalid system claim=%#v err=%v", input, err)
		}
	}
	if claim, found, err := systemService.ClaimNextForWorker(t.Context(), validScope, "worker", time.Minute); err != nil || !found || claim.Run.ID != "run" {
		t.Fatalf("system claim=%#v found=%v err=%v", claim, found, err)
	}
	if _, err := (*AgentTaskRunApplicationService)(nil).ListForWorker(t.Context(), validScope, agentpersistence.AgentTaskRunFilter{}); apperror.CodeOf(err) != "agent.task.system_worker_unavailable" {
		t.Fatalf("nil system list=%v", err)
	}
	if _, err := service.ListForWorker(t.Context(), validScope, agentpersistence.AgentTaskRunFilter{}); apperror.CodeOf(err) != "agent.task.system_worker_unavailable" {
		t.Fatalf("unsupported system list=%v", err)
	}
	for _, scope := range []principalmodel.SystemScope{{}, {Kind: principalmodel.SystemScopeBootstrap, Purpose: "bootstrap"}} {
		if _, err := systemService.ListForWorker(t.Context(), scope, agentpersistence.AgentTaskRunFilter{}); apperror.CodeOf(err) != "agent.task.query_invalid" {
			t.Fatalf("invalid system list scope=%#v err=%v", scope, err)
		}
	}
	system.systemRuns = []agentmodel.AgentTaskRun{valid}
	if runs, err := systemService.ListForWorker(t.Context(), validScope, agentpersistence.AgentTaskRunFilter{}); err != nil || len(runs) != 1 {
		t.Fatalf("system list=%v err=%v", runs, err)
	}
}

func TestAgentTaskRunTransitionBoundaryMatrix(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	wantErr := errors.New("transition failure")
	run, owner, token := runningAgentTaskRun(now)
	unavailable := []*AgentTaskRunApplicationService{nil, NewAgentTaskRunApplicationService(nil, agentTaskClock{now})}
	for _, service := range unavailable {
		if _, err := service.WaitForApproval(t.Context(), run, owner, token, "proposal", agentmodel.AgentTaskExecutionEvidence{}); apperror.CodeOf(err) != "agent.task.repository_unavailable" {
			t.Fatalf("wait unavailable=%v", err)
		}
		if _, _, err := service.ResolveApproval(t.Context(), run.WorkspaceID, run.ID, AgentTaskApprovalResolution{}); apperror.CodeOf(err) != "agent.task.repository_unavailable" {
			t.Fatalf("resolve unavailable=%v", err)
		}
		if _, err := service.FailAttempt(t.Context(), run, owner, token, "error", "code", false, now); apperror.CodeOf(err) != "agent.task.repository_unavailable" {
			t.Fatalf("fail unavailable=%v", err)
		}
		if _, err := service.Complete(t.Context(), run, owner, token, AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunSucceeded}); apperror.CodeOf(err) != "agent.task.repository_unavailable" {
			t.Fatalf("complete unavailable=%v", err)
		}
		if _, _, err := service.RequestCancel(t.Context(), run.WorkspaceID, run.ID, "reason"); apperror.CodeOf(err) != "agent.task.repository_unavailable" {
			t.Fatalf("cancel unavailable=%v", err)
		}
		if _, err := service.OverrideOutput(t.Context(), run.WorkspaceID, run.ID, nil, principalmodel.Principal{}, "reason"); apperror.CodeOf(err) != "agent.task.repository_unavailable" {
			t.Fatalf("override unavailable=%v", err)
		}
	}

	for _, input := range []struct {
		workspace, runID string
		owner            workerplatform.WorkerID
		token            workerplatform.FencingToken
		lease            time.Duration
	}{
		{"", "run", owner, token, time.Minute}, {run.WorkspaceID, "", owner, token, time.Minute}, {run.WorkspaceID, run.ID, "", token, time.Minute}, {run.WorkspaceID, run.ID, owner, 0, time.Minute}, {run.WorkspaceID, run.ID, owner, token, 0},
	} {
		if _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{}, agentTaskClock{now}).Heartbeat(t.Context(), input.workspace, input.runID, input.owner, input.token, input.lease); apperror.CodeOf(err) != "agent.task.heartbeat_invalid" {
			t.Fatalf("heartbeat input=%#v err=%v", input, err)
		}
	}
	if _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{heartbeatErr: wantErr}, agentTaskClock{now}).Heartbeat(t.Context(), run.WorkspaceID, run.ID, owner, token, time.Minute); !errors.Is(err, wantErr) {
		t.Fatalf("heartbeat error=%v", err)
	}
	lostRepo := &agentTaskRunRepositoryStub{heartbeat: workerplatform.HeartbeatResult{State: workerplatform.HeartbeatLeaseLost, Lease: workerplatform.Lease{Owner: owner, Token: token, ExpiresAt: now.Add(time.Minute)}}}
	if result, err := NewAgentTaskRunApplicationService(lostRepo, agentTaskClock{now}).Heartbeat(t.Context(), run.WorkspaceID, run.ID, owner, token, time.Minute); err != nil || result.State != workerplatform.HeartbeatLeaseLost || result.Lease.Token != token {
		t.Fatalf("lost heartbeat=%#v err=%v", result, err)
	}

	service := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{}, agentTaskClock{now})
	for _, candidate := range []agentmodel.AgentTaskRun{func() agentmodel.AgentTaskRun { v := run; v.Status = agentmodel.AgentTaskRunPending; return v }(), func() agentmodel.AgentTaskRun { v := run; v.Lease.Owner = "other"; return v }()} {
		if _, err := service.WaitForApproval(t.Context(), candidate, owner, token, "proposal", agentmodel.AgentTaskExecutionEvidence{}); apperror.CodeOf(err) != "agent.task.terminal_fence_rejected" {
			t.Fatalf("invalid wait=%v", err)
		}
	}
	if _, err := service.WaitForApproval(t.Context(), run, owner, token, "", agentmodel.AgentTaskExecutionEvidence{}); apperror.CodeOf(err) != "agent.task.terminal_fence_rejected" {
		t.Fatalf("blank proposal=%v", err)
	}
	if _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{saveRunningErr: wantErr}, agentTaskClock{now}).WaitForApproval(t.Context(), run, owner, token, "proposal", agentmodel.AgentTaskExecutionEvidence{}); !errors.Is(err, wantErr) {
		t.Fatalf("wait save error=%v", err)
	}

	if _, _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{getErr: wantErr}, agentTaskClock{now}).ResolveApproval(t.Context(), run.WorkspaceID, run.ID, AgentTaskApprovalResolution{}); !errors.Is(err, wantErr) {
		t.Fatalf("resolve get error=%v", err)
	}
	if _, found, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{}, agentTaskClock{now}).ResolveApproval(t.Context(), run.WorkspaceID, run.ID, AgentTaskApprovalResolution{}); err != nil || found {
		t.Fatalf("resolve missing found=%v err=%v", found, err)
	}
	terminal := run
	terminal.Status, terminal.Approval = agentmodel.AgentTaskRunSucceeded, nil
	for _, candidate := range []agentmodel.AgentTaskRun{terminal, func() agentmodel.AgentTaskRun { v := run; v.Status = agentmodel.AgentTaskRunWaitingApproval; return v }(), func() agentmodel.AgentTaskRun {
		v := run
		v.Status = agentmodel.AgentTaskRunWaitingApproval
		v.Approval = &agentmodel.AgentTaskApproval{ProposalID: "other"}
		return v
	}()} {
		if _, _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{current: candidate}, agentTaskClock{now}).ResolveApproval(t.Context(), run.WorkspaceID, run.ID, AgentTaskApprovalResolution{ProposalID: "proposal", Decision: "approved"}); apperror.CodeOf(err) != "agent.task.approval_state_conflict" {
			t.Fatalf("approval conflict candidate=%#v err=%v", candidate, err)
		}
	}
	waiting := run
	waiting.Status, waiting.Approval = agentmodel.AgentTaskRunWaitingApproval, &agentmodel.AgentTaskApproval{ProposalID: "proposal"}
	if _, _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{current: waiting}, agentTaskClock{now}).ResolveApproval(t.Context(), run.WorkspaceID, run.ID, AgentTaskApprovalResolution{ProposalID: "proposal", Decision: "invalid"}); apperror.CodeOf(err) != "agent.task.approval_decision_invalid" {
		t.Fatalf("approval invalid=%v", err)
	}
	for name, setup := range map[string]func(*agentTaskRunRepositoryStub, *agentTaskTerminalCommitterStub) *AgentTaskRunApplicationService{
		"save": func(r *agentTaskRunRepositoryStub, _ *agentTaskTerminalCommitterStub) *AgentTaskRunApplicationService {
			r.saveApprovalErr = wantErr
			return NewAgentTaskRunApplicationService(r, agentTaskClock{now})
		},
		"terminal": func(r *agentTaskRunRepositoryStub, c *agentTaskTerminalCommitterStub) *AgentTaskRunApplicationService {
			r.current.ProcessID, r.current.NodeInstanceID, c.err = "process", "node", wantErr
			return NewAgentTaskRunApplicationService(r, agentTaskClock{now}, c)
		},
	} {
		repo, committer := &agentTaskRunRepositoryStub{current: waiting}, &agentTaskTerminalCommitterStub{}
		if _, _, err := setup(repo, committer).ResolveApproval(t.Context(), run.WorkspaceID, run.ID, AgentTaskApprovalResolution{ProposalID: "proposal", Decision: "approved"}); !errors.Is(err, wantErr) {
			t.Fatalf("resolve %s error=%v", name, err)
		}
	}

	invalidFence := run
	invalidFence.Lease.Owner = "other"
	if _, err := service.FailAttempt(t.Context(), invalidFence, owner, token, "error", "code", false, now); apperror.CodeOf(err) != "agent.task.terminal_fence_rejected" {
		t.Fatalf("fail fence=%v", err)
	}
	noAttempts := run
	noAttempts.Attempts = nil
	if _, err := service.FailAttempt(t.Context(), noAttempts, owner, token, "error", "code", false, now); err != nil {
		t.Fatalf("fail no attempts=%v", err)
	}
	if _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{saveRunningErr: wantErr}, agentTaskClock{now}).FailAttempt(t.Context(), run, owner, token, "error", "code", false, now); !errors.Is(err, wantErr) {
		t.Fatalf("fail save=%v", err)
	}
	workflowRun := run
	workflowRun.ProcessID, workflowRun.NodeInstanceID, workflowRun.Attempt = "process", "node", workflowRun.MaxAttempts
	if _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{}, agentTaskClock{now}, &agentTaskTerminalCommitterStub{err: wantErr}).FailAttempt(t.Context(), workflowRun, owner, token, "error", "code", false, now); !errors.Is(err, wantErr) {
		t.Fatalf("fail terminal=%v", err)
	}

	if _, err := service.Complete(t.Context(), invalidFence, owner, token, AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunSucceeded}); apperror.CodeOf(err) != "agent.task.terminal_fence_rejected" {
		t.Fatalf("complete fence=%v", err)
	}
	if _, err := service.Complete(t.Context(), run, owner, token, AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunRunning}); apperror.CodeOf(err) != "agent.task.terminal_fence_rejected" {
		t.Fatalf("complete status=%v", err)
	}
	noAttempts = run
	noAttempts.Attempts = nil
	if _, err := service.Complete(t.Context(), noAttempts, owner, token, AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunSucceeded}); err != nil {
		t.Fatalf("complete no attempts=%v", err)
	}
	if _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{saveRunningErr: wantErr}, agentTaskClock{now}).Complete(t.Context(), run, owner, token, AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunSucceeded}); !errors.Is(err, wantErr) {
		t.Fatalf("complete save=%v", err)
	}
	if _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{}, agentTaskClock{now}, &agentTaskTerminalCommitterStub{err: wantErr}).Complete(t.Context(), workflowRun, owner, token, AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunSucceeded}); !errors.Is(err, wantErr) {
		t.Fatalf("complete terminal=%v", err)
	}
}

func TestAgentTaskRunApprovalCancellationExpiryAndOverrideBoundaries(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	wantErr := errors.New("boundary failure")
	run, owner, token := runningAgentTaskRun(now)
	waiting := run
	waiting.Status, waiting.Approval = agentmodel.AgentTaskRunWaitingApproval, &agentmodel.AgentTaskApproval{ProposalID: "proposal"}
	for _, candidate := range []agentmodel.AgentTaskRun{
		func() agentmodel.AgentTaskRun {
			v := run
			v.Status = agentmodel.AgentTaskRunSucceeded
			v.Approval = &agentmodel.AgentTaskApproval{ProposalID: "other", Decision: "approved"}
			return v
		}(),
		func() agentmodel.AgentTaskRun {
			v := run
			v.Status = agentmodel.AgentTaskRunSucceeded
			v.Approval = &agentmodel.AgentTaskApproval{ProposalID: "proposal", Decision: "other"}
			return v
		}(),
	} {
		if _, _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{current: candidate}, agentTaskClock{now}).ResolveApproval(t.Context(), run.WorkspaceID, run.ID, AgentTaskApprovalResolution{ProposalID: "proposal", Decision: "approved"}); apperror.CodeOf(err) != "agent.task.approval_state_conflict" {
			t.Fatalf("terminal mismatch=%v", err)
		}
	}
	noAttempts := waiting
	noAttempts.Attempts = nil
	if _, _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{current: noAttempts}, agentTaskClock{now}).ResolveApproval(t.Context(), run.WorkspaceID, run.ID, AgentTaskApprovalResolution{ProposalID: "proposal", Decision: "approved"}); err != nil {
		t.Fatalf("approval no attempts=%v", err)
	}
	for _, candidate := range []agentmodel.AgentTaskRun{
		func() agentmodel.AgentTaskRun { v := waiting; v.ProcessID = "process"; return v }(),
		func() agentmodel.AgentTaskRun { v := waiting; v.NodeInstanceID = "node"; return v }(),
	} {
		if _, _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{current: candidate}, agentTaskClock{now}, &agentTaskTerminalCommitterStub{}).ResolveApproval(t.Context(), run.WorkspaceID, run.ID, AgentTaskApprovalResolution{ProposalID: "proposal", Decision: "approved"}); err != nil {
			t.Fatalf("partial workflow approval=%v", err)
		}
	}

	wrongStatus := run
	wrongStatus.Status = agentmodel.AgentTaskRunPending
	if _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{}, agentTaskClock{now}).FailAttempt(t.Context(), wrongStatus, owner, token, "error", "code", true, now.Add(time.Minute)); apperror.CodeOf(err) != "agent.task.terminal_fence_rejected" {
		t.Fatalf("fail status=%v", err)
	}
	for _, candidate := range []agentmodel.AgentTaskRun{
		func() agentmodel.AgentTaskRun { v := run; v.Attempt = v.MaxAttempts; return v }(),
		func() agentmodel.AgentTaskRun { v := run; v.ProcessID = "process"; v.Attempt = v.MaxAttempts; return v }(),
		func() agentmodel.AgentTaskRun {
			v := run
			v.NodeInstanceID = "node"
			v.Attempt = v.MaxAttempts
			return v
		}(),
	} {
		if _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{}, agentTaskClock{now}, &agentTaskTerminalCommitterStub{}).FailAttempt(t.Context(), candidate, owner, token, "error", "code", true, now.Add(time.Minute)); err != nil {
			t.Fatalf("fail branch=%v", err)
		}
	}
	if _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{}, agentTaskClock{now}).FailAttempt(t.Context(), run, owner, token, "error", "code", true, now); err != nil {
		t.Fatalf("retry deadline=%v", err)
	}
	if _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{}, agentTaskClock{now}).Complete(t.Context(), wrongStatus, owner, token, AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunSucceeded}); apperror.CodeOf(err) != "agent.task.terminal_fence_rejected" {
		t.Fatalf("complete status=%v", err)
	}
	for _, candidate := range []agentmodel.AgentTaskRun{func() agentmodel.AgentTaskRun { v := run; v.ProcessID = "process"; return v }(), func() agentmodel.AgentTaskRun { v := run; v.NodeInstanceID = "node"; return v }()} {
		if _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{}, agentTaskClock{now}, &agentTaskTerminalCommitterStub{}).Complete(t.Context(), candidate, owner, token, AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunSucceeded}); err != nil {
			t.Fatalf("complete branch=%v", err)
		}
	}

	if _, _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{cancelErr: wantErr}, agentTaskClock{now}).RequestCancel(t.Context(), run.WorkspaceID, run.ID, "reason"); !errors.Is(err, wantErr) {
		t.Fatalf("cancel error=%v", err)
	}
	for _, candidate := range []agentmodel.AgentTaskRun{run, func() agentmodel.AgentTaskRun { v := waiting; v.Approval = nil; return v }()} {
		got, _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{current: candidate}, agentTaskClock{now}).RequestCancel(t.Context(), run.WorkspaceID, run.ID, "reason")
		if err != nil || got.ID != run.ID {
			t.Fatalf("cancel direct=%#v err=%v", got, err)
		}
	}
	if _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{listErr: wantErr}, agentTaskClock{now}).ExpireApprovals(t.Context(), run.WorkspaceID, now); !errors.Is(err, wantErr) {
		t.Fatalf("expire list=%v", err)
	}
	future := waiting
	future.Approval = &agentmodel.AgentTaskApproval{ProposalID: "proposal", ExpiresAt: now.Add(time.Hour)}
	zero := waiting
	zero.Approval = &agentmodel.AgentTaskApproval{ProposalID: "proposal"}
	missing := waiting
	missing.Approval = nil
	replayed := waiting
	replayed.Status, replayed.Approval = agentmodel.AgentTaskRunFailed, &agentmodel.AgentTaskApproval{ProposalID: "proposal", Decision: "timed_out", ExpiresAt: now.Add(-time.Hour)}
	repo := &agentTaskRunRepositoryStub{listed: []agentmodel.AgentTaskRun{missing, zero, future, replayed}, current: replayed}
	if count, err := NewAgentTaskRunApplicationService(repo, agentTaskClock{now}).ExpireApprovals(t.Context(), run.WorkspaceID, now); err != nil || count != 0 {
		t.Fatalf("expire skips count=%d err=%v", count, err)
	}
	expired := waiting
	expired.Approval.ExpiresAt = now.Add(-time.Hour)
	repo = &agentTaskRunRepositoryStub{listed: []agentmodel.AgentTaskRun{expired}, current: expired, saveApprovalErr: wantErr}
	if _, err := NewAgentTaskRunApplicationService(repo, agentTaskClock{now}).ExpireApprovals(t.Context(), run.WorkspaceID, now); !errors.Is(err, wantErr) {
		t.Fatalf("expire resolve=%v", err)
	}

	actor := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: run.WorkspaceID, UserID: "operator"}}
	terminal := run
	terminal.Status = agentmodel.AgentTaskRunSucceeded
	for _, badActor := range []principalmodel.Principal{principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: run.WorkspaceID}}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: run.WorkspaceID, UserID: "operator"}}} {
		reason := "reason"
		if badActor.UserID != "" {
			reason = ""
		}
		if _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{current: terminal}, agentTaskClock{now}).OverrideOutput(t.Context(), run.WorkspaceID, run.ID, nil, badActor, reason); apperror.CodeOf(err) != "agent.task.override_evidence_required" {
			t.Fatalf("override actor=%v", err)
		}
	}
	wrongActor := actor
	wrongActor.WorkspaceID = "other"
	if _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{current: terminal}, agentTaskClock{now}).OverrideOutput(t.Context(), run.WorkspaceID, run.ID, nil, wrongActor, "reason"); apperror.CodeOf(err) != "agent.task.override_evidence_required" {
		t.Fatalf("override workspace=%v", err)
	}
	if _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{getErr: wantErr}, agentTaskClock{now}).OverrideOutput(t.Context(), run.WorkspaceID, run.ID, nil, actor, "reason"); !errors.Is(err, wantErr) {
		t.Fatalf("override get=%v", err)
	}
	if _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{}, agentTaskClock{now}).OverrideOutput(t.Context(), run.WorkspaceID, run.ID, nil, actor, "reason"); err != nil {
		t.Fatalf("override missing=%v", err)
	}
	if _, err := NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{current: run}, agentTaskClock{now}).OverrideOutput(t.Context(), run.WorkspaceID, run.ID, nil, actor, "reason"); apperror.CodeOf(err) != "agent.task.override_terminal_required" {
		t.Fatalf("override status=%v", err)
	}
	if _, err := NewAgentTaskRunApplicationServiceWithAudit(&agentTaskRunRepositoryStub{current: terminal, saveOverrideErr: wantErr}, agentTaskClock{now}, nil, &agentTaskAuditStub{}).OverrideOutput(t.Context(), run.WorkspaceID, run.ID, nil, actor, "reason"); !errors.Is(err, wantErr) {
		t.Fatalf("override save=%v", err)
	}
}
