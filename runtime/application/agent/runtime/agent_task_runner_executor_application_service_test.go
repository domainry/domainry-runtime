package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
)

type agentTaskRunnerStub struct {
	startResult    agentsdk.TaskResult
	pollResults    []agentsdk.TaskResult
	cancelResult   agentsdk.TaskResult
	startErr       error
	pollErr        error
	cancelErr      error
	starts         int
	polls          int
	cancels        int
	startWorkspace string
}

func (s *agentTaskRunnerStub) Start(ctx context.Context, _ agentsdk.TaskRequest) (agentsdk.TaskResult, error) {
	s.starts++
	s.startWorkspace = requestcontext.WorkspaceID(ctx)
	return s.startResult, s.startErr
}
func (s *agentTaskRunnerStub) Poll(context.Context, string, string) (agentsdk.TaskResult, error) {
	s.polls++
	if len(s.pollResults) == 0 {
		return agentsdk.TaskResult{Status: agentsdk.ProviderRunRunning, ExternalRunID: "external-1"}, s.pollErr
	}
	result := s.pollResults[0]
	s.pollResults = s.pollResults[1:]
	return result, s.pollErr
}
func (s *agentTaskRunnerStub) Cancel(context.Context, string, string) (agentsdk.TaskResult, error) {
	s.cancels++
	return s.cancelResult, s.cancelErr
}

func agentTaskRunnerExecutorFixture(t *testing.T, runner agentsdk.TaskRunner) (*AgentTaskRunnerExecutor, agentmodel.AgentTaskRun) {
	t.Helper()
	authorization, initiator, schema, _ := agentAuthorizationFixture()
	outputSchema := map[string]any{"type": "object", "required": []string{"score"}, "properties": map[string]any{"score": map[string]any{"type": "number"}}, "additionalProperties": false}
	for index := range schema.full.AgentTasks {
		schema.full.AgentTasks[index].OutputSchema = outputSchema
		schema.full.AgentTasks[index].ExecutionLimits.MaxOutputBytes = 1024
	}
	for key, snapshot := range schema.filtered {
		for index := range snapshot.AgentTasks {
			snapshot.AgentTasks[index].OutputSchema = outputSchema
			snapshot.AgentTasks[index].ExecutionLimits.MaxOutputBytes = 1024
		}
		schema.filtered[key] = snapshot
	}
	credentials := NewAgentTaskCredentialApplicationService([]byte("0123456789abcdef0123456789abcdef"), agentTaskClock{now: time.Now().UTC()}, agentCredentialIDStub{})
	executor := NewAgentTaskRunnerExecutor(AgentTaskRunnerExecutorDependencies{Runner: runner, Authorization: authorization, Credentials: credentials, PollInterval: time.Millisecond})
	run := agentmodel.AgentTaskRun{ID: "run-1", WorkspaceID: initiator.WorkspaceID, ProcessID: "process-1", TaskKey: "customer.review", TaskVersion: "1.0.0", Identity: agentsdk.ExecutionIdentity{Mode: agentsdk.AgentTaskIdentityInherit, Initiator: agentPrincipalReference(initiator)}, Input: map[string]any{"record_id": "customer-1"}, IdempotencyKey: "idem-1", CorrelationID: "correlation-1", Evidence: agentmodel.AgentTaskExecutionEvidence{ManifestHash: "manifest", Authorization: []agentmodel.AgentAuthorizationEvidence{{AllowedObjects: []string{"customer"}, AllowedActions: []string{"customer.update"}, AllowedOutcomes: []string{"success"}}}}, Attempts: []agentmodel.AgentTaskAttempt{{Number: 1}}}
	return executor, run
}

func TestAgentTaskRunnerExecutorStartsPollsValidatesAndRetainsEvidence(t *testing.T) {
	runner := &agentTaskRunnerStub{startResult: agentsdk.TaskResult{Status: agentsdk.ProviderRunRunning, ExternalRunID: "external-1"}, pollResults: []agentsdk.TaskResult{{Status: agentsdk.ProviderRunCompleted, ExternalRunID: "external-1", Outcome: "success", Output: map[string]any{"score": float64(90)}, Model: "model-1", Usage: map[string]any{"tokens": 10}}}}
	executor, run := agentTaskRunnerExecutorFixture(t, runner)
	completion, err := executor.ExecuteAgentTask(t.Context(), run)
	if err != nil || completion.Status != agentmodel.AgentTaskRunSucceeded || completion.ExternalRunID != "external-1" || completion.Evidence.ManifestHash != "manifest" || completion.Evidence.Model != "model-1" || runner.starts != 1 || runner.polls != 1 || runner.startWorkspace != run.WorkspaceID {
		t.Fatalf("completion=%#v starts=%d polls=%d err=%v", completion, runner.starts, runner.polls, err)
	}
}

