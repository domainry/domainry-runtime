package transport

import (
	"context"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
	integration "github.com/domainry/domainry-integration-sdk"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agenthost"
	tools "github.com/domainry/domainry-tools-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

type mcpAccountPortsStub struct{}

type conversationCapabilityFactoryProbe struct {
	capabilities tools.ConversationToolCapabilities
}

func (p *conversationCapabilityFactoryProbe) ConversationToolDefinitions(capabilities tools.ConversationToolCapabilities) []tools.Definition {
	p.capabilities = capabilities
	return nil
}

func (*conversationCapabilityFactoryProbe) AssembleConversationTools(input tools.ConversationToolAssembly) (tools.Host, error) {
	return input.Base, nil
}

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
func (mcpAccountPortsStub) RetryConnectionAccountBackgroundTask(context.Context, integration.ConnectionAccountSubject, string, string, integration.ConnectionAccountBackgroundRetryRequest) (integration.ConnectionAccountBackgroundTask, error) {
	return integration.ConnectionAccountBackgroundTask{}, nil
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
	factory := toolmodule.NewConversationToolFactory()
	if got := count((runtimeAgentApplicationHost{conversationToolsFactory: factory}).ConversationToolDefinitions()); got != 0 {
		t.Fatalf("MCP tools published without Integration ports: %d", got)
	}
	ports := mcpAccountPortsStub{}
	partial := runtimeAgentApplicationHost{accounts: ports, accountReads: ports, accountWrites: ports, conversationToolsFactory: factory}
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

func TestRuntimePublishesBusinessSourceCapabilityOnlyWhenAssembled(t *testing.T) {
	probe := &conversationCapabilityFactoryProbe{}
	(runtimeAgentApplicationHost{conversationToolsFactory: probe}).ConversationToolDefinitions()
	if probe.capabilities.Business {
		t.Fatal("business source capability published without a ConversationBusinessHost")
	}
	(runtimeAgentApplicationHost{conversationToolsFactory: probe, conversations: &agentapplication.ConversationBusinessHost{}}).ConversationToolDefinitions()
	if !probe.capabilities.Business {
		t.Fatal("assembled ConversationBusinessHost was not published to the product tool factory")
	}
}

func TestRuntimeConversationAuthorizerCarriesProductToolDefinitions(t *testing.T) {
	definition := tools.Definition{Key: "crm_search_accounts", Version: "1", ActionKey: "agent.conversation_tools.crm_search_accounts"}
	factory := &conversationDefinitionFactoryProbe{definitions: []tools.Definition{definition}}
	host := runtimeAgentApplicationHost{conversationToolsFactory: factory, conversations: &agentapplication.ConversationBusinessHost{}}
	authorizer, ok := host.ConversationAuthorizer().(runtimeConversationToolAuthorizer)
	if !ok || authorizer.ConversationBusinessHost != host.conversations || len(authorizer.definitions) != 1 || authorizer.definitions[0].Key != definition.Key {
		t.Fatalf("product authorizer=%#v", authorizer)
	}
	if _, ok := any(authorizer).(agent.ConversationExecutionAuthorizer); !ok {
		t.Fatal("product authorizer dropped current execution authorization")
	}
}

type conversationDefinitionFactoryProbe struct {
	definitions []tools.Definition
}

func (p *conversationDefinitionFactoryProbe) ConversationToolDefinitions(tools.ConversationToolCapabilities) []tools.Definition {
	return append([]tools.Definition(nil), p.definitions...)
}

func (*conversationDefinitionFactoryProbe) AssembleConversationTools(input tools.ConversationToolAssembly) (tools.Host, error) {
	return input.Base, nil
}
