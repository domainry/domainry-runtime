package runtime

import (
	"context"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type AgentTaskLocator struct {
	WorkspaceID string
	RunID       string
}

type AgentTaskWakeup func(AgentTaskLocator)

func BindAgentTaskRunWakeup(service *AgentTaskRunApplicationService, wakeup AgentTaskWakeup) {
	if service != nil {
		service.wakeup = wakeup
	}
}

func BindAgentInteractiveRunWakeup(service *AgentInteractiveRunApplicationService, wakeup AgentTaskWakeup) {
	if service != nil {
		service.wakeup = wakeup
	}
}

func (s *AgentTaskRunApplicationService) wake(run agentmodel.AgentTaskRun) {
	if s == nil || s.wakeup == nil || strings.TrimSpace(run.ID) == "" {
		return
	}
	workspace, err := principalmodel.NewWorkspaceID(run.WorkspaceID)
	if err != nil {
		return
	}
	s.wakeup(AgentTaskLocator{WorkspaceID: workspace.String(), RunID: strings.TrimSpace(run.ID)})
}

func (s *AgentTaskRunApplicationService) ClaimTask(ctx context.Context, locator AgentTaskLocator, owner workerplatform.WorkerID, leaseDuration time.Duration) (agentrepository.AgentTaskClaim, bool, error) {
	if s == nil {
		return agentrepository.AgentTaskClaim{}, false, apperror.New(apperror.KindUnavailable, "agent.task.worker_unavailable", nil, nil)
	}
	repository, ok := s.repository.(agentrepository.AgentTaskRunDirectClaimRepository)
	workspace, workspaceErr := principalmodel.NewWorkspaceID(locator.WorkspaceID)
	if !ok || workspaceErr != nil {
		return agentrepository.AgentTaskClaim{}, false, apperror.New(apperror.KindBadRequest, "agent.task.claim_invalid", nil, nil)
	}
	if strings.TrimSpace(locator.RunID) == "" || strings.TrimSpace(owner.String()) == "" || leaseDuration <= 0 {
		return agentrepository.AgentTaskClaim{}, false, apperror.New(apperror.KindBadRequest, "agent.task.claim_invalid", nil, nil)
	}
	return repository.ClaimAgentTaskRun(ctx, workspace.String(), strings.TrimSpace(locator.RunID), owner.String(), s.clock.Now().UTC(), leaseDuration)
}
