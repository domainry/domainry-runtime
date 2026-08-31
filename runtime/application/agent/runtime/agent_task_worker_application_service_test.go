package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type agentTaskExecutorFunc func(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error)

type advancingAgentTaskClock struct {
	now   time.Time
	steps int
}

func (clock *advancingAgentTaskClock) Now() time.Time {
	clock.steps++
	return clock.now.Add(time.Duration(clock.steps) * time.Millisecond)
}

func (fn agentTaskExecutorFunc) ExecuteAgentTask(ctx context.Context, run agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
	return fn(ctx, run)
}

type agentTaskCancellableExecutorStub struct {
	cancelled bool
	err       error
}

func (*agentTaskCancellableExecutorStub) ExecuteAgentTask(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
	return AgentTaskRunCompletion{}, nil
}
func (s *agentTaskCancellableExecutorStub) CancelAgentTask(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
	s.cancelled = true
	return AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunCancelled, Outcome: "cancelled", Reconciliation: agentmodel.AgentTaskReconciliation{Required: true, ExternalRunID: "external", State: "cancel_uncertain"}}, s.err
}

func agentTaskWorkerFixture(now time.Time, execute agentTaskExecutorFunc) (*AgentTaskWorker, *agentTaskRunRepositoryStub) {
	run, owner, _ := runningAgentTaskRun(now)
	repository := &agentTaskRunRepositoryStub{found: true, claim: agentpersistence.AgentTaskClaim{Run: run, Lease: run.Lease}, listed: []agentmodel.AgentTaskRun{{CreatedAt: now.Add(-2 * time.Second)}}}
	dependencies := workerplatform.NormalizeDependencies(workerplatform.Dependencies{Clock: agentTaskClock{now: now}, WorkerID: owner, Control: workerplatform.NewController()})
	return NewAgentTaskWorker(NewAgentTaskRunApplicationService(repository, agentTaskClock{now: now}), execute, dependencies, AgentTaskWorkerConfig{WorkspaceID: run.WorkspaceID, LeaseTTL: time.Minute, HeartbeatInterval: 10 * time.Millisecond, RetryBaseDelay: time.Second}), repository
}

