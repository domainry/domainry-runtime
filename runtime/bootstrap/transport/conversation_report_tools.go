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
	return h.conversationToolsFactory.ConversationToolDefinitions(toolsdk.ConversationToolCapabilities{
		Business:     h.conversations != nil,
		AccountReads: h.hasConversationAccountReads(),
		MCP:          h.hasMCPConversationTools(),
	})
}

func (h runtimeAgentApplicationHost) AssembleConversationTools(base agent.ConversationToolHost) (agent.ConversationToolHost, error) {
	authorizer := h.conversationToolAuthorizer()
	input := toolsdk.ConversationToolAssembly{
		Base: base,
		BusinessSource: func() toolsdk.ConversationBusinessSource {
			return conversationToolsBusinessSource{source: h.conversations}
		},
		ReportSource:   func() toolsdk.ReportSource { return h.conversations },
		AnalysisSource: func() toolsdk.AnalysisSource { return h.conversations },
		Authorize:      authorizer.AuthorizeConversationTool,
	}
	if h.hasConversationAccountReads() {
		input.AccountReads = &toolsdk.ConversationAccountReadPorts{
			Accounts: h.accounts,
			Reads:    h.accountReads,
			Subject:  toolsdk.ConnectionAccountSubjectResolver(h.accountSubject),
		}
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

func (h runtimeAgentApplicationHost) hasConversationAccountReads() bool {
	return h.accounts != nil && h.accountReads != nil && h.accountSubject != nil
}

func (h runtimeAgentApplicationHost) hasMCPConversationTools() bool {
	return h.hasConversationAccountReads() && h.accountWrites != nil
}
