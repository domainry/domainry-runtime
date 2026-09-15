package agenthost

import (
	"context"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-identity-sdk/authorization/evaluator"
)

// Library membership and document state remain Knowledge-owned. Runtime
// evaluates their loaded facts against the exact actor's current Identity.
func (h *ConversationBusinessHost) AuthorizeKnowledgeLibrary(ctx context.Context, operation string, library agentsdk.KnowledgeLibrary, authority agentsdk.ConversationAuthority) error {
	permission := agentsdk.KnowledgeLibraryPermission(operation)
	if permission == nil {
		permission = agentsdk.KnowledgeDocumentPermission(operation)
	}
	if permission == nil {
		return &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.library_access_denied"}
	}
	principal, err := h.principal(ctx, authority)
	if err != nil {
		return err
	}
	facts := identitysdk.ResourceFacts{"workspace_id": authority.WorkspaceID, "library_id": library.ID, "library_kind": library.Kind, "library_role": library.Role, "owner_user_id": library.OwnerUserID}
	decision, err := evaluator.Evaluate(*principal.AccessBundle, identitysdk.AccessRequest{ObjectKey: permission.ResourceKey, Action: permission.OperationKey}, facts, time.Now().UTC())
	if err != nil {
		return conversationBusinessReadError(err)
	}
	if !decision.Allowed {
		return &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.library_access_denied"}
	}
	return nil
}

func (h *ConversationBusinessHost) ValidateKnowledgeLibraryMember(ctx context.Context, user string, authority agentsdk.ConversationAuthority) error {
	denied := &agentsdk.Error{Class: "bad_request", Code: "agent.conversation.library_member_unavailable"}
	if user == "" || len(user) > 255 {
		return denied
	}
	if _, err := h.principal(ctx, authority); err != nil {
		return err
	}
	resolved, err := h.principals.Resolve(requestcontext.WithWorkspaceID(ctx, authority.WorkspaceID), identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(user)})
	if err != nil || !resolved.Principal.Known || resolved.Principal.UserID != user || resolved.Principal.WorkspaceID != authority.WorkspaceID {
		return denied
	}
	return nil
}

var _ agentsdk.KnowledgeLibraryAuthorizer = (*ConversationBusinessHost)(nil)
