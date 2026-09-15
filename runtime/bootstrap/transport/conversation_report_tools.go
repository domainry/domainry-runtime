package transport

import (
	agent "github.com/domainry/domainry-agent-sdk"
	toolsdk "github.com/domainry/domainry-tools-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

// Runtime bootstrap selects Tools' public adapter. The Runtime application
// exposes only its current authority and the Report owner's SDK DTO port.
func (h runtimeAgentApplicationHost) ConversationToolDefinitions() []agent.ConversationToolDefinition {
	definitions := append(toolmodule.ReportDefinitions(), toolmodule.AnalysisDefinitions()...)
	if h.accounts != nil && h.accountReads != nil && h.accountWrites != nil && h.accountSubject != nil {
		definitions = append(definitions, toolmodule.MCPDefinitions()...)
	}
	return definitions
}

func (h runtimeAgentApplicationHost) AssembleConversationTools(base agent.ConversationToolHost) (agent.ConversationToolHost, error) {
	adapter := &toolmodule.ReportAdapter{Source: func() toolmodule.ReportSource { return h.conversations }, Authorize: h.conversations.AuthorizeConversationTool}
	registry := toolmodule.NewRegistry()
	if err := adapter.Register(registry); err != nil {
		return nil, err
	}
	analysis := &toolmodule.AnalysisAdapter{Source: func() toolmodule.AnalysisSource { return h.conversations }, Authorize: h.conversations.AuthorizeConversationTool}
	if err := analysis.Register(registry); err != nil {
		return nil, err
	}
	if h.accounts != nil && h.accountReads != nil && h.accountWrites != nil && h.accountSubject != nil {
		confirmation, ok := base.(toolsdk.ConfirmationVerifier)
		if !ok || confirmation == nil {
			return nil, &toolsdk.Error{Class: "unavailable", Code: "mcp.confirmation_verifier_unavailable"}
		}
		mcp := &toolmodule.MCPAdapter{
			Accounts: h.accounts, Reads: h.accountReads, Writes: h.accountWrites,
			Subject: h.accountSubject, Authorize: h.conversations.AuthorizeConversationTool, Confirmation: confirmation,
		}
		if err := mcp.Register(registry); err != nil {
			return nil, err
		}
	}
	keys := []string{}
	for _, d := range h.ConversationToolDefinitions() {
		keys = append(keys, d.Key)
	}
	selected, err := registry.Select(keys)
	if err != nil {
		return nil, err
	}
	return toolmodule.Combine(base, selected, keys)
}
