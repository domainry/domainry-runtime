package transport

import (
	"context"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent"
	agentruntime "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type transportAgentPrincipalStub struct{}

func (transportAgentPrincipalStub) Resolve(_ context.Context, request identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	bundle := identitysdk.AccessBundle{
		ContractVersion: identitysdk.CurrentPolicyBundleVersion,
		Subject: identitysdk.Subject{
			SubjectID:   request.SubjectID,
			WorkspaceID: request.Application.WorkspaceID,
		},
	}
	principal := identitysdk.Principal{
		Known:        true,
		UserID:       string(request.SubjectID),
		WorkspaceID:  string(request.Application.WorkspaceID),
		RoleKey:      request.RoleKey,
		AccessBundle: &bundle,
	}
	return identitysdk.PrincipalResolution{Principal: principal, AccessBundle: bundle}, nil
}

func TestAssembleAgentApplicationPortsRequiresPersistentStore(t *testing.T) {
	state, proposals := assembleAgentApplicationPorts(HTTPServerDependencies{})
	if state != nil || proposals != nil {
		t.Fatalf("state=%#v proposals=%#v", state, proposals)
	}
}

func TestAgentProposalApplicationCallbacksDelegateToRuntimeOwners(t *testing.T) {
	if _, err := resolveAgentPrincipalRole(t.Context(), nil, "user", "role"); apperror.CodeOf(err) != "agent.authorization.resolver_unavailable" {
		t.Fatalf("err=%v", err)
	}
	_, _ = resolveAgentPrincipalRole(t.Context(), transportAgentPrincipalStub{}, "user", "role")
	services := composition.NewRuntimeServices(t.Context(), composition.RuntimeServicesConfig{})
	applications := services.Applications()
	_, _ = invokeAgentProposalAction(t.Context(), applications, agentapplication.AgentActionInvocation{ActionKey: "missing"})
	_, _ = runAgentProposalWorkflow(t.Context(), applications.Workflows, "missing", nil, principalmodel.Principal{})
	_, _ = agentProposalActionInvoker(applications)(t.Context(), agentapplication.AgentActionInvocation{ActionKey: "missing"})
	_, _ = agentProposalWorkflowRunner(applications.Workflows)(t.Context(), "missing", nil, principalmodel.Principal{})
}

func TestAgentProposalLifecycleResolverHandlesMissingTaskContextAndOwner(t *testing.T) {
	resolve := agentProposalLifecycleResolver(nil)
	if err := resolve(t.Context(), agentapplication.AgentProposal{}, principalmodel.Principal{}); err != nil {
		t.Fatalf("empty task context err=%v", err)
	}
	if err := resolve(t.Context(), agentapplication.AgentProposal{Metadata: map[string]any{"task_run_id": ""}}, principalmodel.Principal{}); err != nil {
		t.Fatalf("blank task context err=%v", err)
	}
	err := resolve(t.Context(), agentapplication.AgentProposal{Metadata: map[string]any{"task_run_id": "task-1"}}, principalmodel.Principal{})
	if apperror.CodeOf(err) != "agent.task.approval_lifecycle_unavailable" {
		t.Fatalf("err=%v", err)
	}
	service := agentruntime.NewAgentTaskRunApplicationServiceWithAudit(nil, nil, nil, nil)
	resolve = agentProposalLifecycleResolver(service)
	if err := resolve(t.Context(), agentapplication.AgentProposal{WorkspaceID: "workspace", Metadata: map[string]any{"task_run_id": "task-1"}}, principalmodel.Principal{}); apperror.CodeOf(err) != "agent.task.repository_unavailable" {
		t.Fatalf("owner err=%v", err)
	}
}

func TestAgentGuardedWriteContractsProjectAllFields(t *testing.T) {
	contracts := agentGuardedWriteContracts([]appschemamodel.ApplicationSchemaGuardedWriteContract{{ObjectKey: "customer", Operation: "update", ActionKey: "update_customer", Endpoint: "/actions/update", RequiresRecord: true}})
	if len(contracts) != 1 || contracts[0].ObjectKey != "customer" || contracts[0].Operation != "update" || contracts[0].ActionKey != "update_customer" || contracts[0].Endpoint != "/actions/update" || !contracts[0].RequiresRecord {
		t.Fatalf("contracts=%+v", contracts)
	}
}
