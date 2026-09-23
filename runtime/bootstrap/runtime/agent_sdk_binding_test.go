package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func openAgentBindingRuntimeStore(t *testing.T) *persistence.RuntimeStore {
	t.Helper()
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "agent-binding.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

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
	agentsdk.Binding
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
	binding := &agentSDKBindingStub{runner: runner, descriptor: agentsdk.Descriptor{ProtocolVersion: agentsdk.ProtocolVersionV1, Mode: agentsdk.DeploymentModeModule, Capabilities: []string{agentsdk.CapabilityTaskStart, agentsdk.CapabilityTaskPoll, agentsdk.CapabilityTaskCancel, agentsdk.CapabilityInteractiveRun, agentsdk.CapabilityLifecycleExecute}}}
	factory := &agentSDKModuleFactoryStub{binding: binding}
	store := openAgentBindingRuntimeStore(t)
	opened, err := openAgentBinding(t.Context(), "runtime", store, nil, nil, factory)
	if err != nil || opened != binding || factory.runtimeID != "runtime" {
		t.Fatalf("opened=%#v runtime=%q err=%v", opened, factory.runtimeID, err)
	}
	result, err := opened.TaskRunner().Start(t.Context(), agentsdk.TaskRequest{TaskRunID: "task", WorkspaceID: "workspace", Task: agentsdk.AgentTaskDefinition{Key: "review", Version: "1", Instruction: "review"}, IdempotencyKey: "key"})
	if err != nil || result.ExternalRunID != "provider" || runner.request.TaskRunID != "task" {
		t.Fatalf("result=%+v request=%+v err=%v", result, runner.request, err)
	}
}
func TestOpenAgentBindingFailsClosedAndClosesInvalidBinding(t *testing.T) {
	store := openAgentBindingRuntimeStore(t)
	if binding, err := openAgentBinding(t.Context(), "runtime", store, nil, nil, nil); err != nil || binding != nil {
		t.Fatalf("binding=%#v err=%v", binding, err)
	}
	invalid := &agentSDKBindingStub{runner: &agentSDKRunnerStub{}, descriptor: agentsdk.Descriptor{ProtocolVersion: "old", Mode: agentsdk.DeploymentModeModule}}
	if _, err := openAgentBinding(t.Context(), "runtime", store, nil, nil, &agentSDKModuleFactoryStub{binding: invalid}); err == nil || !invalid.closed {
		t.Fatalf("invalid descriptor err=%v closed=%v", err, invalid.closed)
	}
	if _, err := openAgentBinding(t.Context(), "runtime", store, nil, nil, &agentSDKModuleFactoryStub{}); err == nil {
		t.Fatal("nil Binding accepted")
	}
}

func TestOpenProjectAgentBindingSkipsUnusedAgentTopology(t *testing.T) {
	factory := &agentSDKModuleFactoryStub{err: errors.New("must not open")}
	binding, err := openProjectAgentBinding(t.Context(), "runtime", nil, nil, nil, factory, runtimeext.ProjectDefinitions{})
	if err != nil || binding != nil || factory.runtimeID != "" {
		t.Fatalf("binding=%#v runtime=%q err=%v", binding, factory.runtimeID, err)
	}
}

type conversationModuleFactoryStub struct {
	*agentSDKModuleFactoryStub
	enabled bool
}

func (f *conversationModuleFactoryStub) ConversationEnabled() bool { return f.enabled }

func TestOpenProjectAgentBindingSupportsConversationsWithoutDefinitions(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		runner := &agentSDKRunnerStub{}
		binding := &agentSDKBindingStub{runner: runner, descriptor: agentsdk.Descriptor{ProtocolVersion: agentsdk.ProtocolVersionV1, Mode: agentsdk.DeploymentModeModule, Capabilities: []string{agentsdk.CapabilityTaskStart, agentsdk.CapabilityTaskPoll, agentsdk.CapabilityTaskCancel, agentsdk.CapabilityInteractiveRun, agentsdk.CapabilityLifecycleExecute}}}
		factory := &conversationModuleFactoryStub{agentSDKModuleFactoryStub: &agentSDKModuleFactoryStub{binding: binding}, enabled: enabled}
		opened, err := openProjectAgentBinding(t.Context(), "runtime", openAgentBindingRuntimeStore(t), nil, nil, factory, runtimeext.ProjectDefinitions{})
		if err != nil || (opened != nil) != enabled || (factory.runtimeID != "") != enabled {
			t.Fatalf("enabled=%v opened=%v err=%v", enabled, opened, err)
		}
	}
}

func TestProjectDefinitionsUseAgentForEveryOwnedDefinitionCollection(t *testing.T) {
	cases := []struct {
		name        string
		definitions runtimeext.ProjectDefinitions
	}{
		{name: "skill", definitions: runtimeext.ProjectDefinitions{AgentSkills: []agentsdk.SkillSchema{{Key: "reader"}}}},
		{name: "agent", definitions: runtimeext.ProjectDefinitions{Agents: []agentsdk.AgentSchema{{Key: "assistant"}}}},
		{name: "task", definitions: runtimeext.ProjectDefinitions{AgentTasks: []agentsdk.AgentTaskDefinition{{Key: "review"}}}},
		{name: "entrypoint", definitions: runtimeext.ProjectDefinitions{AgentEntrypoints: []agentsdk.AgentEntrypointAssignment{{Key: "assistant.global"}}}},
		{name: "service principal", definitions: runtimeext.ProjectDefinitions{AgentServicePrincipals: []agentsdk.AgentServicePrincipalBinding{{Key: "assistant_service"}}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if !projectDefinitionsUseAgent(test.definitions) {
				t.Fatal("Agent-owned project definition did not require Agent Binding")
			}
		})
	}
}
