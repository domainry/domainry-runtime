package validation

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
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
		agentmodel.AgentTaskContractVersion,
		agentmodel.AgentEntrypointContractVersion,
		agentmodel.AgentServicePrincipalContractVersion,
		agentmodel.GlobalAgentContextContractVersion,
		agentmodel.AgentRoutingContractVersion,
		agentmodel.AgentTaskSideEffectAnalysisOnly,
		agentmodel.AgentTaskSideEffectProposalOnly,
		agentmodel.AgentTaskSideEffectActionAllowed,
		agentmodel.AgentTaskIdentityInherit,
		agentmodel.AgentTaskIdentityService,
		agentmodel.AgentRouteInteractiveQuery,
		agentmodel.AgentRouteTask,
		agentmodel.AgentRouteWorkflow,
		agentmodel.AgentRouteProposal,
	} {
		if !strings.Contains(document, value) {
			t.Errorf("Agent document drift: missing Runtime contract value %q", value)
		}
	}
	for _, outcome := range agentmodel.AgentTaskOutcomes {
		if !strings.Contains(document, outcome) {
			t.Errorf("Agent document drift: missing task outcome %q", outcome)
		}
	}
}