func TestAgentTaskRunnerExecutorReconcilesExistingExternalRunBeforeStart(t *testing.T) {
	runner := &agentTaskRunnerStub{pollResults: []agentsdk.TaskResult{{Status: agentsdk.ProviderRunCompleted, ExternalRunID: "existing", Outcome: "success", Output: map[string]any{"score": 1}}}}
	executor, run := agentTaskRunnerExecutorFixture(t, runner)
	run.Attempts[0].ExternalRunID = "existing"
	if _, err := executor.ExecuteAgentTask(t.Context(), run); err != nil || runner.starts != 0 || runner.polls != 1 {
		t.Fatalf("starts=%d polls=%d err=%v", runner.starts, runner.polls, err)
	}
}

func TestAgentTaskRunnerExecutorClassifiesTimeoutOutputAndProviderBoundaries(t *testing.T) {
	t.Run("local timeout retains reconciliation", func(t *testing.T) {
		runner := &agentTaskRunnerStub{startResult: agentsdk.TaskResult{Status: agentsdk.ProviderRunRunning, ExternalRunID: "external-timeout"}}
		executor, run := agentTaskRunnerExecutorFixture(t, runner)
		executor.dependencies.PollInterval = time.Minute
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
		defer cancel()
		_, err := executor.ExecuteAgentTask(ctx, run)
		var executionErr *AgentTaskExecutionError
		if !errors.As(err, &executionErr) || !executionErr.Retryable || !executionErr.Reconciliation.Required || executionErr.ExternalRunID != "external-timeout" {
			t.Fatalf("error=%#v", err)
		}
	})
	for name, result := range map[string]agentsdk.TaskResult{
		"outcome": {Status: agentsdk.ProviderRunCompleted, ExternalRunID: "external", Outcome: "root", Output: map[string]any{"score": 1}},
		"schema":  {Status: agentsdk.ProviderRunCompleted, ExternalRunID: "external", Outcome: "success", Output: map[string]any{"unknown": true}},
		"unknown": {Status: agentsdk.ProviderRunUnknown, ExternalRunID: "external"},
		"failed":  {Status: agentsdk.ProviderRunFailed, ExternalRunID: "external", ErrorClass: "provider_5xx", ErrorCode: "provider.unavailable", Retryable: true},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &agentTaskRunnerStub{startResult: result}
			executor, run := agentTaskRunnerExecutorFixture(t, runner)
			if _, err := executor.ExecuteAgentTask(t.Context(), run); err == nil {
				t.Fatal("expected classified failure")
			}
		})
	}
	runner := &agentTaskRunnerStub{startResult: agentsdk.TaskResult{Status: agentsdk.ProviderRunCancelled}}
	executor, run := agentTaskRunnerExecutorFixture(t, runner)
	if completion, err := executor.ExecuteAgentTask(t.Context(), run); err != nil || completion.Status != agentmodel.AgentTaskRunCancelled {
		t.Fatalf("provider cancelled=%#v err=%v", completion, err)
	}
}

func TestAgentTaskRunnerExecutorPropagatesAndReconcilesCancel(t *testing.T) {
	runner := &agentTaskRunnerStub{cancelResult: agentsdk.TaskResult{Status: agentsdk.ProviderRunUnknown}}
	executor, run := agentTaskRunnerExecutorFixture(t, runner)
	run.Attempts[0].ExternalRunID = "external-cancel"
	completion, err := executor.CancelAgentTask(t.Context(), run)
	if err != nil || completion.Status != agentmodel.AgentTaskRunCancelled || !completion.Reconciliation.Required || runner.cancels != 1 {
		t.Fatalf("completion=%#v cancels=%d err=%v", completion, runner.cancels, err)
	}
}

