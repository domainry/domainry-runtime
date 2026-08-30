package agent

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type workspaceGuardAgentRepository struct{ calls int }

func (repository *workspaceGuardAgentRepository) List(context.Context, string, string, string, string) ([]agentmodel.AgentStateRecord, error) {
	repository.calls++
	return nil, nil
}
func (repository *workspaceGuardAgentRepository) Get(context.Context, string, string, string) (agentmodel.AgentStateRecord, bool, error) {
	repository.calls++
	return agentmodel.AgentStateRecord{}, false, nil
}
func (repository *workspaceGuardAgentRepository) Put(context.Context, string, agentmodel.AgentStateRecord) error {
	repository.calls++
	return nil
}
func (repository *workspaceGuardAgentRepository) PutBatch(context.Context, string, []agentmodel.AgentStateRecord) error {
	repository.calls++
	return nil
}
func (repository *workspaceGuardAgentRepository) CompareAndSwap(context.Context, string, agentmodel.AgentStateRecord, int64) (bool, error) {
	repository.calls++
	return true, nil
}

func TestAgentApplicationAuthorizesWorkspaceBeforeRepositoryAccess(t *testing.T) {
	repository := &workspaceGuardAgentRepository{}
	service := NewAgentApplicationService(repository)
	missing := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user"}}

	checks := []func() error{
		func() error { _, err := service.ListSessions(t.Context(), AgentSessionQuery{}, missing); return err },
		func() error {
			_, err := service.UpsertSession(t.Context(), AgentSessionUpsertRequest{}, missing)
			return err
		},
		func() error { _, err := service.GetProposal(t.Context(), "proposal", missing); return err },
		func() error {
			_, err := service.StoreProposal(t.Context(), AgentProposal{ProposalID: "proposal"})
			return err
		},
		func() error {
			_, err := service.RecordReportGovernance(t.Context(), AgentReportGovernanceRequest{QueryRef: "query"}, missing)
			return err
		},
	}
	for _, check := range checks {
		if err := check(); err == nil || apperror.KindOf(err) != apperror.KindForbidden {
			t.Fatalf("missing workspace was not rejected as forbidden: %v", err)
		}
	}
	if repository.calls != 0 {
		t.Fatalf("repository was called %d times before workspace authorization", repository.calls)
	}
}
