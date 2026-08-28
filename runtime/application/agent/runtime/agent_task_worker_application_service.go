package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/logging"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
	"github.com/domainry/domainry-runtime/runtime/platform/telemetry"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

type AgentTaskExecutor interface {
	ExecuteAgentTask(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error)
}

type AgentTaskExecutionError struct {
	Class          string
	Code           string
	Retryable      bool
	ExternalRunID  string
	Reconciliation agentmodel.AgentTaskReconciliation
	Cause          error
}

func (err *AgentTaskExecutionError) Error() string {
	if err == nil {
		return ""
	}
	if err.Cause != nil {
		return err.Cause.Error()
	}
	return strings.TrimSpace(err.Code)
}
func (err *AgentTaskExecutionError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

type AgentTaskWorkerConfig struct {
	WorkspaceID       string
	SystemScope       principalmodel.SystemScope
	LeaseTTL          time.Duration
	HeartbeatInterval time.Duration
	RetryBaseDelay    time.Duration
}

type AgentTaskCancellationExecutor interface {
	CancelAgentTask(context.Context, agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error)
}

type AgentTaskWorkerMetrics struct {
	Claims, Completed, Retried, DeadLettered, Cancelled, LeaseLost uint64
	PermissionDenied, ApprovalWaits, ToolCalls                     uint64
	QueueDepth, InFlight, QueueLagMilliseconds                     int64
	DurationMilliseconds, Tokens, CostMicrounits                   uint64
}

type AgentTaskWorker struct {
	runs      *AgentTaskRunApplicationService
	executor  AgentTaskExecutor
	worker    workerplatform.Dependencies
	config    AgentTaskWorkerConfig
	wakeups   chan AgentTaskLocator
	metricsMu sync.Mutex
	metrics   AgentTaskWorkerMetrics
}

func NewAgentTaskWorker(runs *AgentTaskRunApplicationService, executor AgentTaskExecutor, dependencies workerplatform.Dependencies, config AgentTaskWorkerConfig) *AgentTaskWorker {
	dependencies = workerplatform.NormalizeDependencies(dependencies)
	if config.LeaseTTL <= 0 {
		config.LeaseTTL = 30 * time.Second
	}
	if config.HeartbeatInterval <= 0 || config.HeartbeatInterval >= config.LeaseTTL {
		config.HeartbeatInterval = config.LeaseTTL / 3
	}
	if config.RetryBaseDelay <= 0 {
		config.RetryBaseDelay = time.Second
	}
	return &AgentTaskWorker{runs: runs, executor: executor, worker: dependencies, config: config, wakeups: make(chan AgentTaskLocator, 256)}
}

func (w *AgentTaskWorker) ProcessOne(ctx context.Context) (processed bool, err error) {
	return w.process(ctx, AgentTaskLocator{}, false)
}

func (w *AgentTaskWorker) ProcessTask(ctx context.Context, locator AgentTaskLocator) (processed bool, err error) {
	return w.process(ctx, locator, true)
}

func (w *AgentTaskWorker) Wake(locator AgentTaskLocator) {
	if w == nil {
		return
	}
	workspace, workspaceErr := principalmodel.NewWorkspaceID(locator.WorkspaceID)
	if workspaceErr != nil {
		return
	}
	if strings.TrimSpace(locator.RunID) == "" {
		return
	}
	select {
	case w.wakeups <- AgentTaskLocator{WorkspaceID: workspace.String(), RunID: strings.TrimSpace(locator.RunID)}:
	default:
		// Wakeups are an acceleration hint. The durable recovery scan remains the
		// source of eventual progress when this bounded process-local queue is full.
	}
}

func (w *AgentTaskWorker) Wakeups() <-chan AgentTaskLocator {
	if w == nil {
		return nil
	}
	return w.wakeups
}

func (w *AgentTaskWorker) process(ctx context.Context, locator AgentTaskLocator, direct bool) (processed bool, err error) {
	workspaceID := ""
	if w != nil {
		workspaceID = w.config.WorkspaceID
	}
	if direct {
		workspaceID = strings.TrimSpace(locator.WorkspaceID)
	}
	ctx, span := telemetry.StartUseCase(ctx, "agent.task.worker.process", attribute.String("workspace.id", workspaceID))
	defer func() { telemetry.EndUseCase(span, err, "") }()
	if w == nil || w.runs == nil || w.executor == nil {
		return false, apperror.New(apperror.KindUnavailable, "agent.task.worker_unavailable", nil, nil)
	}
	if !w.worker.Control.TryBegin() {
		return false, nil
	}
	defer w.worker.Control.End()
	w.observeQueue(ctx)
	var claim agentrepository.AgentTaskClaim
	var found bool
	if direct {
		claim, found, err = w.runs.ClaimTask(ctx, locator, w.worker.WorkerID, w.config.LeaseTTL)
	} else {
		claim, found, err = w.claimNext(ctx)
	}
	if err != nil || !found {
		return false, err
	}
	ctx = requestcontext.WithWorkspaceID(ctx, claim.Run.WorkspaceID)
	ctx = requestcontext.WithActorID(ctx, claim.Lease.Owner)
	span.SetAttributes(attribute.String("workflow.process_id", claim.Run.ProcessID), attribute.String("workflow.node_instance_id", claim.Run.NodeInstanceID), attribute.String("agent.task_run_id", claim.Run.ID), attribute.String("agent.task_key", claim.Run.TaskKey), attribute.Int("agent.attempt", claim.Run.Attempt), attribute.String("agent.external_run_id", latestAgentExternalRunID(claim.Run)), attribute.String("correlation.id", claim.Run.CorrelationID))
	logging.FromContext(ctx).Info("agent task claimed", logging.Fields(map[string]any{"workspace_id": claim.Run.WorkspaceID, "interactive_run_id": claim.Run.InteractiveRunID, "process_id": claim.Run.ProcessID, "node_instance_id": claim.Run.NodeInstanceID, "task_run_id": claim.Run.ID, "task_key": claim.Run.TaskKey, "attempt": claim.Run.Attempt, "external_run_id": latestAgentExternalRunID(claim.Run), "correlation_id": claim.Run.CorrelationID})...)
	w.addMetric(func(metrics *AgentTaskWorkerMetrics) { metrics.Claims++ })
	startedAt := w.worker.Clock.Now().UTC()
	w.addMetric(func(metrics *AgentTaskWorkerMetrics) { metrics.InFlight++ })
	defer w.addMetric(func(metrics *AgentTaskWorkerMetrics) {
		metrics.InFlight--
		duration := w.worker.Clock.Now().UTC().Sub(startedAt)
		if duration > 0 {
			metrics.DurationMilliseconds += uint64(duration.Milliseconds())
		}
	})
	run := claim.Run
	if run.CancelRequestedAt != nil {
		completion := AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunCancelled, Outcome: "cancelled", ErrorCode: "agent.task.cancelled"}
		if cancellable, ok := w.executor.(AgentTaskCancellationExecutor); ok {
			var cancelErr error
			completion, cancelErr = cancellable.CancelAgentTask(ctx, run)
			if cancelErr != nil {
				return true, cancelErr
			}
		}
		_, err := w.runs.Complete(ctx, run, workerplatform.WorkerID(claim.Lease.Owner), workerplatform.FencingToken(claim.Lease.FencingToken), completion)
		if err == nil {
			w.addMetric(func(metrics *AgentTaskWorkerMetrics) { metrics.Cancelled++ })
		}
		return true, err
	}
	executionContext := ctx
	var cancel context.CancelFunc
	if run.TimeoutSeconds > 0 {
		executionContext, cancel = context.WithTimeout(ctx, time.Duration(run.TimeoutSeconds)*time.Second)
		defer cancel()
	}
	heartbeatContext, stopHeartbeat := workerplatform.WithHeartbeat(executionContext, w.config.HeartbeatInterval, func(heartbeatCtx context.Context) error {
		result, heartbeatErr := w.runs.Heartbeat(heartbeatCtx, run.WorkspaceID, run.ID, workerplatform.WorkerID(claim.Lease.Owner), workerplatform.FencingToken(claim.Lease.FencingToken), w.config.LeaseTTL)
		if heartbeatErr != nil {
			return heartbeatErr
		}
		if result.Lost() {
			return apperror.New(apperror.KindConflict, "agent.task.lease_lost", nil, nil)
		}
		return nil
	})
	completion, executionErr := w.executor.ExecuteAgentTask(heartbeatContext, run)
	heartbeatErr := stopHeartbeat()
	current, currentFound, currentErr := w.runs.Get(ctx, run.WorkspaceID, run.ID)
	if currentErr != nil {
		return true, currentErr
	}
	if currentFound && current.Status == agentmodel.AgentTaskRunWaitingApproval {
		// A proposal tool call durably moves the task out of the leased running
		// state. Any provider completion or heartbeat lease-loss observed after
		// that transition is stale and must not overwrite the approval wait.
		w.addMetric(func(metrics *AgentTaskWorkerMetrics) { metrics.ApprovalWaits++ })
		return true, nil
	}
	if heartbeatErr != nil {
		w.addMetric(func(metrics *AgentTaskWorkerMetrics) { metrics.LeaseLost++ })
		return true, heartbeatErr
	}
	if executionErr != nil {
		if apperror.KindOf(executionErr) == apperror.KindForbidden {
			w.addMetric(func(metrics *AgentTaskWorkerMetrics) { metrics.PermissionDenied++ })
		}
		classified := &AgentTaskExecutionError{Class: "unknown", Code: apperror.CodeOf(executionErr)}
		var typed *AgentTaskExecutionError
		if errors.As(executionErr, &typed) {
			classified = typed
			if len(run.Attempts) > 0 && strings.TrimSpace(typed.ExternalRunID) != "" {
				run.Attempts[len(run.Attempts)-1].ExternalRunID = strings.TrimSpace(typed.ExternalRunID)
			}
			if typed.Reconciliation.Required {
				run.Reconciliation = typed.Reconciliation
			}
		}
		if classified.Code == "" {
			classified.Code = "agent.task.execution_failed"
		}
		next := w.worker.Clock.Now().UTC().Add(w.retryDelay(run.Attempt))
		updated, failErr := w.runs.FailAttempt(ctx, run, workerplatform.WorkerID(claim.Lease.Owner), workerplatform.FencingToken(claim.Lease.FencingToken), classified.Class, classified.Code, classified.Retryable, next)
		if failErr != nil {
			return true, failErr
		}
		if updated.Status == agentmodel.AgentTaskRunRetryScheduled {
			w.addMetric(func(metrics *AgentTaskWorkerMetrics) { metrics.Retried++ })
		} else {
			w.addMetric(func(metrics *AgentTaskWorkerMetrics) { metrics.DeadLettered++ })
		}
		return true, nil
	}
	if _, err := w.runs.Complete(ctx, run, workerplatform.WorkerID(claim.Lease.Owner), workerplatform.FencingToken(claim.Lease.FencingToken), completion); err != nil {
		return true, err
	}
	w.addMetric(func(metrics *AgentTaskWorkerMetrics) { metrics.Completed++ })
	w.addMetric(func(metrics *AgentTaskWorkerMetrics) {
		metrics.ToolCalls += uint64(len(completion.Evidence.ToolInvocationRefs))
		metrics.Tokens += agentUsageUint64(completion.Evidence.Usage, "tokens")
		metrics.CostMicrounits += agentUsageUint64(completion.Evidence.Usage, "cost_microunits")
	})
	return true, nil
}

