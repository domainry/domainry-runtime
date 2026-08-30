package runtime

import (
	"context"
	"errors"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
)

type agentSDKRunnerStub struct{ request agentsdk.TaskRequest }

func (s *agentSDKRunnerStub) Start(_ context.Context, request agentsdk.TaskRequest) (agentsdk.TaskResult, error) {
	s.request = request
	return agentsdk.TaskResult{ExternalRunID: "provider", Status: agentsdk.ProviderRunAccepted}, nil
}
func (*agentSDKRunnerStub) Poll(context.Context, string, string) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{}, nil
}
func (*agentSDKRunnerStub) Cancel(context.Context, string, string) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{}, nil
}
func (*agentSDKRunnerStub) Run(context.Context, agentsdk.InteractiveRequest) (agentsdk.InteractiveResult, error) {
	return agentsdk.InteractiveResult{Status: "completed"}, nil
}

type agentSDKBindingStub struct {
	runner     *agentSDKRunnerStub
	descriptor agentsdk.Descriptor
	closed     bool
}

func (b *agentSDKBindingStub) Descriptor() agentsdk.Descriptor               { return b.descriptor }
func (b *agentSDKBindingStub) TaskRunner() agentsdk.TaskRunner               { return b.runner }
func (b *agentSDKBindingStub) InteractiveRunner() agentsdk.InteractiveRunner { return b.runner }
func (b *agentSDKBindingStub) Close(context.Context) error                   { b.closed = true; return nil }

type agentSDKModuleFactoryStub struct {
	binding   agentsdk.Binding
	err       error
	runtimeID string
}

func (f *agentSDKModuleFactoryStub) Open(context.Context, agentsdk.ApplicationRef) (agentsdk.Binding, error) {
	return nil, errors.New("generic Open used")
}
func (f *agentSDKModuleFactoryStub) OpenModule(_ context.Context, application agentsdk.ApplicationRef, host modulehost.Host) (agentsdk.Binding, error) {
	f.runtimeID = host.RuntimeID()
	if application.RuntimeID != host.RuntimeID() {
		return nil, errors.New("identity mismatch")
	}
	return f.binding, f.err
}

func TestOpenAgentBindingUsesModuleFactoryAndValidatesDescriptor(t *testing.T) {
	runner := &agentSDKRunnerStub{}
	binding := &agentSDKBindingStub{runner: runner, descriptor: agentsdk.Descriptor{ProtocolVersion: agentsdk.ProtocolVersionV1, Mode: agentsdk.DeploymentModeModule, Capabilities: []string{"task.start", "task.poll", "task.cancel", "interactive.run"}}}
	factory := &agentSDKModuleFactoryStub{binding: binding}
	opened, err := openAgentBinding(t.Context(), "runtime", factory)
	if err != nil || opened != binding || factory.runtimeID != "runtime" {
		t.Fatalf("opened=%#v runtime=%q err=%v", opened, factory.runtimeID, err)
	}
	adapter := runtimeAgentTaskRunner{runner: opened.TaskRunner()}
	result, err := adapter.Start(t.Context(), agentapplication.AgentTaskRunnerRequest{TaskRunID: "task", WorkspaceID: "workspace", Task: agentmodel.AgentTaskDefinition{Key: "review", Version: "1", Instruction: "review"}, IdempotencyKey: "key"})
	if err != nil || result.ExternalRunID != "provider" || runner.request.TaskRunID != "task" {
		t.Fatalf("result=%+v request=%+v err=%v", result, runner.request, err)
	}
}
func TestOpenAgentBindingFailsClosedAndClosesInvalidBinding(t *testing.T) {
	if binding, err := openAgentBinding(t.Context(), "runtime", nil); err != nil || binding != nil {
		t.Fatalf("binding=%#v err=%v", binding, err)
	}
	invalid := &agentSDKBindingStub{runner: &agentSDKRunnerStub{}, descriptor: agentsdk.Descriptor{ProtocolVersion: "old", Mode: agentsdk.DeploymentModeModule}}
	if _, err := openAgentBinding(t.Context(), "runtime", &agentSDKModuleFactoryStub{binding: invalid}); err == nil || !invalid.closed {
		t.Fatalf("invalid descriptor err=%v closed=%v", err, invalid.closed)
	}
	if _, err := openAgentBinding(t.Context(), "runtime", &agentSDKModuleFactoryStub{}); err == nil {
		t.Fatal("nil Binding accepted")
	}
}
