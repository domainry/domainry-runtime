package transport

import (
	"context"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
	integration "github.com/domainry/domainry-integration-sdk"
	tools "github.com/domainry/domainry-tools-sdk"
)

type mcpAccountPortsStub struct{}

func (mcpAccountPortsStub) ListConnectionAccounts(context.Context, integration.ConnectionAccountSubject) ([]integration.ConnectionAccount, error) {
	return nil, nil
}
func (mcpAccountPortsStub) GetConnectionAccount(context.Context, integration.ConnectionAccountSubject, string) (integration.ConnectionAccount, error) {
	return integration.ConnectionAccount{}, nil
}
func (mcpAccountPortsStub) TestConnectionAccount(context.Context, integration.ConnectionAccountSubject, string, integration.ConnectionTestRequest) (integration.ConnectionAccountTestResult, error) {
	return integration.ConnectionAccountTestResult{}, nil
}
func (mcpAccountPortsStub) RevokeConnectionAccount(context.Context, integration.ConnectionAccountSubject, string, string) (integration.ConnectionAccount, error) {
	return integration.ConnectionAccount{}, nil
}
func (mcpAccountPortsStub) AuthorizeConnectionAccountRead(context.Context, integration.ConnectionAccountSubject, string, integration.ConnectionAccountReadOperation) (integration.ConnectionAccountReadAccess, error) {
	return integration.ConnectionAccountReadAccess{}, nil
}
func (mcpAccountPortsStub) ReadConnectionAccount(context.Context, integration.ConnectionAccountSubject, string, integration.ConnectionAccountReadRequest) (integration.ConnectionAccountReadResult, error) {
	return integration.ConnectionAccountReadResult{}, nil
}
func (mcpAccountPortsStub) AuthorizeConnectionAccountWrite(context.Context, integration.ConnectionAccountSubject, string, integration.ConnectionAccountWriteOperation) (integration.ConnectionAccountWriteAccess, error) {
	return integration.ConnectionAccountWriteAccess{}, nil
}
func (mcpAccountPortsStub) WriteConnectionAccount(context.Context, integration.ConnectionAccountSubject, string, integration.ConnectionAccountWriteRequest) (integration.ConnectionAccountWriteResult, error) {
	return integration.ConnectionAccountWriteResult{}, nil
}
func (mcpAccountPortsStub) ReadConnectionAccountWriteReceipt(context.Context, integration.ConnectionAccountSubject, string, integration.ConnectionAccountWriteRequest) (integration.ConnectionAccountWriteResult, error) {
	return integration.ConnectionAccountWriteResult{}, nil
}

func TestRuntimePublishesMCPToolsOnlyWithCompleteIntegrationPorts(t *testing.T) {
	count := func(definitions []agent.ConversationToolDefinition) int {
		keys := map[string]bool{}
		for _, definition := range tools.MCPDefinitions() {
			keys[definition.Key] = true
		}
		total := 0
		for _, definition := range definitions {
			if keys[definition.Key] {
				total++
			}
		}
		return total
	}
	if got := count((runtimeAgentApplicationHost{}).ConversationToolDefinitions()); got != 0 {
		t.Fatalf("MCP tools published without Integration ports: %d", got)
	}
	ports := mcpAccountPortsStub{}
	partial := runtimeAgentApplicationHost{accounts: ports, accountReads: ports, accountWrites: ports}
	if got := count(partial.ConversationToolDefinitions()); got != 0 {
		t.Fatalf("MCP tools published without a trusted subject resolver: %d", got)
	}
	complete := partial
	complete.accountSubject = func(context.Context, agent.ConversationAuthority, string) (integration.ConnectionAccountSubject, error) {
		return integration.ConnectionAccountSubject{}, nil
	}
	if got, want := count(complete.ConversationToolDefinitions()), len(tools.MCPDefinitions()); got != want {
		t.Fatalf("MCP tool count=%d want=%d", got, want)
	}
}