func TestAgentTaskWorkerCompletesRetriesDeadLettersAndCancels(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	t.Run("complete", func(t *testing.T) {
		worker, repository := agentTaskWorkerFixture(now, func(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
			return AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunSucceeded, Outcome: "success", Output: map[string]any{"score": 95}, Evidence: agentmodel.AgentTaskExecutionEvidence{ToolInvocationRefs: []string{"tool-1", "tool-2"}, Usage: map[string]any{"tokens": 12, "cost_microunits": 34}}}, nil
		})
		processed, err := worker.ProcessOne(t.Context())
		if err != nil || !processed || repository.saved.Status != agentmodel.AgentTaskRunSucceeded || worker.Metrics().Completed != 1 || worker.Metrics().QueueDepth != 1 || worker.Metrics().QueueLagMilliseconds != 2000 || worker.Metrics().ToolCalls != 2 || worker.Metrics().Tokens != 12 || worker.Metrics().CostMicrounits != 34 || !strings.Contains(worker.OpenMetrics(), `event="completed"} 1`) {
			t.Fatalf("processed=%v saved=%#v metrics=%#v err=%v", processed, repository.saved, worker.Metrics(), err)
		}
	})
	t.Run("permission denied", func(t *testing.T) {
		worker, _ := agentTaskWorkerFixture(now, func(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
			return AgentTaskRunCompletion{}, apperror.New(apperror.KindForbidden, "agent.authorization.denied", nil, nil)
		})
		processed, err := worker.ProcessOne(t.Context())
		if err != nil || !processed || worker.Metrics().PermissionDenied != 1 || !strings.Contains(worker.OpenMetrics(), `event="permission_denied"} 1`) {
			t.Fatalf("processed=%v metrics=%#v err=%v", processed, worker.Metrics(), err)
		}
	})
	t.Run("retry", func(t *testing.T) {
		worker, repository := agentTaskWorkerFixture(now, func(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
			return AgentTaskRunCompletion{}, &AgentTaskExecutionError{Class: "provider_5xx", Code: "provider.unavailable", Retryable: true, ExternalRunID: "external-1", Reconciliation: agentmodel.AgentTaskReconciliation{Required: true, ExternalRunID: "external-1", State: "poll_required"}, Cause: errors.New("unavailable")}
		})
		processed, err := worker.ProcessOne(t.Context())
		if err != nil || !processed || repository.saved.Status != agentmodel.AgentTaskRunRetryScheduled || repository.saved.Attempts[0].ExternalRunID != "external-1" || !repository.saved.Reconciliation.Required || worker.Metrics().Retried != 1 {
			t.Fatalf("processed=%v saved=%#v metrics=%#v err=%v", processed, repository.saved, worker.Metrics(), err)
		}
	})
	t.Run("dead-letter", func(t *testing.T) {
		worker, repository := agentTaskWorkerFixture(now, func(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
			return AgentTaskRunCompletion{}, errors.New("bad output")
		})
		processed, err := worker.ProcessOne(t.Context())
		if err != nil || !processed || repository.saved.Status != agentmodel.AgentTaskRunDeadLetter || worker.Metrics().DeadLettered != 1 {
			t.Fatalf("processed=%v saved=%#v metrics=%#v err=%v", processed, repository.saved, worker.Metrics(), err)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		called := false
		worker, repository := agentTaskWorkerFixture(now, func(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
			called = true
			return AgentTaskRunCompletion{}, nil
		})
		requested := now.Add(-time.Second)
		worker.runs.repository.(*agentTaskRunRepositoryStub).claim.Run.CancelRequestedAt = &requested
		processed, err := worker.ProcessOne(t.Context())
		if err != nil || !processed || called || repository.saved.Status != agentmodel.AgentTaskRunCancelled || worker.Metrics().Cancelled != 1 {
			t.Fatalf("processed=%v called=%v saved=%#v err=%v", processed, called, repository.saved, err)
		}
	})
	t.Run("cancel propagates to provider", func(t *testing.T) {
		run, owner, _ := runningAgentTaskRun(now)
		requested := now.Add(-time.Second)
		run.CancelRequestedAt = &requested
		repository := &agentTaskRunRepositoryStub{found: true, claim: agentpersistence.AgentTaskClaim{Run: run, Lease: run.Lease}}
		executor := &agentTaskCancellableExecutorStub{}
		dependencies := workerplatform.NormalizeDependencies(workerplatform.Dependencies{Clock: agentTaskClock{now: now}, WorkerID: owner, Control: workerplatform.NewController()})
		worker := NewAgentTaskWorker(NewAgentTaskRunApplicationService(repository, agentTaskClock{now: now}), executor, dependencies, AgentTaskWorkerConfig{WorkspaceID: run.WorkspaceID, LeaseTTL: time.Minute})
		if processed, err := worker.ProcessOne(t.Context()); err != nil || !processed || !executor.cancelled || !repository.saved.Reconciliation.Required {
			t.Fatalf("processed=%v cancelled=%v saved=%#v err=%v", processed, executor.cancelled, repository.saved, err)
		}
	})
}

func TestAgentTaskWorkerWakeTargetsCommittedTaskID(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	worker, repository := agentTaskWorkerFixture(now, func(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
		return AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunSucceeded, Outcome: "success"}, nil
	})
	locator := AgentTaskLocator{WorkspaceID: "workspace-a", RunID: "run-a"}
	worker.Wake(locator)
	if got := <-worker.Wakeups(); got != locator {
		t.Fatalf("wakeup=%#v", got)
	}
	processed, err := worker.ProcessTask(t.Context(), locator)
	if err != nil || !processed || repository.directCalls != 1 || repository.directWorkspaceID != locator.WorkspaceID || repository.directRunID != locator.RunID {
		t.Fatalf("processed=%v direct=%d workspace=%q run=%q err=%v", processed, repository.directCalls, repository.directWorkspaceID, repository.directRunID, err)
	}
}

