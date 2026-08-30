package agent

import (
	agentrepository "github.com/domainry/domainry-agent-sdk/repository"
)

type AgentApplicationService struct {
	repository agentrepository.AgentStateRepository
}

func NewAgentApplicationService(repository agentrepository.AgentStateRepository) *AgentApplicationService {
	return &AgentApplicationService{repository: repository}
}
