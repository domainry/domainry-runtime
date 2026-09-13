package agenthost

import (
	"context"
	"errors"
	"time"

	agent "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-identity-sdk/authorization/evaluator"
)

// Agent loads delegation facts and intersects this current Identity decision
// with the delegation's participant/executor grant. Runtime supplies policy,
// never a second delegation store or a client-selected acting Agent identity.
func (h *ConversationBusinessHost) AuthorizeConversationCollaboration(ctx context.Context, in agent.ConversationCollaborationAuthorizationRequest) (agent.ConversationCollaborationAuthorization, error) {
	var out agent.ConversationCollaborationAuthorization
	p, err := h.principal(ctx, in.Authority)
	if err != nil {
		var denied *agent.Error
		if errors.As(err, &denied) && (denied.Class == "forbidden" || denied.Class == "not_found") {
			return out, nil
		}
		return out, err
	}
	if in.OwnerUserID == "" {
		return out, nil
	}
	out.Revision = string(p.AccessBundle.AuthorizationRevision)
	for _, operation := range in.Operations {
		permission := agent.ConversationCollaborationPermission(operation)
		if permission == nil {
			continue
		}
		decision, err := evaluator.Evaluate(*p.AccessBundle, identity.AccessRequest{ObjectKey: permission.ResourceKey, Action: permission.OperationKey}, identity.ResourceFacts{"owner_user_id": in.OwnerUserID, "workspace_id": in.Authority.WorkspaceID, "delegation_id": in.DelegationID, "from_agent_id": in.FromAgentID, "to_agent_id": in.ToAgentID}, time.Now().UTC())
		if err != nil {
			return agent.ConversationCollaborationAuthorization{}, conversationBusinessReadError(err)
		}
		if decision.Allowed {
			out.Allowed = append(out.Allowed, operation)
		}
	}
	return out, ctx.Err()
}

func (h *ConversationBusinessHost) ValidateConversationAgentSubjects(ctx context.Context, a agent.ConversationAuthority, users []string) error {
	if len(users) > 64 {
		return &agent.Error{Class: "bad_request", Code: "agent.conversation.agent_sharing_invalid"}
	}
	if _, err := h.principal(ctx, a); err != nil {
		return err
	}
	for _, user := range users {
		// Membership resolution is not an operation authorization and its
		// principal is never passed to the actual reader's tool/data calls.
		subject := agent.ConversationAuthority{Known: true, RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: user}
		if _, err := h.principal(ctx, subject); err != nil {
			return err
		}
	}
	return ctx.Err()
}

var _ agent.ConversationCollaborationAuthorizer = (*ConversationBusinessHost)(nil)
var _ agent.ConversationAgentSubjectValidator = (*ConversationBusinessHost)(nil)
