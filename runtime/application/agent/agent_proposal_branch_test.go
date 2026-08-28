package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type agentProposalCASMissRepository struct{ *agentRepositoryStub }

type agentProposalCASCountRepository struct {
	*agentRepositoryStub
	calls  int
	failAt int
}

func (r *agentProposalCASCountRepository) CompareAndSwap(_ context.Context, _ string, value agentmodel.AgentStateRecord, _ int64) (bool, error) {
	r.calls++
	if r.calls == r.failAt {
		return false, errAgentRepository
	}
	r.puts = append(r.puts, value)
	return true, nil
}

func (agentProposalCASMissRepository) CompareAndSwap(context.Context, string, agentmodel.AgentStateRecord, int64) (bool, error) {
	return false, nil
}

func TestAgentProposalRemainingPersistenceBranches(t *testing.T) {
	principal := agentTestPrincipal()
	repository := &agentRepositoryStub{getValues: map[string]agentmodel.AgentStateRecord{}, getFound: map[string]bool{}, getErr: errAgentRepository}
	service := NewAgentApplicationService(repository)
	if _, err := service.DecideProposal(t.Context(), "missing", "approved", "reason", nil, nil, principal); !errors.Is(err, errAgentRepository) {
		t.Fatalf("decision get err=%v", err)
	}
	record := AgentProposal{ProposalID: "proposal", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey, Metadata: map[string]any{"bad": make(chan int)}}
	if _, err := service.storeProposalCAS(t.Context(), record, 1); err == nil {
		t.Fatal("unencodable CAS proposal accepted")
	}
	record.Metadata = nil
	repository.getErr = nil
	repository.putErr = errAgentRepository
	if _, err := service.storeProposalCAS(t.Context(), record, 1); !errors.Is(err, errAgentRepository) {
		t.Fatalf("CAS repository err=%v", err)
	}
	miss := NewAgentApplicationService(agentProposalCASMissRepository{agentRepositoryStub: &agentRepositoryStub{}})
	if _, err := miss.storeProposalCAS(t.Context(), record, 1); apperror.CodeOf(err) != "agent_dialog.proposal_decision_conflict" {
		t.Fatalf("CAS miss err=%v", err)
	}
	memory := NewAgentMemoryStateRepository()
	stored, err := NewAgentApplicationService(memory).StoreProposal(t.Context(), AgentProposal{ProposalID: "created", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey, CreatedAt: 1})
	if err != nil || stored.CreatedAt != 1 {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
}

func TestAgentProposalMissingApprovalResolverBranch(t *testing.T) {
	service := NewAgentProposalApplicationService(nil, AgentProposalDependencies{})
	if _, err := service.resolveApprovalPrincipal(t.Context(), AgentProposal{}, principalmodel.Principal{}); apperror.CodeOf(err) != "agent.authorization.resolver_unavailable" {
		t.Fatalf("err=%v", err)
	}
	if _, err := (*AgentProposalApplicationService)(nil).resolveApprovalPrincipal(t.Context(), AgentProposal{}, principalmodel.Principal{}); apperror.CodeOf(err) != "agent.authorization.resolver_unavailable" {
		t.Fatalf("nil err=%v", err)
	}
	wantErr := errors.New("resolver failure")
	failed := NewAgentProposalApplicationService(nil, AgentProposalDependencies{ResolvePrincipalRole: func(context.Context, string, string) (principalmodel.Principal, error) {
		return principalmodel.Principal{}, wantErr
	}})
	if _, err := failed.resolveApprovalPrincipal(t.Context(), AgentProposal{}, principalmodel.Principal{}); !errors.Is(err, wantErr) {
		t.Fatalf("resolver err=%v", err)
	}
	if err := (*AgentProposalApplicationService)(nil).resolveLifecycle(t.Context(), AgentProposal{}, principalmodel.Principal{}); err != nil {
		t.Fatalf("nil lifecycle err=%v", err)
	}
	baseProposal := AgentProposal{WorkspaceID: "workspace", UserID: "user", Role: "role"}
	for name, resolved := range map[string]principalmodel.Principal{
		"workspace": accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "other", UserID: "user"}}, accessfixture.Bundle{Key: "role"}),
		"user":      accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "other"}}, accessfixture.Bundle{Key: "role"}),
		"role":      accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "user"}}, accessfixture.Bundle{Key: "other"}),
	} {
		t.Run(name, func(t *testing.T) {
			service := NewAgentProposalApplicationService(nil, AgentProposalDependencies{ResolvePrincipalRole: func(context.Context, string, string) (principalmodel.Principal, error) { return resolved, nil }})
			if _, err := service.resolveApprovalPrincipal(t.Context(), baseProposal, principalmodel.Principal{}); apperror.CodeOf(err) != "agent.authorization.approval_principal_revoked" {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestAgentProposalDecisionCASFailureStages(t *testing.T) {
	principal := agentTestPrincipal()
	proposal := AgentProposal{ProposalID: "proposal", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey, Status: "draft", UpdatedAt: 1}
	payload, err := json.Marshal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	newService := func(failAt int) *AgentProposalApplicationService {
		repository := &agentProposalCASCountRepository{agentRepositoryStub: &agentRepositoryStub{
			getValues: map[string]agentmodel.AgentStateRecord{"proposal": {Payload: payload}},
			getFound:  map[string]bool{"proposal": true},
		}, failAt: failAt}
		state := NewAgentApplicationService(repository)
		return NewAgentProposalApplicationService(state, AgentProposalDependencies{ResolvePrincipalRole: func(context.Context, string, string) (principalmodel.Principal, error) { return principal, nil }})
	}
	if _, err := newService(1).Decide(t.Context(), proposal.ProposalID, "rejected", "reason", nil, principal); !errors.Is(err, errAgentRepository) {
		t.Fatalf("first CAS err=%v", err)
	}
	if _, err := newService(2).Decide(t.Context(), proposal.ProposalID, "approved", "reason", nil, principal); !errors.Is(err, errAgentRepository) {
		t.Fatalf("second CAS err=%v", err)
	}
}
