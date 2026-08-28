package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type agentRouterFunc func(context.Context, agentmodel.GlobalAgentContext, string, []AgentRouteCandidate) (AgentRouteResult, error)

func (fn agentRouterFunc) Route(ctx context.Context, runtimeContext agentmodel.GlobalAgentContext, message string, candidates []AgentRouteCandidate) (AgentRouteResult, error) {
	return fn(ctx, runtimeContext, message, candidates)
}

func TestAgentRouterAcceptsOnlyRuntimeFilteredPublishedTargets(t *testing.T) {
	candidates := []AgentRouteCandidate{{RouteType: agentmodel.AgentRouteTask, TargetKey: "resume.screen", Version: "v1"}}
	service := NewAgentRouterApplicationService(agentRouterFunc(func(_ context.Context, _ agentmodel.GlobalAgentContext, message string, got []AgentRouteCandidate) (AgentRouteResult, error) {
		if message != "screen" || len(got) != 1 {
			t.Fatalf("message=%q candidates=%v", message, got)
		}
		return AgentRouteResult{RouteType: agentmodel.AgentRouteTask, TargetKey: "resume.screen", TargetVersion: "v1", Input: map[string]any{"candidate_id": "1"}, IdempotencyKey: "handoff-1"}, nil
	}))
	result, err := service.Route(t.Context(), agentmodel.GlobalAgentContext{}, " screen ", candidates)
	if err != nil || result.TargetKey != "resume.screen" {
		t.Fatalf("result=%#v err=%v", result, err)
	}

	for name, result := range map[string]AgentRouteResult{
		"unknown target":  {RouteType: agentmodel.AgentRouteTask, TargetKey: "forged", TargetVersion: "v1", IdempotencyKey: "key"},
		"unknown version": {RouteType: agentmodel.AgentRouteTask, TargetKey: "resume.screen", TargetVersion: "v2", IdempotencyKey: "key"},
		"arbitrary url":   {RouteType: "https://evil.invalid", TargetKey: "resume.screen", TargetVersion: "v1", IdempotencyKey: "key"},
	} {
		t.Run(name, func(t *testing.T) {
			denied := NewAgentRouterApplicationService(agentRouterFunc(func(context.Context, agentmodel.GlobalAgentContext, string, []AgentRouteCandidate) (AgentRouteResult, error) {
				return result, nil
			}))
			if _, err := denied.Route(t.Context(), agentmodel.GlobalAgentContext{}, "", candidates); err == nil {
				t.Fatal("forged route accepted")
			}
		})
	}
}

func TestAgentRouterRequiresDurableIdempotencyAndBoundsInput(t *testing.T) {
	candidate := AgentRouteCandidate{RouteType: agentmodel.AgentRouteWorkflow, TargetKey: "flow", Version: "v1"}
	result := AgentRouteResult{RouteType: candidate.RouteType, TargetKey: candidate.TargetKey, TargetVersion: candidate.Version}
	service := NewAgentRouterApplicationService(agentRouterFunc(func(context.Context, agentmodel.GlobalAgentContext, string, []AgentRouteCandidate) (AgentRouteResult, error) {
		return result, nil
	}))
	if _, err := service.Route(t.Context(), agentmodel.GlobalAgentContext{}, "", []AgentRouteCandidate{candidate}); apperror.CodeOf(err) != "agent.router.idempotency_required" {
		t.Fatalf("idempotency err=%v", err)
	}
	result.IdempotencyKey, result.Input = "key", map[string]any{"large": strings.Repeat("x", 65*1024)}
	if _, err := service.Route(t.Context(), agentmodel.GlobalAgentContext{}, "", []AgentRouteCandidate{candidate}); apperror.CodeOf(err) != "agent.router.input_invalid" {
		t.Fatalf("input err=%v", err)
	}
	if _, err := (*AgentRouterApplicationService)(nil).Route(t.Context(), agentmodel.GlobalAgentContext{}, "", nil); apperror.CodeOf(err) != "agent.router.unavailable" {
		t.Fatalf("nil router err=%v", err)
	}
	if _, err := NewAgentRouterApplicationService(nil).Route(t.Context(), agentmodel.GlobalAgentContext{}, "", nil); apperror.CodeOf(err) != "agent.router.unavailable" {
		t.Fatalf("missing router err=%v", err)
	}
	wantErr := errors.New("router failed")
	if _, err := NewAgentRouterApplicationService(agentRouterFunc(func(context.Context, agentmodel.GlobalAgentContext, string, []AgentRouteCandidate) (AgentRouteResult, error) {
		return AgentRouteResult{}, wantErr
	})).Route(t.Context(), agentmodel.GlobalAgentContext{}, "", nil); !errors.Is(err, wantErr) {
		t.Fatalf("router error=%v", err)
	}
	invalidType := AgentRouteCandidate{RouteType: "invalid", TargetKey: "target", Version: "v1"}
	if _, err := ValidateAgentRouteResult(AgentRouteResult{RouteType: invalidType.RouteType, TargetKey: invalidType.TargetKey, TargetVersion: invalidType.Version, IdempotencyKey: "key"}, []AgentRouteCandidate{{}, invalidType}); apperror.CodeOf(err) != "agent.router.route_type_denied" {
		t.Fatalf("route type=%v", err)
	}
	query := AgentRouteCandidate{RouteType: agentmodel.AgentRouteInteractiveQuery, TargetKey: "query", Version: "v1"}
	if _, err := ValidateAgentRouteResult(AgentRouteResult{RouteType: query.RouteType, TargetKey: query.TargetKey, TargetVersion: query.Version}, []AgentRouteCandidate{query}); err != nil {
		t.Fatalf("interactive idempotency=%v", err)
	}
	if _, err := ValidateAgentRouteResult(AgentRouteResult{RouteType: query.RouteType, TargetKey: query.TargetKey, TargetVersion: query.Version, Input: map[string]any{"bad": make(chan int)}}, []AgentRouteCandidate{query}); apperror.CodeOf(err) != "agent.router.input_invalid" {
		t.Fatalf("marshal input=%v", err)
	}
}
