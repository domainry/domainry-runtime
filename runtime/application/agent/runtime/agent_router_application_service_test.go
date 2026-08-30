package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/apperror"
)

type agentRouterFunc func(context.Context, agentsdk.GlobalContext, string, []agentsdk.RouteCandidate) (agentsdk.RouteResult, error)

func (fn agentRouterFunc) Route(ctx context.Context, runtimeContext agentsdk.GlobalContext, message string, candidates []agentsdk.RouteCandidate) (agentsdk.RouteResult, error) {
	return fn(ctx, runtimeContext, message, candidates)
}

func TestAgentRouterAcceptsOnlyRuntimeFilteredPublishedTargets(t *testing.T) {
	candidates := []agentsdk.RouteCandidate{{RouteType: agentsdk.AgentRouteTask, TargetKey: "resume.screen", Version: "v1"}}
	service := NewAgentRouterApplicationService(agentRouterFunc(func(_ context.Context, _ agentsdk.GlobalContext, message string, got []agentsdk.RouteCandidate) (agentsdk.RouteResult, error) {
		if message != "screen" || len(got) != 1 {
			t.Fatalf("message=%q candidates=%v", message, got)
		}
		return agentsdk.RouteResult{RouteType: agentsdk.AgentRouteTask, TargetKey: "resume.screen", TargetVersion: "v1", Input: map[string]any{"candidate_id": "1"}, IdempotencyKey: "handoff-1"}, nil
	}))
	result, err := service.Route(t.Context(), agentsdk.GlobalContext{}, " screen ", candidates)
	if err != nil || result.TargetKey != "resume.screen" {
		t.Fatalf("result=%#v err=%v", result, err)
	}

	for name, result := range map[string]agentsdk.RouteResult{
		"unknown target":  {RouteType: agentsdk.AgentRouteTask, TargetKey: "forged", TargetVersion: "v1", IdempotencyKey: "key"},
		"unknown version": {RouteType: agentsdk.AgentRouteTask, TargetKey: "resume.screen", TargetVersion: "v2", IdempotencyKey: "key"},
		"arbitrary url":   {RouteType: "https://evil.invalid", TargetKey: "resume.screen", TargetVersion: "v1", IdempotencyKey: "key"},
	} {
		t.Run(name, func(t *testing.T) {
			denied := NewAgentRouterApplicationService(agentRouterFunc(func(context.Context, agentsdk.GlobalContext, string, []agentsdk.RouteCandidate) (agentsdk.RouteResult, error) {
				return result, nil
			}))
			if _, err := denied.Route(t.Context(), agentsdk.GlobalContext{}, "", candidates); err == nil {
				t.Fatal("forged route accepted")
			}
		})
	}
}

func TestAgentRouterRequiresDurableIdempotencyAndBoundsInput(t *testing.T) {
	candidate := agentsdk.RouteCandidate{RouteType: agentsdk.AgentRouteWorkflow, TargetKey: "flow", Version: "v1"}
	result := agentsdk.RouteResult{RouteType: candidate.RouteType, TargetKey: candidate.TargetKey, TargetVersion: candidate.Version}
	service := NewAgentRouterApplicationService(agentRouterFunc(func(context.Context, agentsdk.GlobalContext, string, []agentsdk.RouteCandidate) (agentsdk.RouteResult, error) {
		return result, nil
	}))
	if _, err := service.Route(t.Context(), agentsdk.GlobalContext{}, "", []agentsdk.RouteCandidate{candidate}); apperror.CodeOf(err) != "agent.router.idempotency_required" {
		t.Fatalf("idempotency err=%v", err)
	}
	result.IdempotencyKey, result.Input = "key", map[string]any{"large": strings.Repeat("x", 65*1024)}
	if _, err := service.Route(t.Context(), agentsdk.GlobalContext{}, "", []agentsdk.RouteCandidate{candidate}); apperror.CodeOf(err) != "agent.router.input_invalid" {
		t.Fatalf("input err=%v", err)
	}
	if _, err := (*AgentRouterApplicationService)(nil).Route(t.Context(), agentsdk.GlobalContext{}, "", nil); apperror.CodeOf(err) != "agent.router.unavailable" {
		t.Fatalf("nil router err=%v", err)
	}
	if _, err := NewAgentRouterApplicationService(nil).Route(t.Context(), agentsdk.GlobalContext{}, "", nil); apperror.CodeOf(err) != "agent.router.unavailable" {
		t.Fatalf("missing router err=%v", err)
	}
	wantErr := errors.New("router failed")
	if _, err := NewAgentRouterApplicationService(agentRouterFunc(func(context.Context, agentsdk.GlobalContext, string, []agentsdk.RouteCandidate) (agentsdk.RouteResult, error) {
		return agentsdk.RouteResult{}, wantErr
	})).Route(t.Context(), agentsdk.GlobalContext{}, "", nil); !errors.Is(err, wantErr) {
		t.Fatalf("router error=%v", err)
	}
	invalidType := agentsdk.RouteCandidate{RouteType: "invalid", TargetKey: "target", Version: "v1"}
	if _, err := ValidateAgentRouteResult(agentsdk.RouteResult{RouteType: invalidType.RouteType, TargetKey: invalidType.TargetKey, TargetVersion: invalidType.Version, IdempotencyKey: "key"}, []agentsdk.RouteCandidate{{}, invalidType}); apperror.CodeOf(err) != "agent.router.route_type_denied" {
		t.Fatalf("route type=%v", err)
	}
	query := agentsdk.RouteCandidate{RouteType: agentsdk.AgentRouteInteractiveQuery, TargetKey: "query", Version: "v1"}
	if _, err := ValidateAgentRouteResult(agentsdk.RouteResult{RouteType: query.RouteType, TargetKey: query.TargetKey, TargetVersion: query.Version}, []agentsdk.RouteCandidate{query}); err != nil {
		t.Fatalf("interactive idempotency=%v", err)
	}
	if _, err := ValidateAgentRouteResult(agentsdk.RouteResult{RouteType: query.RouteType, TargetKey: query.TargetKey, TargetVersion: query.Version, Input: map[string]any{"bad": make(chan int)}}, []agentsdk.RouteCandidate{query}); apperror.CodeOf(err) != "agent.router.input_invalid" {
		t.Fatalf("marshal input=%v", err)
	}
}
