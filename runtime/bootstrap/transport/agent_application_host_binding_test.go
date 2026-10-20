package transport

import (
	"fmt"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
)

type unboundKnowledgeAgentFixture struct {
	agentsdk.Binding
	manifestReads int
}

func (*unboundKnowledgeAgentFixture) BindApplicationHost(modulehost.ApplicationHost) error {
	return nil
}

func (*unboundKnowledgeAgentFixture) HTTPAdapters() []modulehttp.Adapter { return nil }

func (binding *unboundKnowledgeAgentFixture) AuthorizationActions() ([]actioncontract.ActionDefinition, error) {
	binding.manifestReads++
	return nil, fmt.Errorf("Knowledge service HTTP adapter is not bound")
}

func TestBindAgentApplicationHostDoesNotProbeUnboundKnowledgeManifest(t *testing.T) {
	binding := &unboundKnowledgeAgentFixture{}
	err := BindAgentApplicationHost(AgentApplicationHostDependencies{Binding: binding})
	if err == nil || !strings.Contains(err.Error(), "host dependencies are incomplete") {
		t.Fatalf("expected missing host dependencies before binding, got %v", err)
	}
	if binding.manifestReads != 0 {
		t.Fatalf("read unbound module manifest %d times", binding.manifestReads)
	}
}

func TestValidateAgentAuthorizationProjectionStillRejectsUnboundKnowledge(t *testing.T) {
	binding := &unboundKnowledgeAgentFixture{}
	err := validateAgentAuthorizationProjection(binding)
	if err == nil || !strings.Contains(err.Error(), "Knowledge service HTTP adapter is not bound") {
		t.Fatalf("incomplete authorization projection was accepted: %v", err)
	}
	if binding.manifestReads != 1 {
		t.Fatalf("expected one final manifest validation, got %d", binding.manifestReads)
	}
}
