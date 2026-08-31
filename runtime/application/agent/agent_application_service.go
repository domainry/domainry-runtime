package agent

import (
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
)

type AgentApplicationService struct {
	repository agentpersistence.AgentStateRepository
}

func NewAgentApplicationService(repository agentpersistence.AgentStateRepository) *AgentApplicationService {
	return &AgentApplicationService{repository: repository}
}
