package agent

import (
	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"
)

type AgentApplicationService struct {
	repository agentrepository.AgentStateRepository
}

func NewAgentApplicationService(repository agentrepository.AgentStateRepository) *AgentApplicationService {
	return &AgentApplicationService{repository: repository}
}