func TestAgentTaskWorkerPreservesDurableApprovalWait(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	var repository *agentTaskRunRepositoryStub
	worker, repository := agentTaskWorkerFixture(now, func(_ context.Context, run agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
		run.Status, run.Revision = agentmodel.AgentTaskRunWaitingApproval, run.Revision+1
		run.Approval = &agentmodel.AgentTaskApproval{ProposalID: "proposal", Status: "pending", RequestedAt: now, ExpiresAt: now.Add(time.Hour)}
		repository.current = run
		return AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunSucceeded, Outcome: "success"}, nil
	})
	processed, err := worker.ProcessOne(t.Context())
	if err != nil || !processed || repository.current.Status != agentmodel.AgentTaskRunWaitingApproval || repository.saved.ID != "" || worker.Metrics().Completed != 0 || worker.Metrics().ApprovalWaits != 1 || !strings.Contains(worker.OpenMetrics(), `event="approval_wait"} 1`) {
		t.Fatalf("processed=%v current=%#v saved=%#v metrics=%#v err=%v", processed, repository.current, repository.saved, worker.Metrics(), err)
	}
}

func TestAgentTaskWorkerFailsClosedAndHonorsDrain(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	if _, err := (*AgentTaskWorker)(nil).ProcessOne(t.Context()); apperror.CodeOf(err) != "agent.task.worker_unavailable" {
		t.Fatalf("nil worker err=%v", err)
	}
	worker, _ := agentTaskWorkerFixture(now, func(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
		t.Fatal("executor called while draining")
		return AgentTaskRunCompletion{}, nil
	})
	if !worker.worker.Control.Drain() {
		t.Fatal("failed to enter drain")
	}
	if processed, err := worker.ProcessOne(t.Context()); err != nil || processed {
		t.Fatalf("drained processed=%v err=%v", processed, err)
	}
	if worker.retryDelay(1) != time.Second || worker.retryDelay(3) != 4*time.Second {
		t.Fatalf("retry delay=%s/%s", worker.retryDelay(1), worker.retryDelay(3))
	}
}

func TestAgentTaskWorkerConfigurationErrorAndCancellationBoundaries(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	configured := NewAgentTaskWorker(nil, nil, workerplatform.Dependencies{}, AgentTaskWorkerConfig{LeaseTTL: -1, HeartbeatInterval: time.Hour, RetryBaseDelay: -1})
	if configured.config.LeaseTTL != 30*time.Second || configured.config.HeartbeatInterval != 10*time.Second || configured.config.RetryBaseDelay != time.Second {
		t.Fatalf("defaults=%#v", configured.config)
	}
	if _, err := (&AgentTaskWorker{}).ProcessOne(t.Context()); apperror.CodeOf(err) != "agent.task.worker_unavailable" {
		t.Fatalf("missing deps=%v", err)
	}
	worker, repository := agentTaskWorkerFixture(now, func(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
		return AgentTaskRunCompletion{}, nil
	})
	repository.claimErr = errors.New("claim")
	if processed, err := worker.ProcessOne(t.Context()); !errors.Is(err, repository.claimErr) || processed {
		t.Fatalf("claim error processed=%v err=%v", processed, err)
	}
	repository.claimErr, repository.found = nil, false
	if processed, err := worker.ProcessOne(t.Context()); err != nil || processed {
		t.Fatalf("no claim processed=%v err=%v", processed, err)
	}

	run, owner, _ := runningAgentTaskRun(now)
	requested := now.Add(-time.Second)
	run.CancelRequestedAt = &requested
	for name, setup := range map[string]func(*agentTaskRunRepositoryStub) AgentTaskExecutor{
		"provider": func(*agentTaskRunRepositoryStub) AgentTaskExecutor {
			return &agentTaskCancellableExecutorStub{err: errors.New("cancel")}
		},
		"save": func(r *agentTaskRunRepositoryStub) AgentTaskExecutor {
			r.saveRunningErr = errors.New("save")
			return agentTaskExecutorFunc(func(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
				return AgentTaskRunCompletion{}, nil
			})
		},
	} {
		repo := &agentTaskRunRepositoryStub{found: true, claim: agentpersistence.AgentTaskClaim{Run: run, Lease: run.Lease}}
		executor := setup(repo)
		dependencies := workerplatform.NormalizeDependencies(workerplatform.Dependencies{Clock: agentTaskClock{now}, WorkerID: owner, Control: workerplatform.NewController()})
		candidate := NewAgentTaskWorker(NewAgentTaskRunApplicationService(repo, agentTaskClock{now}), executor, dependencies, AgentTaskWorkerConfig{WorkspaceID: run.WorkspaceID, LeaseTTL: time.Minute})
		if processed, err := candidate.ProcessOne(t.Context()); err == nil || !processed {
			t.Fatalf("cancel %s processed=%v err=%v", name, processed, err)
		}
	}
}

