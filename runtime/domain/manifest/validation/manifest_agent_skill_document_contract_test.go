package validation

import (
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuilderAgentCapabilityDocumentTracksRuntimeContract(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "..", "..", "..", "domainry-plane"))
	path := filepath.Join(repository, "skills", "domainry-builder-v1", "references", "capabilities", "agents-and-agent-tasks.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	document := string(content)
	for _, collection := range []string{"skills", "agents", "agent_tasks", "agent_entrypoints", "agent_service_principals"} {
		if !strings.Contains(document, "`"+collection+"`") {
			t.Errorf("Agent document omits Blueprint collection %s", collection)
		}
	}
	for _, value := range []string{
		agentsdk.AgentTaskContractVersion,
		agentsdk.AgentEntrypointContractVersion,
		agentsdk.AgentServicePrincipalContractVersion,
		agentsdk.GlobalAgentContextContractVersion,
		agentsdk.AgentRoutingContractVersion,
		agentsdk.AgentTaskSideEffectAnalysisOnly,
		agentsdk.AgentTaskSideEffectProposalOnly,
		agentsdk.AgentTaskSideEffectActionAllowed,
		agentsdk.AgentTaskIdentityInherit,
		agentsdk.AgentTaskIdentityService,
		agentsdk.AgentRouteInteractiveQuery,
		agentsdk.AgentRouteTask,
		agentsdk.AgentRouteWorkflow,
		agentsdk.AgentRouteProposal,
	} {
		if !strings.Contains(document, value) {
			t.Errorf("Agent document drift: missing Runtime contract value %q", value)
		}
	}
	for _, outcome := range agentsdk.AgentTaskOutcomes {
		if !strings.Contains(document, outcome) {
			t.Errorf("Agent document drift: missing task outcome %q", outcome)
		}
	}
}
