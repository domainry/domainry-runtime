package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type AgentTaskRunnerExecutorDependencies struct {
	Runner        agentsdk.TaskRunner
	Authorization *AgentAuthorizationApplicationService
	Credentials   *AgentTaskCredentialApplicationService
	PollInterval  time.Duration
}

type AgentTaskRunnerExecutor struct {
	dependencies AgentTaskRunnerExecutorDependencies
}

func NewAgentTaskRunnerExecutor(dependencies AgentTaskRunnerExecutorDependencies) *AgentTaskRunnerExecutor {
	if dependencies.PollInterval <= 0 {
		dependencies.PollInterval = 500 * time.Millisecond
	}
	return &AgentTaskRunnerExecutor{dependencies: dependencies}
}

func (e *AgentTaskRunnerExecutor) ExecuteAgentTask(ctx context.Context, run agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
	if e == nil || e.dependencies.Runner == nil || e.dependencies.Authorization == nil || e.dependencies.Credentials == nil {
		return AgentTaskRunCompletion{}, apperror.New(apperror.KindUnavailable, "agent.runner.unavailable", nil, nil)
	}
	ctx = requestcontext.WithWorkspaceID(ctx, run.WorkspaceID)
	authorization, err := e.authorize(ctx, run)
	if err != nil {
		return AgentTaskRunCompletion{}, err
	}
	credential, err := e.dependencies.Credentials.Issue(ctx, AgentTaskCredentialClaims{WorkspaceID: run.WorkspaceID, ProcessID: run.ProcessID, TaskRunID: run.ID, Principal: agentPrincipalReference(authorization.Principal), AllowedTools: authorization.AllowedTools}, agentTaskCredentialTTL(run.TimeoutSeconds))
	if err != nil {
		return AgentTaskRunCompletion{}, err
	}
	request := agentsdk.TaskRequest{TaskRunID: run.ID, ProcessID: run.ProcessID, WorkspaceID: run.WorkspaceID, Task: authorization.Task, Identity: authorization.Identity, Input: run.Input, ExecutionCredential: credential, CorrelationID: run.CorrelationID, IdempotencyKey: run.IdempotencyKey}
	if run.TimeoutSeconds > 0 {
		request.Deadline = time.Now().UTC().Add(time.Duration(run.TimeoutSeconds) * time.Second)
	}
	externalRunID := latestAgentExternalRunID(run)
	var result agentsdk.TaskResult
	if externalRunID == "" {
		result, err = e.dependencies.Runner.Start(ctx, request)
		externalRunID = strings.TrimSpace(result.ExternalRunID)
	} else {
		result, err = e.dependencies.Runner.Poll(ctx, externalRunID, run.IdempotencyKey)
	}
	if err != nil {
		return AgentTaskRunCompletion{}, agentRunnerError(result, externalRunID, err)
	}
	for result.Status == agentsdk.ProviderRunAccepted || result.Status == agentsdk.ProviderRunRunning {
		if externalRunID == "" {
			return AgentTaskRunCompletion{}, &AgentTaskExecutionError{Class: "provider_contract", Code: "agent.runner.external_run_id_required"}
		}
		select {
		case <-ctx.Done():
			return AgentTaskRunCompletion{}, &AgentTaskExecutionError{Class: "local_timeout", Code: "agent.runner.local_timeout", Retryable: true, ExternalRunID: externalRunID, Reconciliation: agentmodel.AgentTaskReconciliation{Required: true, ExternalRunID: externalRunID, State: "poll_required", Reason: "local_context_ended"}, Cause: ctx.Err()}
		case <-time.After(e.dependencies.PollInterval):
		}
		result, err = e.dependencies.Runner.Poll(ctx, externalRunID, run.IdempotencyKey)
		if err != nil {
			return AgentTaskRunCompletion{}, agentRunnerError(result, externalRunID, err)
		}
	}
	return e.completion(authorization, result, externalRunID, run.Evidence)
}