func TestAgentTaskWorkerExecutionAndHeartbeatBoundaryMatrix(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	wantErr := errors.New("boundary")
	t.Run("timeout and get error", func(t *testing.T) {
		worker, repo := agentTaskWorkerFixture(now, func(ctx context.Context, _ agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
			<-ctx.Done()
			return AgentTaskRunCompletion{}, ctx.Err()
		})
		repo.claim.Run.TimeoutSeconds = 1
		repo.getErr = wantErr
		if processed, err := worker.ProcessOne(t.Context()); !processed || !errors.Is(err, wantErr) {
			t.Fatalf("processed=%v err=%v", processed, err)
		}
	})
	t.Run("lease lost", func(t *testing.T) {
		worker, repo := agentTaskWorkerFixture(now, func(ctx context.Context, _ agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
			<-ctx.Done()
			return AgentTaskRunCompletion{}, ctx.Err()
		})
		repo.heartbeat = workerplatform.HeartbeatResult{State: workerplatform.HeartbeatLeaseLost}
		if processed, err := worker.ProcessOne(t.Context()); !processed || apperror.CodeOf(err) != "agent.task.lease_lost" || worker.Metrics().LeaseLost != 1 {
			t.Fatalf("processed=%v metrics=%#v err=%v", processed, worker.Metrics(), err)
		}
	})
	t.Run("typed minimal and fail save", func(t *testing.T) {
		worker, repo := agentTaskWorkerFixture(now, func(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
			return AgentTaskRunCompletion{}, &AgentTaskExecutionError{Retryable: true}
		})
		repo.claim.Run.Attempts = nil
		repo.saveRunningErr = wantErr
		if processed, err := worker.ProcessOne(t.Context()); !processed || !errors.Is(err, wantErr) {
			t.Fatalf("processed=%v err=%v", processed, err)
		}
	})
	t.Run("completion save", func(t *testing.T) {
		worker, repo := agentTaskWorkerFixture(now, func(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
			return AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunSucceeded}, nil
		})
		repo.saveRunningErr = wantErr
		if processed, err := worker.ProcessOne(t.Context()); !processed || !errors.Is(err, wantErr) {
			t.Fatalf("processed=%v err=%v", processed, err)
		}
	})
	t.Run("current missing", func(t *testing.T) {
		worker, repo := agentTaskWorkerFixture(now, func(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
			return AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunSucceeded}, nil
		})
		repo.current = agentmodel.AgentTaskRun{}
		if processed, err := worker.ProcessOne(t.Context()); err != nil || !processed {
			t.Fatalf("processed=%v err=%v", processed, err)
		}
	})
	t.Run("current running and empty external id", func(t *testing.T) {
		worker, repo := agentTaskWorkerFixture(now, func(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
			return AgentTaskRunCompletion{}, &AgentTaskExecutionError{Code: "failed"}
		})
		repo.current = repo.claim.Run
		if processed, err := worker.ProcessOne(t.Context()); err != nil || !processed {
			t.Fatalf("processed=%v err=%v", processed, err)
		}
	})
	t.Run("heartbeat repository error", func(t *testing.T) {
		worker, repo := agentTaskWorkerFixture(now, func(ctx context.Context, _ agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
			<-ctx.Done()
			return AgentTaskRunCompletion{}, ctx.Err()
		})
		repo.heartbeatErr = wantErr
		if processed, err := worker.ProcessOne(t.Context()); !processed || !errors.Is(err, wantErr) {
			t.Fatalf("processed=%v err=%v", processed, err)
		}
	})
}

