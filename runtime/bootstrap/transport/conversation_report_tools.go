package transport

import (
	agent "github.com/domainry/domainry-agent-sdk"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

// Runtime bootstrap selects Tools' public adapter. The Runtime application
// exposes only its current authority and the Report owner's SDK DTO port.
func (h runtimeAgentApplicationHost) ConversationToolDefinitions() []agent.ConversationToolDefinition {
	if h.conversationToolsFactory == nil {
		return nil
	}
	return h.conversationToolsFactory.ConversationToolDefinitions(toolsdk.ConversationToolCapabilities{MCP: h.hasMCPConversationTools()})
}

func (h runtimeAgentApplicationHost) AssembleConversationTools(base agent.ConversationToolHost) (agent.ConversationToolHost, error) {
	input := toolsdk.ConversationToolAssembly{
		Base:           base,
		ReportSource:   func() toolsdk.ReportSource { return h.conversations },
		AnalysisSource: func() toolsdk.AnalysisSource { return h.conversations },
		Authorize:      h.conversations.AuthorizeConversationTool,
	}
	if h.hasMCPConversationTools() {
		input.MCP = &toolsdk.ConversationMCPPorts{
			Accounts: h.accounts,
			Reads:    h.accountReads,
			Writes:   h.accountWrites,
			Subject:  toolsdk.ConnectionAccountSubjectResolver(h.accountSubject),
		}
	}
	return h.conversationToolsFactory.AssembleConversationTools(input)
}

func (h runtimeAgentApplicationHost) hasMCPConversationTools() bool {
	return h.accounts != nil && h.accountReads != nil && h.accountWrites != nil && h.accountSubject != nil
}