func (e *AgentTaskRunnerExecutor) CancelAgentTask(ctx context.Context, run agentmodel.AgentTaskRun) (AgentTaskRunCompletion, error) {
	if e == nil || e.dependencies.Runner == nil {
		return AgentTaskRunCompletion{}, apperror.New(apperror.KindUnavailable, "agent.runner.unavailable", nil, nil)
	}
	externalRunID := latestAgentExternalRunID(run)
	if externalRunID == "" {
		return AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunCancelled, Outcome: "cancelled", ErrorCode: "agent.task.cancelled"}, nil
	}
	result, err := e.dependencies.Runner.Cancel(ctx, externalRunID, run.IdempotencyKey)
	if err != nil || result.Status == agentsdk.ProviderRunUnknown || result.Status == agentsdk.ProviderRunRunning {
		return AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunCancelled, Outcome: "cancelled", ErrorCode: "agent.task.cancel_uncertain", Reconciliation: agentmodel.AgentTaskReconciliation{Required: true, ExternalRunID: externalRunID, State: "cancel_uncertain", Reason: "provider_cancel_unconfirmed"}}, nil
	}
	return AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunCancelled, Outcome: "cancelled", ErrorCode: "agent.task.cancelled", Reconciliation: agentmodel.AgentTaskReconciliation{ExternalRunID: externalRunID, State: "cancelled"}}, nil
}

func (e *AgentTaskRunnerExecutor) authorize(ctx context.Context, run agentmodel.AgentTaskRun) (AgentTaskAuthorization, error) {
	ctx = requestcontext.WithWorkspaceID(ctx, run.WorkspaceID)
	initiator := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: run.Identity.Initiator.UserID, WorkspaceID: run.Identity.Initiator.WorkspaceID, RoleKey: run.Identity.Initiator.RoleKey, AuthorizationRevision: run.Identity.Initiator.AuthorizationRevision}, CorrelationID: run.CorrelationID}
	objects, actions, outcomes := []string(nil), []string(nil), []string(nil)
	if count := len(run.Evidence.Authorization); count > 0 {
		decision := run.Evidence.Authorization[count-1]
		objects, actions, outcomes = decision.AllowedObjects, decision.AllowedActions, decision.AllowedOutcomes
	}
	return e.dependencies.Authorization.AuthorizeTask(ctx, AgentTaskAuthorizationRequest{Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: run.Identity.Mode, PrincipalKey: run.Identity.ServicePrincipalKey}, ExpectedRotationVersion: run.Identity.ServiceRotationVersion, TaskKey: run.TaskKey, TaskVersion: run.TaskVersion, NodeAllowedObjects: objects, NodeAllowedActions: actions, NodeAllowedOutcomes: outcomes})
}

func (e *AgentTaskRunnerExecutor) completion(authorization AgentTaskAuthorization, result agentsdk.TaskResult, externalRunID string, evidence agentmodel.AgentTaskExecutionEvidence) (AgentTaskRunCompletion, error) {
	switch result.Status {
	case agentsdk.ProviderRunCompleted:
		if !agentContains(authorization.AllowedOutcomes, result.Outcome) {
			return AgentTaskRunCompletion{}, &AgentTaskExecutionError{Class: "output_validation", Code: "agent.runner.outcome_invalid", ExternalRunID: externalRunID}
		}
		if err := validateAgentTaskOutput(authorization.Task.OutputSchema, result.Output, authorization.Task.ExecutionLimits.MaxOutputBytes); err != nil {
			return AgentTaskRunCompletion{}, &AgentTaskExecutionError{Class: "output_validation", Code: "agent.runner.output_invalid", ExternalRunID: externalRunID, Cause: err}
		}
		status := agentTaskStatusForOutcome(result.Outcome)
		evidence.TaskVersion, evidence.AgentKey, evidence.Model, evidence.Usage = authorization.Task.Version, authorization.Task.AgentKey, result.Model, result.Usage
		evidence.Authorization = append(evidence.Authorization, authorization.Evidence)
		return AgentTaskRunCompletion{Status: status, Outcome: result.Outcome, Output: result.Output, ExternalRunID: externalRunID, RawEvidenceRef: agentStableHash(result.RawEvidence), Evidence: evidence, Reconciliation: agentmodel.AgentTaskReconciliation{ExternalRunID: externalRunID, State: "completed"}}, nil
	case agentsdk.ProviderRunCancelled:
		return AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunCancelled, Outcome: "cancelled", ErrorCode: "agent.runner.cancelled"}, nil
	case agentsdk.ProviderRunFailed:
		return AgentTaskRunCompletion{}, agentRunnerError(result, externalRunID, nil)
	default:
		return AgentTaskRunCompletion{}, &AgentTaskExecutionError{Class: "provider_unknown", Code: "agent.runner.status_unknown", Retryable: true, ExternalRunID: externalRunID, Reconciliation: agentmodel.AgentTaskReconciliation{Required: true, ExternalRunID: externalRunID, State: "poll_required", Reason: "provider_status_unknown"}}
	}
}

