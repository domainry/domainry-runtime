package runtime

import (
	"context"
	"encoding/json"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/apperror"
)

type AgentRouterApplicationService struct{ router AgentRouter }

func NewAgentRouterApplicationService(router AgentRouter) *AgentRouterApplicationService {
	return &AgentRouterApplicationService{router: router}
}

func (s *AgentRouterApplicationService) Route(ctx context.Context, runtimeContext agentsdk.GlobalContext, message string, candidates []agentsdk.RouteCandidate) (agentsdk.RouteResult, error) {
	if s == nil || s.router == nil {
		return agentsdk.RouteResult{}, apperror.New(apperror.KindUnavailable, "agent.router.unavailable", nil, nil)
	}
	result, err := s.router.Route(ctx, runtimeContext, strings.TrimSpace(message), candidates)
	if err != nil {
		return agentsdk.RouteResult{}, err
	}
	return ValidateAgentRouteResult(result, candidates)
}

func ValidateAgentRouteResult(result agentsdk.RouteResult, candidates []agentsdk.RouteCandidate) (agentsdk.RouteResult, error) {
	allowed := map[string]agentsdk.RouteCandidate{}
	for _, candidate := range candidates {
		candidate.RouteType, candidate.TargetKey, candidate.Version = strings.TrimSpace(candidate.RouteType), strings.TrimSpace(candidate.TargetKey), strings.TrimSpace(candidate.Version)
		if candidate.TargetKey != "" {
			allowed[candidate.RouteType+"\x00"+candidate.TargetKey+"\x00"+candidate.Version] = candidate
		}
	}
	result.RouteType, result.TargetKey, result.TargetVersion = strings.TrimSpace(result.RouteType), strings.TrimSpace(result.TargetKey), strings.TrimSpace(result.TargetVersion)
	if _, ok := allowed[result.RouteType+"\x00"+result.TargetKey+"\x00"+result.TargetVersion]; !ok {
		return agentsdk.RouteResult{}, apperror.New(apperror.KindForbidden, "agent.router.target_denied", nil, nil)
	}
	switch result.RouteType {
	case agentsdk.AgentRouteInteractiveQuery, agentsdk.AgentRouteTask, agentsdk.AgentRouteWorkflow, agentsdk.AgentRouteProposal:
	default:
		return agentsdk.RouteResult{}, apperror.New(apperror.KindForbidden, "agent.router.route_type_denied", nil, nil)
	}
	encoded, err := json.Marshal(result.Input)
	if err != nil || len(encoded) > 64*1024 {
		return agentsdk.RouteResult{}, apperror.New(apperror.KindBadRequest, "agent.router.input_invalid", err, nil)
	}
	if result.RouteType != agentsdk.AgentRouteInteractiveQuery && strings.TrimSpace(result.IdempotencyKey) == "" {
		return agentsdk.RouteResult{}, apperror.New(apperror.KindBadRequest, "agent.router.idempotency_required", nil, nil)
	}
	return result, nil
}
