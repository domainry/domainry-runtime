package agenthost

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
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
	Identity               agentsdk.AgentTaskIdentity
	Input                  map[string]any
	AllowedObjects         []string
	AllowedActions         []string
	AllowedOutcomes        []string
	TimeoutSeconds         int
	MaxAttempts            int
	Initiator              principalmodel.Principal
	CorrelationID          string
}

// AgentTaskDispatchApplicationService is a Runtime Host adapter: it validates
// and authorizes a workflow command, then projects it into the SDK protocol.
// It never constructs or persists Agent-owned task state.
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

func (s *AgentTaskDispatchApplicationService) PrepareRequest(ctx context.Context, request AgentTaskDispatchRequest) (agentsdk.TaskRequest, error) {
	if err := ctx.Err(); err != nil {
		return agentsdk.TaskRequest{}, err
	}
	if s == nil || s.authorization == nil {
		return agentsdk.TaskRequest{}, apperror.New(apperror.KindUnavailable, "agent.task.dispatch_unavailable", nil, nil)
	}
	workspace, err := principalmodel.NewWorkspaceID(request.WorkspaceID)
	if err != nil || workspace.String() != strings.TrimSpace(request.Initiator.WorkspaceID) || strings.TrimSpace(request.ProcessID) == "" || strings.TrimSpace(request.NodeInstanceID) == "" || request.Iteration < 1 {
		return agentsdk.TaskRequest{}, apperror.New(apperror.KindBadRequest, "agent.task.dispatch_invalid", err, nil)
	}
	authorization, err := s.authorization.AuthorizeTask(ctx, AgentTaskAuthorizationRequest{
		Initiator: request.Initiator, Identity: request.Identity, TaskKey: request.TaskKey, TaskVersion: request.TaskVersion,
		NodeAllowedObjects: request.AllowedObjects, NodeAllowedActions: request.AllowedActions, NodeAllowedOutcomes: request.AllowedOutcomes,
	})
	if err != nil {
		return agentsdk.TaskRequest{}, err
	}
	runID := strings.TrimSpace(request.RunID)
	if runID == "" {
		runID = "agent_task_" + s.ids.NewID()
	}
	maxAttempts := request.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	timeoutSeconds := request.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = authorization.Task.ExecutionLimits.TimeoutSeconds
	}
	command := agentsdk.TaskRequest{
		TaskRunID: runID, WorkspaceID: workspace.String(), ProcessID: strings.TrimSpace(request.ProcessID), NodeInstanceID: strings.TrimSpace(request.NodeInstanceID),
		Task: authorization.Task, Identity: authorization.Identity, Input: cloneAgentHostMap(request.Input),
		AllowedObjects: append([]string(nil), authorization.AllowedObjects...), AllowedActions: append([]string(nil), authorization.AllowedActions...),
		AllowedOutcomes: append([]string(nil), authorization.AllowedOutcomes...), AllowedTools: append([]string(nil), authorization.AllowedTools...),
		CorrelationID: strings.TrimSpace(request.CorrelationID), IdempotencyKey: fmt.Sprintf("%s:%s:%d", strings.TrimSpace(request.ProcessID), strings.TrimSpace(request.NodeID), request.Iteration), MaxAttempts: maxAttempts,
	}
	if timeoutSeconds > 0 {
		now := s.clock.Now().UTC()
		if now.IsZero() {
			return agentsdk.TaskRequest{}, apperror.New(apperror.KindBadRequest, "agent.task.dispatch_invalid", nil, map[string]string{"field": "clock"})
		}
		command.Deadline = now.Add(time.Duration(timeoutSeconds) * time.Second)
	}
	return command, nil
}

func cloneAgentHostMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
