// Agent-proposal domain service tests.
package agent

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestAgentProposalApplicationOwnsDecisionAndVisibility(t *testing.T) {
	application := NewAgentApplicationService(NewAgentMemoryStateRepository())
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}, accessfixture.Bundle{Key: "admin"})
	created, err := application.StoreProposal(t.Context(), AgentProposal{ProposalID: "proposal-1", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey, Title: "Update customer"})
	if err != nil || created.Status != "draft" || !created.Audited {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	approved, err := application.DecideProposal(t.Context(), created.ProposalID, "approved", "reviewed", map[string]any{"object_key": "customer"}, map[string]any{"status": "succeeded"}, principal)
	if err != nil || approved.Status != "approved" || approved.DecisionActor != principal.UserID || approved.DecidedAt == 0 {
		t.Fatalf("approved=%#v err=%v", approved, err)
	}
	listed, err := application.ListProposals(t.Context(), "approved", principal)
	if err != nil || len(listed) != 1 {
		t.Fatalf("listed=%#v err=%v", listed, err)
	}
	other := principal
	other.UserID = "user-b"
	if _, err := application.GetProposal(t.Context(), created.ProposalID, other); errorKind(err) != apperror.KindNotFound {
		t.Fatalf("cross-user proposal err=%v", err)
	}
}
