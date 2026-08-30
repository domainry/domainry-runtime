package runtime

import (
	"context"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// AgentRouter is a Runtime orchestration port. Its request and result contracts
// are owned by the Agent SDK; Runtime only supplies authorized candidates.
type AgentRouter interface {
	Route(context.Context, agentsdk.GlobalContext, string, []agentsdk.RouteCandidate) (agentsdk.RouteResult, error)
}
