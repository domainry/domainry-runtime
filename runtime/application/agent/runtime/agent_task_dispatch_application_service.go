package runtime

import (
	"context"
	"fmt"
	"strings"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

type AgentTaskDispatchRequest struct {
	RunID                  string
	WorkspaceID            string
	ProcessID              string
	NodeInstanceID         string
	NodeID                 string
	Iteration              int
	DefinitionSnapshotHash string
	ManifestHash           string
	TaskKey                string
	TaskVersion            string
	Identity               agentmodel.AgentTaskIdentity
	Input                  map[string]any
	AllowedObjects         []string
	AllowedActions         []string
	AllowedOutcomes        []string
	TimeoutSeconds         int
	MaxAttempts            int
	Initiator              principalmodel.Principal
	CorrelationID          string
}

type AgentInteractiveTaskDispatchRequest struct {
	RunID, InteractiveRunID, WorkspaceID, TaskKey, TaskVersion string
	IdempotencyKey, ManifestHash, CorrelationID                string
	Identity                                                   agentmodel.AgentTaskIdentity
	Input                                                      map[string]any
	AllowedObjects, AllowedActions, AllowedOutcomes            []string
	TimeoutSeconds, MaxAttempts                                int
	Initiator                                                  principalmodel.Principal
}

type AgentTaskDispatchApplicationService struct {
	authorization *AgentAuthorizationApplicationService
	clock         workerplatform.Clock
	ids           workerplatform.IdentifierGenerator
}

func NewAgentTaskDispatchApplicationService(authorization *AgentAuthorizationApplicationService, clock workerplatform.Clock, ids workerplatform.IdentifierGenerator) *AgentTaskDispatchApplicationService {
	if clock == nil {
		clock = workerplatform.SystemClock{}
	}
	if ids == nil {
		ids = workerplatform.CryptoIdentifierGenerator{}
	}
	return &AgentTaskDispatchApplicationService{authorization: authorization, clock: clock, ids: ids}
}

func (s *AgentTaskDispatchApplicationService) Prepare(ctx context.Context, request AgentTaskDispatchRequest) (agentmodel.AgentTaskRun, error) {
	if err := ctx.Err(); err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	if s == nil || s.authorization == nil {
		return agentmodel.AgentTaskRun{}, apperror.New(apperror.KindUnavailable, "agent.task.dispatch_unavailable", nil, nil)
	}
	workspace, err := principalmodel.NewWorkspaceID(request.WorkspaceID)
	if err != nil || workspace.String() != strings.TrimSpace(request.Initiator.WorkspaceID) {
		return agentmodel.AgentTaskRun{}, apperror.New(apperror.KindBadRequest, "agent.task.dispatch_invalid", err, nil)
	}
	if strings.TrimSpace(request.ProcessID) == "" || strings.TrimSpace(request.NodeInstanceID) == "" || request.Iteration < 1 {
		return agentmodel.AgentTaskRun{}, apperror.New(apperror.KindBadRequest, "agent.task.dispatch_invalid", err, nil)
	}
	authorization, err := s.authorization.AuthorizeTask(ctx, AgentTaskAuthorizationRequest{
		Initiator: request.Initiator, Identity: request.Identity, TaskKey: request.TaskKey, TaskVersion: request.TaskVersion,
		NodeAllowedObjects: request.AllowedObjects, NodeAllowedActions: request.AllowedActions, NodeAllowedOutcomes: request.AllowedOutcomes,
	})
	if err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	now := s.clock.Now().UTC()
	maxAttempts := request.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	runID := strings.TrimSpace(request.RunID)
	if runID == "" {
		runID = "agent_task_" + s.ids.NewID()
	}
	run := agentmodel.AgentTaskRun{
		ID: runID, WorkspaceID: workspace.String(), ProcessID: strings.TrimSpace(request.ProcessID), NodeInstanceID: strings.TrimSpace(request.NodeInstanceID),
		TaskKey: authorization.Task.Key, TaskVersion: authorization.Task.Version, Status: agentmodel.AgentTaskRunPending, Identity: authorization.Identity,
		Input: request.Input, MaxAttempts: maxAttempts, TimeoutSeconds: request.TimeoutSeconds,
		IdempotencyKey: fmt.Sprintf("%s:%s:%d", strings.TrimSpace(request.ProcessID), strings.TrimSpace(request.NodeID), request.Iteration),
		CorrelationID:  strings.TrimSpace(request.CorrelationID), CreatedAt: now, UpdatedAt: now, Revision: 1,
		Evidence: agentmodel.AgentTaskExecutionEvidence{ManifestHash: strings.TrimSpace(request.ManifestHash), DefinitionSnapshotHash: strings.TrimSpace(request.DefinitionSnapshotHash), TaskVersion: authorization.Task.Version, AgentKey: authorization.Task.AgentKey, Authorization: []agentmodel.AgentAuthorizationEvidence{authorization.Evidence}},
	}
	if run.TimeoutSeconds <= 0 {
		run.TimeoutSeconds = authorization.Task.ExecutionLimits.TimeoutSeconds
	}
	if !run.ValidForCreate() {
		return agentmodel.AgentTaskRun{}, apperror.New(apperror.KindBadRequest, "agent.task.dispatch_invalid", nil, nil)
	}
	return run, nil
}

func (s *AgentTaskDispatchApplicationService) PrepareInteractive(ctx context.Context, request AgentInteractiveTaskDispatchRequest) (agentmodel.AgentTaskRun, error) {
	if err := ctx.Err(); err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	if s == nil || s.authorization == nil {
		return agentmodel.AgentTaskRun{}, apperror.New(apperror.KindUnavailable, "agent.task.dispatch_unavailable", nil, nil)
	}
	workspace, err := principalmodel.NewWorkspaceID(request.WorkspaceID)
	if err != nil || workspace.String() != strings.TrimSpace(request.Initiator.WorkspaceID) {
		return agentmodel.AgentTaskRun{}, apperror.New(apperror.KindBadRequest, "agent.task.dispatch_invalid", err, nil)
	}
	if strings.TrimSpace(request.InteractiveRunID) == "" || strings.TrimSpace(request.IdempotencyKey) == "" {
		return agentmodel.AgentTaskRun{}, apperror.New(apperror.KindBadRequest, "agent.task.dispatch_invalid", err, nil)
	}
	authorization, err := s.authorization.AuthorizeTask(ctx, AgentTaskAuthorizationRequest{
		Initiator: request.Initiator, Identity: request.Identity, TaskKey: request.TaskKey, TaskVersion: request.TaskVersion,
		NodeAllowedObjects: request.AllowedObjects, NodeAllowedActions: request.AllowedActions, NodeAllowedOutcomes: request.AllowedOutcomes,
	})
	if err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	now := s.clock.Now().UTC()
	runID := strings.TrimSpace(request.RunID)
	if runID == "" {
		runID = "agent_task_" + s.ids.NewID()
	}
	maxAttempts := request.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	run := agentmodel.AgentTaskRun{
		ID: runID, WorkspaceID: workspace.String(), InteractiveRunID: strings.TrimSpace(request.InteractiveRunID), TaskKey: authorization.Task.Key, TaskVersion: authorization.Task.Version,
		Status: agentmodel.AgentTaskRunPending, Identity: authorization.Identity, Input: request.Input, MaxAttempts: maxAttempts, TimeoutSeconds: request.TimeoutSeconds,
		IdempotencyKey: strings.TrimSpace(request.IdempotencyKey), CorrelationID: strings.TrimSpace(request.CorrelationID), CreatedAt: now, UpdatedAt: now, Revision: 1,
		Evidence: agentmodel.AgentTaskExecutionEvidence{ManifestHash: strings.TrimSpace(request.ManifestHash), TaskVersion: authorization.Task.Version, AgentKey: authorization.Task.AgentKey, Authorization: []agentmodel.AgentAuthorizationEvidence{authorization.Evidence}},
	}
	if run.TimeoutSeconds <= 0 {
		run.TimeoutSeconds = authorization.Task.ExecutionLimits.TimeoutSeconds
	}
	if !run.ValidForCreate() {
		return agentmodel.AgentTaskRun{}, apperror.New(apperror.KindBadRequest, "agent.task.dispatch_invalid", nil, nil)
	}
	return run, nil
}
