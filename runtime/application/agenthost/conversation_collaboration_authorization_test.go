package agenthost

import (
	"context"
	"errors"
	"slices"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	principal "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	fixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

type collaborationPrincipalError struct{ err error }

func (r collaborationPrincipalError) Resolve(context.Context, identity.PrincipalResolutionRequest) (identity.PrincipalResolution, error) {
	return identity.PrincipalResolution{}, r.err
}

func TestConversationCollaborationIdentityDenialsRemainDenials(t *testing.T) {
	h, _, _, a := newConversationBusinessFixture(t)
	request := agent.ConversationCollaborationAuthorizationRequest{Authority: a, OwnerUserID: a.UserID, Operations: []string{"view"}}
	for _, status := range []int{401, 403, 404} {
		h.principals = collaborationPrincipalError{&identity.Error{StatusCode: status, Code: "identity.subject_not_found"}}
		decision, err := h.AuthorizeConversationCollaboration(t.Context(), request)
		if err != nil || len(decision.Allowed) != 0 {
			t.Fatal("Identity denial became unavailable", status, decision, err)
		}
		var coded *agent.Error
		if _, err := h.principal(t.Context(), a); !errors.As(err, &coded) || coded.Class != "forbidden" {
			t.Fatal("role/subject denial misclassified", status, err)
		}
	}
	h.principals = collaborationPrincipalError{&identity.Error{StatusCode: 503, Code: "identity.temporarily_unavailable"}}
	if _, err := h.AuthorizeConversationCollaboration(t.Context(), request); err == nil {
		t.Fatal("unavailable Identity treated as a policy decision")
	}
}

func TestConversationCollaborationUsesCurrentRoleAndActualDelegationOwner(t *testing.T) {
	h, resolver, records, a := newConversationBusinessFixture(t)
	grant := func(viewScope identity.DataScope) {
		resolver.mu.Lock()
		defer resolver.mu.Unlock()
		resolver.principal = fixture.Attach(principal.Principal{Principal: identity.Principal{Known: true, WorkspaceID: a.WorkspaceID, UserID: a.UserID, RoleKey: a.RoleKey}}, fixture.Bundle{Key: a.RoleKey, Permissions: []string{
			agent.ConversationCollaborationPermission("view").Key,
			agent.ConversationCollaborationPermission("execution_read").Key,
			agent.ConversationToolActionPrefix + "agent_delegate",
		}, DataPolicies: []fixture.DataPolicyFixture{
			{ObjectKey: "agent.collaboration", Action: "view", Scope: viewScope, Read: true},
			{ObjectKey: "agent.collaboration", Action: "execution_read", Scope: identity.DataScopeAll, Read: true},
			{ObjectKey: "agent.conversation_tools", Action: "agent_delegate", Scope: identity.DataScopeAll, Read: true},
		}})
	}
	grant(identity.DataScopeOwner)
	request := agent.ConversationCollaborationAuthorizationRequest{Authority: a, OwnerUserID: "other-owner", DelegationID: "delegation-1", FromAgentID: "from", ToAgentID: "to", Operations: []string{"view", "execution_read", "manage", "unknown"}}
	decision, err := h.AuthorizeConversationCollaboration(t.Context(), request)
	if err != nil || !slices.Equal(decision.Allowed, []string{"execution_read"}) {
		t.Fatal("foreign owner ignored", decision, err)
	}
	if resolver.request.SubjectID != identity.SubjectID(a.UserID) || resolver.request.RoleKey != a.RoleKey || resolver.workspace != a.WorkspaceID {
		t.Fatal("role or workspace not resolved", resolver.request, resolver.workspace)
	}
	request.OwnerUserID = a.UserID
	decision, err = h.AuthorizeConversationCollaboration(t.Context(), request)
	if err != nil || !slices.Equal(decision.Allowed, []string{"view", "execution_read"}) {
		t.Fatal("owner policy rejected own delegation", decision, err)
	}
	grant(identity.DataScopeAll)
	request.OwnerUserID = "other-owner"
	decision, err = h.AuthorizeConversationCollaboration(t.Context(), request)
	if err != nil || !slices.Contains(decision.Allowed, "view") {
		t.Fatal("current policy edit not observed", decision, err)
	}
	var definition agent.ConversationToolDefinition
	for _, d := range agent.ConversationCollaborationTools() {
		if d.Key == "agent_delegate" {
			definition = d
		}
	}
	authorization, err := h.AuthorizeConversationTool(t.Context(), agent.ConversationToolRequest{Authority: a, Definition: definition})
	if err != nil || !authorization.Granted {
		t.Fatal("registered collaboration tool not authorized", authorization, err)
	}
	definition.Version = "unregistered-version"
	if authorization, err := h.AuthorizeConversationTool(t.Context(), agent.ConversationToolRequest{Authority: a, Definition: definition}); err != nil || authorization.Granted {
		t.Fatal("unregistered tool version authorized", authorization, err)
	}
	if err := h.ValidateConversationAgentSubjects(t.Context(), a, []string{a.UserID}); err != nil {
		t.Fatal("actual subject rejected", err)
	}
	if err := h.ValidateConversationAgentSubjects(t.Context(), a, []string{"unrelated-user"}); err == nil {
		t.Fatal("mismatched resolved user accepted")
	}
	foreign := request
	foreign.Authority.WorkspaceID = "foreign"
	before := resolver.checks
	if decision, err := h.AuthorizeConversationCollaboration(t.Context(), foreign); err != nil || len(decision.Allowed) != 0 || resolver.checks != before {
		t.Fatal("foreign workspace reached Identity")
	}
	resolver.revoke()
	if decision, err := h.AuthorizeConversationCollaboration(t.Context(), request); err != nil || len(decision.Allowed) != 0 {
		t.Fatal("revoked role still authorized")
	}
	if err := h.ValidateConversationAgentSubjects(t.Context(), a, []string{a.UserID}); err == nil {
		t.Fatal("inactive subject allowed shared use")
	}
	if records.queries != 0 {
		t.Fatal("collaboration policy read business records")
	}
}