func (w *AgentTaskWorker) claimNext(ctx context.Context) (agentrepository.AgentTaskClaim, bool, error) {
	if w.config.SystemScope.Valid() {
		return w.runs.ClaimNextForWorker(ctx, w.config.SystemScope, w.worker.WorkerID, w.config.LeaseTTL)
	}
	return w.runs.ClaimNext(ctx, w.config.WorkspaceID, w.worker.WorkerID, w.config.LeaseTTL)
}

func (w *AgentTaskWorker) retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := w.config.RetryBaseDelay
	for index := 1; index < attempt && delay < time.Hour; index++ {
		delay *= 2
	}
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

func (w *AgentTaskWorker) addMetric(update func(*AgentTaskWorkerMetrics)) {
	w.metricsMu.Lock()
	defer w.metricsMu.Unlock()
	update(&w.metrics)
}

func (w *AgentTaskWorker) Metrics() AgentTaskWorkerMetrics {
	if w == nil {
		return AgentTaskWorkerMetrics{}
	}
	w.metricsMu.Lock()
	defer w.metricsMu.Unlock()
	return w.metrics
}

func (w *AgentTaskWorker) observeQueue(ctx context.Context) {
	filter := agentrepository.AgentTaskRunFilter{Statuses: []agentmodel.AgentTaskRunStatus{agentmodel.AgentTaskRunPending, agentmodel.AgentTaskRunRetryScheduled}, Limit: 500}
	var runs []agentmodel.AgentTaskRun
	var err error
	if w.config.SystemScope.Valid() {
		runs, err = w.runs.ListForWorker(ctx, w.config.SystemScope, filter)
	} else {
		runs, err = w.runs.List(ctx, w.config.WorkspaceID, filter)
	}
	if err != nil {
		return
	}
	now, oldest := w.worker.Clock.Now().UTC(), time.Time{}
	for _, run := range runs {
		if oldest.IsZero() || run.CreatedAt.Before(oldest) {
			oldest = run.CreatedAt
		}
	}
	lag := int64(0)
	if !oldest.IsZero() && now.After(oldest) {
		lag = now.Sub(oldest).Milliseconds()
	}
	w.addMetric(func(metrics *AgentTaskWorkerMetrics) {
		metrics.QueueDepth, metrics.QueueLagMilliseconds = int64(len(runs)), lag
	})
}

