package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

func openAgentTaskRunStore(t *testing.T) *AgentTaskRunStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "agent-task.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewAgentTaskRunStore(store)
	if err := NewAgentSchemaMigration(store).EnsureSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	return repository
}

func TestAgentTaskRunSystemWorkerScopeDiscoversAndClaimsAcrossWorkspaces(t *testing.T) {
	repository := openAgentTaskRunStore(t)
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	first := agentTaskRunFixture(now, "run-a", "idem-a")
	second := agentTaskRunFixture(now.Add(time.Second), "run-b", "idem-b")
	second.WorkspaceID, second.Identity.Initiator.WorkspaceID = "workspace-b", "workspace-b"
	for _, run := range []agentmodel.AgentTaskRun{first, second} {
		if _, _, err := repository.Create(t.Context(), run); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repository.ListAgentTaskRunsForWorker(t.Context(), principalmodel.SystemScope{}, agentrepository.AgentTaskRunFilter{}); err == nil {
		t.Fatal("unscoped global list was accepted")
	}
	if _, _, err := repository.ClaimNextAgentTaskRunForWorker(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "wrong scope"), "worker", now, time.Minute); err == nil {
		t.Fatal("installation scope claimed Runtime tasks")
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test Agent worker")
	listed, err := repository.ListAgentTaskRunsForWorker(t.Context(), scope, agentrepository.AgentTaskRunFilter{Statuses: []agentmodel.AgentTaskRunStatus{agentmodel.AgentTaskRunPending}, Limit: 10})
	if err != nil || len(listed) != 2 {
		t.Fatalf("listed=%#v err=%v", listed, err)
	}
	claimA, found, err := repository.ClaimNextAgentTaskRunForWorker(t.Context(), scope, "worker-a", now, time.Minute)
	if err != nil || !found || claimA.Run.WorkspaceID != "workspace-a" {
		t.Fatalf("claim A=%#v found=%v err=%v", claimA, found, err)
	}
	claimB, found, err := repository.ClaimNextAgentTaskRunForWorker(t.Context(), scope, "worker-b", now.Add(2*time.Second), time.Minute)
	if err != nil || !found || claimB.Run.WorkspaceID != "workspace-b" {
		t.Fatalf("claim B=%#v found=%v err=%v", claimB, found, err)
	}
}

func TestAgentTaskRunBackfillWorkerScopesDoesNotBlockSingleConnection(t *testing.T) {
	repository := openAgentTaskRunStore(t)
	repository.db.SetMaxOpenConns(1)
	now := time.Date(2026, 8, 23, 8, 0, 0, 0, time.UTC)
	for index, workspaceID := range []string{"workspace-a", "workspace-b"} {
		run := agentTaskRunFixture(now.Add(time.Duration(index)*time.Second), fmt.Sprintf("run-%d", index), fmt.Sprintf("idem-%d", index))
		run.WorkspaceID = workspaceID
		if _, _, err := repository.Create(t.Context(), run); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := repository.BackfillWorkerScopes(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := repository.db.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM "+repository.store.TableIdentifier("runtime_worker_queue_scopes")+" WHERE "+repository.store.Identifier("queue_kind")+" = "+repository.store.Placeholder(1),
		agentTaskWorkerQueueKind,
	).Scan(&count); err != nil || count != 2 {
		t.Fatalf("worker scope count=%d err=%v", count, err)
	}
}

func agentTaskRunFixture(now time.Time, id, idempotencyKey string) agentmodel.AgentTaskRun {
	return agentmodel.AgentTaskRun{
		ID: id, WorkspaceID: "workspace-a", ProcessID: "process-a", NodeInstanceID: "node-a", TaskKey: "customer.review", TaskVersion: "v1",
		Status: agentmodel.AgentTaskRunPending, MaxAttempts: 3, IdempotencyKey: idempotencyKey, CreatedAt: now, UpdatedAt: now, Revision: 1,
		Identity: agentmodel.AgentExecutionIdentity{Mode: agentmodel.AgentTaskIdentityInherit, Initiator: agentmodel.AgentPrincipalReference{UserID: "user-a", RoleKey: "operator", WorkspaceID: "workspace-a"}},
	}
}

func TestAgentTaskRunStoreCreateReplayListClaimHeartbeatAndFence(t *testing.T) {
	repository := openAgentTaskRunStore(t)
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	run := agentTaskRunFixture(now, "run-a", "idem-a")
	created, replayed, err := repository.Create(t.Context(), run)
	if err != nil || replayed || created.ID != run.ID {
		t.Fatalf("create=%#v replayed=%v err=%v", created, replayed, err)
	}
	replay, replayed, err := repository.Create(t.Context(), agentTaskRunFixture(now, "run-other", "idem-a"))
	if err != nil || !replayed || replay.ID != run.ID {
		t.Fatalf("replay=%#v replayed=%v err=%v", replay, replayed, err)
	}
	values, err := repository.List(t.Context(), "workspace-a", agentrepository.AgentTaskRunFilter{Statuses: []agentmodel.AgentTaskRunStatus{agentmodel.AgentTaskRunPending}, ProcessID: "process-a", TaskKey: "customer.review", Limit: 1})
	if err != nil || len(values) != 1 {
		t.Fatalf("list=%#v err=%v", values, err)
	}
	owner := workerplatform.WorkerID("worker-a")
	claim, found, err := repository.ClaimNext(t.Context(), "workspace-a", owner.String(), now, time.Minute)
	if err != nil || !found || claim.Run.Status != agentmodel.AgentTaskRunRunning || claim.Run.Attempt != 1 || claim.Lease.FencingToken != 1 {
		t.Fatalf("claim=%#v found=%v err=%v", claim, found, err)
	}
	if _, found, err := repository.ClaimNext(t.Context(), "workspace-a", "worker-b", now.Add(30*time.Second), time.Minute); err != nil || found {
		t.Fatalf("live lease second claim found=%v err=%v", found, err)
	}
	heartbeat, err := repository.Heartbeat(t.Context(), "workspace-a", run.ID, owner.String(), claim.Lease.FencingToken, now.Add(30*time.Second), time.Minute)
	if err != nil || heartbeat.Lost || !heartbeat.Lease.ExpiresAt.Equal(now.Add(90*time.Second)) {
		t.Fatalf("heartbeat=%#v err=%v", heartbeat, err)
	}
	stale, err := repository.Heartbeat(t.Context(), "workspace-a", run.ID, owner.String(), claim.Lease.FencingToken+1, now.Add(40*time.Second), time.Minute)
	if err != nil || !stale.Lost {
		t.Fatalf("stale heartbeat=%#v err=%v", stale, err)
	}
	start := agentrepository.AgentToolCallStart{WorkspaceID: "workspace-a", ProcessID: "process-a", TaskRunID: run.ID, Tool: "query_records", InputHash: "input-hash", Owner: owner.String(), FencingToken: claim.Lease.FencingToken, MaxToolCalls: 2, CostUnits: 2, MaxCostUnits: 5, Authorization: agentmodel.AgentAuthorizationEvidence{Decision: "allow", Code: "agent.authorization.allowed", PolicyRevision: "policy-1"}}
	callRef, count, err := repository.BeginAgentToolCall(t.Context(), start)
	if err != nil || count != 1 || callRef == "" {
		t.Fatalf("begin tool ref=%q count=%d err=%v", callRef, count, err)
	}
	if err := repository.FinishAgentToolCall(t.Context(), agentrepository.AgentToolCallFinish{WorkspaceID: "workspace-a", TaskRunID: run.ID, CallRef: callRef, Status: "executed", Owner: owner.String(), FencingToken: claim.Lease.FencingToken, Evidence: map[string]any{"output_hash": "output-hash"}}); err != nil {
		t.Fatal(err)
	}
	if err := repository.FinishAgentToolCall(t.Context(), agentrepository.AgentToolCallFinish{WorkspaceID: "workspace-a", TaskRunID: run.ID, CallRef: callRef, Status: "executed", Owner: owner.String(), FencingToken: claim.Lease.FencingToken}); err != nil {
		t.Fatalf("duplicate finish=%v", err)
	}
	if _, _, err := repository.BeginAgentToolCall(t.Context(), agentrepository.AgentToolCallStart{WorkspaceID: "workspace-a", TaskRunID: run.ID, Tool: "query_records", Owner: owner.String(), FencingToken: claim.Lease.FencingToken + 1, MaxToolCalls: 2}); apperror.CodeOf(err) != "agent.task.tool_fence_rejected" {
		t.Fatalf("stale tool begin=%v", err)
	}
	budget := start
	budget.CostUnits, budget.MaxCostUnits = 4, 5
	if _, _, err := repository.BeginAgentToolCall(t.Context(), budget); apperror.CodeOf(err) != "agent.task.cost_budget_exceeded" {
		t.Fatalf("cost budget err=%v", err)
	}
	terminal := claim.Run
	terminal.Status, terminal.Outcome, terminal.UpdatedAt = agentmodel.AgentTaskRunSucceeded, "success", now.Add(time.Minute)
	if err := repository.SaveRunning(t.Context(), terminal, owner.String(), claim.Lease.FencingToken+1); apperror.CodeOf(err) != "agent.task.terminal_fence_rejected" {
		t.Fatalf("stale terminal err=%v", err)
	}
	if err := repository.SaveRunning(t.Context(), terminal, owner.String(), claim.Lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := repository.Get(t.Context(), "workspace-a", run.ID)
	if err != nil || !found || loaded.Status != agentmodel.AgentTaskRunSucceeded || loaded.ToolCallCount != 1 || len(loaded.Evidence.ToolInvocations) != 1 || loaded.Evidence.ToolInvocations[0].OutputHash != "output-hash" {
		t.Fatalf("loaded=%#v found=%v err=%v", loaded, found, err)
	}
}

func TestAgentTaskRunStoreDirectClaimTargetsOneTaskAndFencesClusterPeers(t *testing.T) {
	repository := openAgentTaskRunStore(t)
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	for _, run := range []agentmodel.AgentTaskRun{
		agentTaskRunFixture(now, "run-a", "idem-a"),
		agentTaskRunFixture(now.Add(time.Millisecond), "run-b", "idem-b"),
	} {
		if _, _, err := repository.Create(t.Context(), run); err != nil {
			t.Fatal(err)
		}
	}
	claim, found, err := repository.ClaimAgentTaskRun(t.Context(), "workspace-a", "run-b", "cluster-a", now.Add(time.Second), time.Minute)
	if err != nil || !found || claim.Run.ID != "run-b" || claim.Lease.FencingToken != 1 {
		t.Fatalf("claim=%#v found=%v err=%v", claim, found, err)
	}
	if _, found, err := repository.ClaimAgentTaskRun(t.Context(), "workspace-a", "run-b", "cluster-b", now.Add(2*time.Second), time.Minute); err != nil || found {
		t.Fatalf("peer claim found=%v err=%v", found, err)
	}
	remaining, found, err := repository.ClaimAgentTaskRun(t.Context(), "workspace-a", "run-a", "cluster-b", now.Add(2*time.Second), time.Minute)
	if err != nil || !found || remaining.Run.ID != "run-a" {
		t.Fatalf("remaining=%#v found=%v err=%v", remaining, found, err)
	}
}

func TestAgentTaskRunStoreReclaimsExpiredLeaseAndCancelsIdempotently(t *testing.T) {
	repository := openAgentTaskRunStore(t)
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	for _, run := range []agentmodel.AgentTaskRun{agentTaskRunFixture(now, "running", "idem-running"), agentTaskRunFixture(now.Add(time.Second), "pending", "idem-pending")} {
		if _, _, err := repository.Create(t.Context(), run); err != nil {
			t.Fatal(err)
		}
	}
	first, found, err := repository.ClaimNext(t.Context(), "workspace-a", "worker-a", now, time.Second)
	if err != nil || !found {
		t.Fatalf("first claim found=%v err=%v", found, err)
	}
	reclaimed, found, err := repository.ClaimNext(t.Context(), "workspace-a", "worker-b", now.Add(2*time.Second), time.Minute)
	if err != nil || !found || reclaimed.Run.ID != first.Run.ID || reclaimed.Lease.FencingToken != first.Lease.FencingToken+1 || reclaimed.Run.Attempt != 2 {
		t.Fatalf("reclaim=%#v found=%v err=%v", reclaimed, found, err)
	}
	cancelled, replayed, err := repository.RequestCancel(t.Context(), "workspace-a", "pending", "operator", now.Add(3*time.Second))
	if err != nil || replayed || cancelled.Status != agentmodel.AgentTaskRunCancelled || cancelled.CompletedAt == nil {
		t.Fatalf("cancelled=%#v replayed=%v err=%v", cancelled, replayed, err)
	}
	replay, replayed, err := repository.RequestCancel(t.Context(), "workspace-a", "pending", "again", now.Add(4*time.Second))
	if err != nil || !replayed || replay.CancellationReason != "operator" {
		t.Fatalf("cancel replay=%#v replayed=%v err=%v", replay, replayed, err)
	}
}

func TestAgentTaskRunStoreApprovalAndOverrideCAS(t *testing.T) {
	repository := openAgentTaskRunStore(t)
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	run := agentTaskRunFixture(now, "approval", "idem-approval")
	if _, _, err := repository.Create(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	claim, found, err := repository.ClaimNext(t.Context(), run.WorkspaceID, "worker", now, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim=%#v found=%v err=%v", claim, found, err)
	}
	waiting := claim.Run
	waiting.Status, waiting.Revision, waiting.UpdatedAt = agentmodel.AgentTaskRunWaitingApproval, waiting.Revision+1, now.Add(time.Second)
	waiting.Approval = &agentmodel.AgentTaskApproval{ProposalID: "proposal", Status: "pending", RequestedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := repository.SaveRunning(t.Context(), waiting, claim.Lease.Owner, claim.Lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	waiting, _, err = repository.Get(t.Context(), run.WorkspaceID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	terminal := waiting
	terminal.Status, terminal.Outcome, terminal.Revision, terminal.UpdatedAt = agentmodel.AgentTaskRunSucceeded, "success", waiting.Revision+1, now.Add(2*time.Second)
	if err := repository.SaveWaitingApproval(t.Context(), terminal, waiting.Revision-1); apperror.CodeOf(err) != "agent.task.approval_state_conflict" {
		t.Fatalf("stale approval err=%v", err)
	}
	if err := repository.SaveWaitingApproval(t.Context(), terminal, waiting.Revision); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := repository.Get(t.Context(), run.WorkspaceID, run.ID)
	if err != nil || loaded.Status != agentmodel.AgentTaskRunSucceeded {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	overridden := loaded
	overridden.Output, overridden.Revision, overridden.UpdatedAt = map[string]any{"corrected": true}, loaded.Revision+1, now.Add(3*time.Second)
	if err := repository.SaveTerminalOverride(t.Context(), overridden, loaded.Revision-1); apperror.CodeOf(err) != "agent.task.override_state_conflict" {
		t.Fatalf("stale override err=%v", err)
	}
	if err := repository.SaveTerminalOverride(t.Context(), overridden, loaded.Revision); err != nil {
		t.Fatal(err)
	}
	transition := overridden
	transition.Status, transition.Revision, transition.UpdatedAt = agentmodel.AgentTaskRunPending, overridden.Revision+1, now.Add(4*time.Second)
	if err := repository.SaveOperationalTransition(t.Context(), transition, overridden.Status, overridden.Revision-1); apperror.CodeOf(err) != "agent.task.operation_state_conflict" {
		t.Fatalf("stale operation err=%v", err)
	}
	if err := repository.SaveOperationalTransition(t.Context(), transition, overridden.Status, overridden.Revision); err != nil {
		t.Fatal(err)
	}
}

func TestAgentTaskRunStoreHundredConcurrentClaimsHaveOneOwner(t *testing.T) {
	repository := openAgentTaskRunStore(t)
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	if _, _, err := repository.Create(t.Context(), agentTaskRunFixture(now, "contended", "idem-contended")); err != nil {
		t.Fatal(err)
	}
	var claimed atomic.Int64
	errorsSeen := make(chan error, 100)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, found, err := repository.ClaimNext(t.Context(), "workspace-a", "worker-"+fmt.Sprint(index), now, time.Minute)
			if err != nil {
				errorsSeen <- err
				return
			}
			if found {
				claimed.Add(1)
			}
		}(index)
	}
	close(start)
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatalf("concurrent claim error=%v", err)
	}
	if claimed.Load() != 1 {
		t.Fatalf("claim owners=%d", claimed.Load())
	}
}

func TestAgentInteractiveRunStoreSeparatesLifecycleAndAtomicallyHandsOffTask(t *testing.T) {
	repository := openAgentTaskRunStore(t)
	now := time.Date(2026, 8, 4, 14, 0, 0, 0, time.UTC)
	trusted := agentmodel.GlobalAgentContext{ContextRevision: "context-1", EntrypointKey: "assistant.global", AgentKey: "customer-agent"}
	run := agentmodel.AgentInteractiveRun{ID: "interactive-1", SessionID: "session-1", WorkspaceID: "workspace-a", UserID: "user-a", RoleKey: "operator", Surface: "business_workspace", RouteKey: "customer.detail", AgentKey: "customer-agent", EntrypointKey: "assistant.global", ContextRevision: "context-1", Context: trusted, Status: agentmodel.AgentInteractiveRunRunning, IdempotencyKey: "message-1", CreatedAt: now, UpdatedAt: now, Revision: 1}
	created, replayed, err := repository.CreateInteractiveRun(t.Context(), run)
	if err != nil || replayed || created.ID != run.ID {
		t.Fatalf("created=%#v replayed=%v err=%v", created, replayed, err)
	}
	if replay, duplicate, err := repository.CreateInteractiveRun(t.Context(), run); err != nil || !duplicate || replay.ID != run.ID {
		t.Fatalf("replay=%#v duplicate=%v err=%v", replay, duplicate, err)
	}
	listed, err := repository.ListInteractiveRuns(t.Context(), "workspace-a", "user-a", "operator", agentrepository.AgentInteractiveRunFilter{Statuses: []agentmodel.AgentInteractiveRunStatus{agentmodel.AgentInteractiveRunRunning}, Limit: 10})
	if err != nil || len(listed) != 1 {
		t.Fatalf("listed=%#v err=%v", listed, err)
	}
	if updated, err := repository.SaveInteractiveRun(t.Context(), run, 99); err != nil || updated {
		t.Fatalf("stale update=%v err=%v", updated, err)
	}
	task := agentmodel.AgentTaskRun{ID: "task-1", WorkspaceID: run.WorkspaceID, ProcessID: "interactive-process-1", InteractiveRunID: run.ID, TaskKey: "customer.review", TaskVersion: "1.0.0", Status: agentmodel.AgentTaskRunPending, MaxAttempts: 2, IdempotencyKey: "interactive-task-1", CreatedAt: now, UpdatedAt: now, Revision: 1}
	run.RouteType, run.RoutedTargetKey, run.RoutedTargetVersion = agentmodel.AgentRouteTask, task.TaskKey, task.TaskVersion
	handedOff, duplicate, err := repository.CommitInteractiveTaskHandoff(t.Context(), run, run.Revision, task)
	if err != nil || duplicate || handedOff.Status != agentmodel.AgentInteractiveRunHandedOff || handedOff.TaskRunID != task.ID || handedOff.ProcessID != task.ProcessID {
		t.Fatalf("handoff=%#v duplicate=%v err=%v", handedOff, duplicate, err)
	}
	if replay, duplicate, err := repository.CommitInteractiveTaskHandoff(t.Context(), run, run.Revision, task); err != nil || !duplicate || replay.TaskRunID != task.ID {
		t.Fatalf("handoff replay=%#v duplicate=%v err=%v", replay, duplicate, err)
	}
	var taskCount int
	if err := repository.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+repository.store.TableIdentifier("agent_task_runs")+" WHERE "+repository.store.Identifier("run_id")+" = "+repository.store.Placeholder(1), task.ID).Scan(&taskCount); err != nil || taskCount != 1 {
		t.Fatalf("task count=%d err=%v", taskCount, err)
	}
}
