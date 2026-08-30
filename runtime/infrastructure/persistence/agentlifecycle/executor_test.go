package agentlifecycle

import (
	"testing"

	lifecyclecontract "github.com/domainry/domainry-lifecycle/contract"
)

func TestExecutorOwnsAgentLifecyclePort(t *testing.T) {
	executor := NewExecutor(nil, nil)
	if executor.Owner(t.Context()) != "agent" {
		t.Fatalf("owner=%q", executor.Owner(t.Context()))
	}
	var _ lifecyclecontract.OwnerLifecycleExecutor = executor
}
