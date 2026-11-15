package agenthost

import (
	"context"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// AgentToolKnowledgePort is a read boundary, independent of project Object
// grants. The adapter must authorize the live library/document and revalidate
// its result before releasing a normalized projection to the Task.
type AgentToolKnowledgePort interface {
	SearchAgentKnowledge(context.Context, string, string, principalmodel.Principal) (AgentToolKnowledgeResult, error)
	ReadAgentKnowledge(context.Context, string, string, principalmodel.Principal) (AgentToolKnowledgeResult, error)
}

type AgentToolKnowledgeResult struct {
	Output    any
	Knowledge *agentmodel.AgentTaskKnowledgeEvidence
}

func agentKnowledgeToolInput(tool string, input map[string]any) (string, string, error) {
	field, limit := "query", 4096
	if tool == agentsdk.AgentToolKnowledgeRead {
		field, limit = "document_id", 255
	}
	invalid := func() (string, string, error) {
		return "", "", apperror.New(apperror.KindBadRequest, "agent.tool.knowledge_input_invalid", nil, nil)
	}
	if len(input) != 2 {
		return invalid()
	}
	library, libraryOK := input["library_id"].(string)
	value, valueOK := input[field].(string)
	library, value = strings.TrimSpace(library), strings.TrimSpace(value)
	if !libraryOK || !valueOK || library == "" || len(library) > 96 || value == "" || len([]rune(value)) > limit {
		return invalid()
	}
	return library, value, nil
}