func TestAgentTaskRunnerExecutorDependencyAndProviderBoundaryMatrix(t *testing.T) {
	if executor := NewAgentTaskRunnerExecutor(AgentTaskRunnerExecutorDependencies{}); executor.dependencies.PollInterval != 500*time.Millisecond {
		t.Fatalf("default poll interval=%s", executor.dependencies.PollInterval)
	}
	runner := &agentTaskRunnerStub{}
	valid, run := agentTaskRunnerExecutorFixture(t, runner)
	for name, executor := range map[string]*AgentTaskRunnerExecutor{
		"nil":           nil,
		"runner":        NewAgentTaskRunnerExecutor(AgentTaskRunnerExecutorDependencies{}),
		"authorization": NewAgentTaskRunnerExecutor(AgentTaskRunnerExecutorDependencies{Runner: runner}),
		"credentials":   NewAgentTaskRunnerExecutor(AgentTaskRunnerExecutorDependencies{Runner: runner, Authorization: valid.dependencies.Authorization}),
	} {
		if _, err := executor.ExecuteAgentTask(t.Context(), run); apperror.CodeOf(err) != "agent.runner.unavailable" {
			t.Fatalf("execute %s=%v", name, err)
		}
	}
	badAuthorization := run
	badAuthorization.Evidence.Authorization = []agentmodel.AgentAuthorizationEvidence{{AllowedObjects: []string{"forbidden"}, AllowedActions: []string{"customer.update"}, AllowedOutcomes: []string{"success"}}}
	if _, err := valid.ExecuteAgentTask(t.Context(), badAuthorization); err == nil {
		t.Fatal("expected authorization failure")
	}
	badCredentials := *valid
	badCredentials.dependencies.Credentials = NewAgentTaskCredentialApplicationService([]byte("short"), agentTaskClock{now: time.Now()}, agentCredentialIDStub{})
	if _, err := badCredentials.ExecuteAgentTask(t.Context(), run); apperror.CodeOf(err) != "agent.credential.signer_unavailable" {
		t.Fatalf("credential error=%v", err)
	}
	withoutPriorAuthorization := run
	withoutPriorAuthorization.Evidence.Authorization = nil
	if _, err := valid.authorize(t.Context(), withoutPriorAuthorization); err != nil {
		t.Fatalf("authorize without prior evidence=%v", err)
	}
	run.TimeoutSeconds = 1
	for name, candidate := range map[string]*agentTaskRunnerStub{
		"start error":         {startErr: errors.New("start")},
		"missing external id": {startResult: agentsdk.TaskResult{Status: agentsdk.ProviderRunAccepted}},
		"poll error":          {startResult: agentsdk.TaskResult{Status: agentsdk.ProviderRunRunning, ExternalRunID: "external"}, pollErr: errors.New("poll")},
	} {
		executor, candidateRun := agentTaskRunnerExecutorFixture(t, candidate)
		candidateRun.TimeoutSeconds = run.TimeoutSeconds
		if _, err := executor.ExecuteAgentTask(t.Context(), candidateRun); err == nil {
			t.Fatalf("%s expected error", name)
		}
	}
}

func TestAgentTaskRunnerExecutorCancellationBoundaryMatrix(t *testing.T) {
	for name, executor := range map[string]*AgentTaskRunnerExecutor{"nil": nil, "runner": NewAgentTaskRunnerExecutor(AgentTaskRunnerExecutorDependencies{})} {
		if _, err := executor.CancelAgentTask(t.Context(), agentmodel.AgentTaskRun{}); apperror.CodeOf(err) != "agent.runner.unavailable" {
			t.Fatalf("cancel %s=%v", name, err)
		}
	}
	runner := &agentTaskRunnerStub{}
	executor, run := agentTaskRunnerExecutorFixture(t, runner)
	if completion, err := executor.CancelAgentTask(t.Context(), run); err != nil || completion.ErrorCode != "agent.task.cancelled" || runner.cancels != 0 {
		t.Fatalf("local cancel=%#v err=%v", completion, err)
	}
	run.Attempts[0].ExternalRunID = "external"
	for name, result := range map[string]struct {
		value agentsdk.TaskResult
		err   error
	}{
		"error":     {err: errors.New("cancel")},
		"unknown":   {value: agentsdk.TaskResult{Status: agentsdk.ProviderRunUnknown}},
		"running":   {value: agentsdk.TaskResult{Status: agentsdk.ProviderRunRunning}},
		"cancelled": {value: agentsdk.TaskResult{Status: agentsdk.ProviderRunCancelled}},
	} {
		runner.cancelResult, runner.cancelErr = result.value, result.err
		completion, err := executor.CancelAgentTask(t.Context(), run)
		if err != nil {
			t.Fatalf("cancel %s=%v", name, err)
		}
		if name == "cancelled" && completion.Reconciliation.Required {
			t.Fatalf("confirmed=%#v", completion)
		}
		if name != "cancelled" && !completion.Reconciliation.Required {
			t.Fatalf("uncertain %s=%#v", name, completion)
		}
	}
}