func agentRunnerError(result agentsdk.TaskResult, externalRunID string, cause error) error {
	code := strings.TrimSpace(result.ErrorCode)
	if code == "" {
		code = "agent.runner.provider_failed"
	}
	return &AgentTaskExecutionError{Class: strings.TrimSpace(result.ErrorClass), Code: code, Retryable: result.Retryable, ExternalRunID: externalRunID, Cause: cause}
}

func latestAgentExternalRunID(run agentmodel.AgentTaskRun) string {
	if run.Reconciliation.State == "force_restart" {
		return ""
	}
	for index := len(run.Attempts) - 1; index >= 0; index-- {
		if value := strings.TrimSpace(run.Attempts[index].ExternalRunID); value != "" {
			return value
		}
	}
	return strings.TrimSpace(run.Reconciliation.ExternalRunID)
}

func agentTaskCredentialTTL(timeoutSeconds int) time.Duration {
	ttl := time.Duration(timeoutSeconds) * time.Second
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if ttl > 15*time.Minute {
		ttl = 15 * time.Minute
	}
	return ttl
}

func agentTaskStatusForOutcome(outcome string) agentmodel.AgentTaskRunStatus {
	switch strings.TrimSpace(outcome) {
	case "success":
		return agentmodel.AgentTaskRunSucceeded
	case "manual_review":
		return agentmodel.AgentTaskRunManualReview
	case "rejected":
		return agentmodel.AgentTaskRunRejected
	case "no_result":
		return agentmodel.AgentTaskRunNoResult
	default:
		return agentmodel.AgentTaskRunFailed
	}
}

func validateAgentTaskOutput(schema map[string]any, output map[string]any, maxBytes int) error {
	raw, err := json.Marshal(output)
	if err != nil {
		return err
	}
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}
	if len(raw) > maxBytes {
		return fmt.Errorf("output exceeds %d bytes", maxBytes)
	}
	if strings.TrimSpace(fmt.Sprint(schema["type"])) != "object" {
		return fmt.Errorf("output schema type must be object")
	}
	required := agentStringsFromAny(schema["required"])
	for _, field := range required {
		if _, exists := output[field]; !exists {
			return fmt.Errorf("required field %s is missing", field)
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	additional, hasAdditional := schema["additionalProperties"].(bool)
	for key, value := range output {
		property, exists := properties[key]
		if !exists && hasAdditional && !additional {
			return fmt.Errorf("unknown output field %s", key)
		}
		if exists && !agentJSONTypeMatches(property, value) {
			return fmt.Errorf("output field %s has invalid type", key)
		}
	}
	return nil
}

func agentJSONTypeMatches(schema any, value any) bool {
	contract, _ := schema.(map[string]any)
	switch strings.TrimSpace(fmt.Sprint(contract["type"])) {
	case "string":
		_, ok := value.(string)
		return ok
	case "number", "integer":
		switch value.(type) {
		case int, int32, int64, float32, float64, json.Number:
			return true
		}
		return false
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	default:
		return true
	}
}
