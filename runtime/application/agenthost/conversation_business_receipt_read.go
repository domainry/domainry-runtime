package agenthost

import (
	"context"

	agent "github.com/domainry/domainry-agent-sdk"
)

func (h *ConversationBusinessHost) AuthorizeBusinessResultRead(ctx context.Context, e agent.ConversationBusinessEvidence, a agent.ConversationAuthority) error {
	switch e.Operation {
	case "business_catalog":
		p, err := h.principal(ctx, a)
		if err != nil {
			return err
		}
		policy := h.businessPolicyDigest(ctx, p)
		if err := h.readBusinessCatalogReceipt(ctx, e, a, a); err != nil {
			return err
		}
		return h.unchangedReceiptReadAuthority(ctx, a, policy)
	case "query_records", "get_record", "query_related_records":
		// These owner reads already enforce current Identity, object/record
		// scopes, field masking and the original signed or exact snapshot.
		// They do not authorize the producing Agent tool Action.
		return h.RevalidateBusiness(ctx, e, a)
	case "workflow_get":
		return h.revalidateBusinessWorkflow(ctx, e, a, true)
	case "invoke_action":
		return h.readBusinessActionReceipt(ctx, e, a)
	case "workflow_start":
		return h.readBusinessWorkflowStartReceipt(ctx, e, a)
	default:
		// An unmigrated owner keeps its original guarded read behavior.
		return &agent.Error{Class: "unavailable", Code: agent.BusinessResultReadUnsupportedCode}
	}
}

var _ agent.ConversationBusinessResultReadSource = (*ConversationBusinessHost)(nil)