func TestAgentTaskRunnerHelperBoundaryMatrix(t *testing.T) {
	if err := agentRunnerError(agentsdk.TaskResult{}, "external", errors.New("provider")); err.(*AgentTaskExecutionError).Code != "agent.runner.provider_failed" {
		t.Fatalf("runner error=%#v", err)
	}
	base := agentmodel.AgentTaskRun{Reconciliation: agentmodel.AgentTaskReconciliation{ExternalRunID: "fallback"}, Attempts: []agentmodel.AgentTaskAttempt{{ExternalRunID: " old "}, {}, {ExternalRunID: " latest "}}}
	if got := latestAgentExternalRunID(base); got != "latest" {
		t.Fatalf("latest=%q", got)
	}
	base.Attempts = nil
	if got := latestAgentExternalRunID(base); got != "fallback" {
		t.Fatalf("fallback=%q", got)
	}
	base.Reconciliation.State = "force_restart"
	if got := latestAgentExternalRunID(base); got != "" {
		t.Fatalf("restart=%q", got)
	}
	for seconds, want := range map[int]time.Duration{0: 5 * time.Minute, 1: time.Second, 901: 15 * time.Minute} {
		if got := agentTaskCredentialTTL(seconds); got != want {
			t.Fatalf("ttl %d=%s", seconds, got)
		}
	}
	for outcome, want := range map[string]agentmodel.AgentTaskRunStatus{"success": agentmodel.AgentTaskRunSucceeded, "manual_review": agentmodel.AgentTaskRunManualReview, "rejected": agentmodel.AgentTaskRunRejected, "no_result": agentmodel.AgentTaskRunNoResult, "other": agentmodel.AgentTaskRunFailed} {
		if got := agentTaskStatusForOutcome(outcome); got != want {
			t.Fatalf("outcome %s=%s", outcome, got)
		}
	}
	schema := map[string]any{"type": "object", "required": []string{"value"}, "properties": map[string]any{"value": map[string]any{"type": "string"}}, "additionalProperties": false}
	for name, output := range map[string]map[string]any{
		"marshal": {"value": make(chan int)}, "oversize": {"value": "long"}, "missing": {}, "unknown": {"value": "ok", "other": true}, "type": {"value": 1},
	} {
		limit := 64 * 1024
		if name == "oversize" {
			limit = 2
		}
		if err := validateAgentTaskOutput(schema, output, limit); err == nil {
			t.Fatalf("%s expected validation error", name)
		}
	}
	if err := validateAgentTaskOutput(map[string]any{"type": "array"}, nil, 0); err == nil {
		t.Fatal("expected schema type error")
	}
	openSchema := map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": true}
	if err := validateAgentTaskOutput(openSchema, map[string]any{"other": true}, 0); err != nil {
		t.Fatalf("open schema=%v", err)
	}
	if err := validateAgentTaskOutput(map[string]any{"type": "object", "properties": map[string]any{}}, map[string]any{"other": true}, 0); err != nil {
		t.Fatalf("unspecified additional properties=%v", err)
	}
	values := []struct {
		schema any
		value  any
		want   bool
	}{
		{map[string]any{"type": "string"}, "x", true}, {map[string]any{"type": "string"}, 1, false},
		{map[string]any{"type": "number"}, json.Number("1"), true}, {map[string]any{"type": "integer"}, "1", false},
		{map[string]any{"type": "boolean"}, true, true}, {map[string]any{"type": "boolean"}, "true", false},
		{map[string]any{"type": "object"}, map[string]any{}, true}, {map[string]any{"type": "object"}, []any{}, false},
		{map[string]any{"type": "array"}, []any{}, true}, {map[string]any{"type": "array"}, map[string]any{}, false},
		{map[string]any{"type": "unknown"}, nil, true},
	}
	for _, value := range values {
		if got := agentJSONTypeMatches(value.schema, value.value); got != value.want {
			t.Fatalf("type schema=%#v value=%#v got=%v", value.schema, value.value, got)
		}
	}
}