func TestAgentTaskWorkerQueueRetryMetricsAndUsageBoundaries(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	worker, repo := agentTaskWorkerFixture(now, func(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
		return AgentTaskRunCompletion{}, nil
	})
	if (*AgentTaskWorker)(nil).Metrics() != (AgentTaskWorkerMetrics{}) {
		t.Fatal("nil metrics")
	}
	if worker.retryDelay(0) != time.Second || worker.retryDelay(100) != time.Hour {
		t.Fatalf("retry bounds=%s/%s", worker.retryDelay(0), worker.retryDelay(100))
	}
	repo.listErr = errors.New("list")
	worker.observeQueue(t.Context())
	repo.listErr, repo.listed = nil, nil
	worker.observeQueue(t.Context())
	repo.listed = []agentmodel.AgentTaskRun{{CreatedAt: now.Add(time.Hour)}, {CreatedAt: now.Add(2 * time.Hour)}}
	worker.observeQueue(t.Context())
	repo.listed = []agentmodel.AgentTaskRun{{CreatedAt: now.Add(2 * time.Hour)}, {CreatedAt: now.Add(time.Hour)}}
	worker.observeQueue(t.Context())
	systemRepo := &agentTaskSystemRepositoryStub{agentTaskRunRepositoryStub: repo, systemRuns: []agentmodel.AgentTaskRun{{CreatedAt: now.Add(-time.Second)}}}
	worker.runs = NewAgentTaskRunApplicationService(systemRepo, agentTaskClock{now})
	worker.config.SystemScope = principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "agent worker")
	worker.observeQueue(t.Context())
	if _, found, err := worker.claimNext(t.Context()); err != nil || found {
		t.Fatalf("system claim found=%v err=%v", found, err)
	}
	if worker.Metrics().QueueDepth != 1 {
		t.Fatalf("system queue=%#v", worker.Metrics())
	}
	for _, test := range []struct {
		value any
		want  uint64
	}{{1, 1}, {0, 0}, {int64(2), 2}, {int64(-1), 0}, {float64(3), 3}, {float64(0), 0}, {"4", 0}} {
		if got := agentUsageUint64(map[string]any{"x": test.value}, "x"); got != test.want {
			t.Fatalf("usage %#v=%d", test.value, got)
		}
	}
	var nilErr *AgentTaskExecutionError
	if nilErr.Error() != "" || nilErr.Unwrap() != nil {
		t.Fatal("nil execution error")
	}
	plain := &AgentTaskExecutionError{Code: " code "}
	if plain.Error() != "code" || plain.Unwrap() != nil {
		t.Fatalf("plain=%q/%v", plain.Error(), plain.Unwrap())
	}
	caused := &AgentTaskExecutionError{Cause: errors.New("cause")}
	if caused.Error() != "cause" || !errors.Is(caused, caused.Cause) {
		t.Fatalf("caused=%q/%v", caused.Error(), caused.Unwrap())
	}
	run, owner, _ := runningAgentTaskRun(now)
	repository := &agentTaskRunRepositoryStub{found: true, claim: agentpersistence.AgentTaskClaim{Run: run, Lease: run.Lease}}
	clock := &advancingAgentTaskClock{now: now}
	dependencies := workerplatform.NormalizeDependencies(workerplatform.Dependencies{Clock: clock, WorkerID: owner, Control: workerplatform.NewController()})
	durationWorker := NewAgentTaskWorker(NewAgentTaskRunApplicationService(repository, clock), agentTaskExecutorFunc(func(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
		return AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunSucceeded}, nil
	}), dependencies, AgentTaskWorkerConfig{WorkspaceID: run.WorkspaceID, LeaseTTL: time.Minute})
	if processed, err := durationWorker.ProcessOne(t.Context()); err != nil || !processed || durationWorker.Metrics().DurationMilliseconds == 0 {
		t.Fatalf("duration processed=%v metrics=%#v err=%v", processed, durationWorker.Metrics(), err)
	}
}

func TestAgentTaskWorkerMissingExecutorWithRunService(t *testing.T) {
	worker := &AgentTaskWorker{runs: NewAgentTaskRunApplicationService(&agentTaskRunRepositoryStub{}, agentTaskClock{now: time.Now()})}
	if _, err := worker.ProcessOne(t.Context()); apperror.CodeOf(err) != "agent.task.worker_unavailable" {
		t.Fatalf("missing executor=%v", err)
	}
}
