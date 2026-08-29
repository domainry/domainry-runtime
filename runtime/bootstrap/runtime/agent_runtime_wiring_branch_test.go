package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type agentSchemaOwnerStub struct{ err error }

func (s agentSchemaOwnerStub) EnsureSchema(context.Context) error { return s.err }

func TestEnsureAgentRuntimeSchemasReportsEachOwnerFailure(t *testing.T) {
	wantErr := errors.New("schema failed")
	if err := ensureAgentRuntimeSchemas(t.Context(), agentSchemaOwnerStub{err: wantErr}, agentSchemaOwnerStub{}); !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "agent lifecycle") {
		t.Fatalf("err=%v", err)
	}
	if err := ensureAgentRuntimeSchemas(t.Context(), agentSchemaOwnerStub{}, agentSchemaOwnerStub{err: wantErr}); !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "agent task") {
		t.Fatalf("err=%v", err)
	}
	if err := ensureAgentRuntimeSchemas(t.Context(), agentSchemaOwnerStub{}, agentSchemaOwnerStub{}); err != nil {
		t.Fatalf("err=%v", err)
	}
}

func TestConfiguredAgentRunnersAndPollIntervalBranches(t *testing.T) {
	for _, cfg := range []config.Config{{}, {AgentHTTPAPIKey: "key"}, {AgentHTTPAgentID: 7}} {
		task, interactive := configuredAgentRunners(cfg)
		if task != nil || interactive != nil {
			t.Fatalf("unexpected runners for %+v", cfg)
		}
	}
	task, interactive := configuredAgentRunners(config.Config{AgentHTTPAPIKey: " key ", AgentHTTPAgentID: 7})
	if task == nil || interactive == nil {
		t.Fatalf("task=%#v interactive=%#v", task, interactive)
	}
	if agentTaskRecoveryInterval(0) != 30*time.Second || agentTaskRecoveryInterval(-time.Second) != 30*time.Second || agentTaskRecoveryInterval(2*time.Second) != 30*time.Second || agentTaskRecoveryInterval(time.Minute) != time.Minute {
		t.Fatal("recovery interval normalization failed")
	}
}

func TestAgentTaskWorkerLoopExecutesAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	done := startAgentTaskWorkerLoop(ctx, time.Nanosecond, nil, &agentapplication.AgentTaskWorker{})
	time.Sleep(time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("agent task worker loop did not stop")
	}
}

func TestRuntimeWorkerOwnerGuardsAndAgentStartCallback(t *testing.T) {
	var missing *Runtime
	missing.startAgentTaskWorker(t.Context())
	missing.StartWorkflowWorker(t.Context())
	missing.startConnectorProviderBackgroundWorker(t.Context())

	runtime := New(t.Context(), bootstrapTestConfig(t), runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory())
	t.Cleanup(func() { _ = runtime.store.Close() })
	original := runtimeWorkerApplications
	t.Cleanup(func() { runtimeWorkerApplications = original })
	runtimeWorkerApplications = func(*composition.RuntimeServices) composition.RuntimeApplications {
		return composition.RuntimeApplications{}
	}
	runtime.startAgentTaskWorker(t.Context())
	runtime.StartWorkflowWorker(t.Context())
	runtime.startConnectorProviderBackgroundWorker(t.Context())

	runtimeWorkerApplications = func(*composition.RuntimeServices) composition.RuntimeApplications {
		return composition.RuntimeApplications{AgentTaskWorker: &agentapplication.AgentTaskWorker{}}
	}
	ctx, cancel := context.WithCancel(t.Context())
	runtime.startAgentTaskWorker(ctx)
	time.Sleep(time.Millisecond)
	cancel()
	if err := runtime.stopWorkers(time.Second); err != nil {
		t.Fatalf("stop agent worker=%v", err)
	}
}