func agentUsageUint64(usage map[string]any, key string) uint64 {
	switch value := usage[key].(type) {
	case int:
		if value > 0 {
			return uint64(value)
		}
	case int64:
		if value > 0 {
			return uint64(value)
		}
	case float64:
		if value > 0 {
			return uint64(value)
		}
	}
	return 0
}

func (w *AgentTaskWorker) OpenMetrics() string {
	metrics := w.Metrics()
	return fmt.Sprintf("# HELP domainry_agent_task_worker_events Agent Task worker events by outcome.\n# TYPE domainry_agent_task_worker_events counter\ndomainry_agent_task_worker_events{event=\"claim\"} %d\ndomainry_agent_task_worker_events{event=\"completed\"} %d\ndomainry_agent_task_worker_events{event=\"retry\"} %d\ndomainry_agent_task_worker_events{event=\"dead_letter\"} %d\ndomainry_agent_task_worker_events{event=\"cancelled\"} %d\ndomainry_agent_task_worker_events{event=\"lease_lost\"} %d\ndomainry_agent_task_worker_events{event=\"permission_denied\"} %d\ndomainry_agent_task_worker_events{event=\"approval_wait\"} %d\ndomainry_agent_task_tool_calls_total %d\ndomainry_agent_task_queue_depth %d\ndomainry_agent_task_in_flight %d\ndomainry_agent_task_queue_lag_milliseconds %d\ndomainry_agent_task_duration_milliseconds_total %d\ndomainry_agent_task_tokens_total %d\ndomainry_agent_task_cost_microunits_total %d\n", metrics.Claims, metrics.Completed, metrics.Retried, metrics.DeadLettered, metrics.Cancelled, metrics.LeaseLost, metrics.PermissionDenied, metrics.ApprovalWaits, metrics.ToolCalls, metrics.QueueDepth, metrics.InFlight, metrics.QueueLagMilliseconds, metrics.DurationMilliseconds, metrics.Tokens, metrics.CostMicrounits)
}
