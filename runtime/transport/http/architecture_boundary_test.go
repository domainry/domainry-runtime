package http

// These tests enforce cross-package HTTP architecture boundaries.

import (
	"os"
	"reflect"
	"strings"
	"testing"

	agentrepository "github.com/domainry/domainry-agent-sdk/repository"
	agent "github.com/domainry/domainry-runtime/runtime/application/agent"
)

func TestHTTPRouterDoesNotRetainAgentStateRepository(t *testing.T) {
	repositoryType := reflect.TypeOf((*agentrepository.AgentStateRepository)(nil)).Elem()
	routerType := reflect.TypeOf(HTTPRouter{})
	for index := 0; index < routerType.NumField(); index++ {
		field := routerType.Field(index)
		if field.Type == repositoryType {
			t.Fatalf("HTTP Router field %s retains AgentStateRepository", field.Name)
		}
	}
}

func TestAgentProposalHandlersDoNotOrchestratePersistenceOrExecution(t *testing.T) {
	for _, name := range []string{"agentdialog/proposal_decision_handlers.go", "agentdialog/proposal_binding.go"} {
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{".Invoke(", "RunWorkflow(", "DecideProposal(", "SchemaForPrincipal("} {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("%s contains application orchestration %q", name, forbidden)
			}
		}
	}
}

func TestAgentStateApplicationServiceMethodBudget(t *testing.T) {
	typeOf := reflect.TypeOf((*agent.AgentApplicationService)(nil))
	if typeOf.NumMethod() > 15 {
		t.Fatalf("agent state application service has %d exported methods", typeOf.NumMethod())
	}
	if typeOf.Implements(reflect.TypeOf((*agentrepository.AgentStateRepository)(nil)).Elem()) {
		t.Fatal("agent application service must not masquerade as its repository contract")
	}
}
