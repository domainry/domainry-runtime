package agenthost

import (
	"bytes"
	"context"
	"encoding/json"

	agent "github.com/domainry/domainry-agent-sdk"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type conversationBusinessActionCatalogReceiptReader interface {
	AgentSharedActionReceiptDefinition(context.Context, string, principalmodel.Principal, principalmodel.Principal) (definitionmodel.ActionSchema, error)
}

type conversationBusinessWorkflowCatalogReceiptReader interface {
	AgentSharedWorkflowReceiptDefinition(context.Context, string, principalmodel.Principal, principalmodel.Principal) (definitionmodel.WorkflowSchema, error)
}

// No source signature is added to an unsigned catalog. Current owner contract
// projection must match the entire original result for each actual reader.
// Execution grants retain their existing metadata-read compatibility; explicit
// receipt grants provide an independent path after execution is withdrawn.
func (h *ConversationBusinessHost) readBusinessCatalogReceipt(ctx context.Context, e agent.ConversationBusinessEvidence, a, producer agent.ConversationAuthority) error {
	var q agent.ConversationBusinessCatalogQuery
	if !h.ownedWriteEvidence(e, producer) || !decodeBusinessReceipt(e.Input, &q) {
		return conversationBusinessError("forbidden")
	}
	original, err := h.principal(ctx, producer)
	if err != nil {
		return err
	}
	for _, current := range []agent.ConversationAuthority{producer, a} {
		// Compare metadata under this actual identity without substituting its
		// user into the original evidence scope or granting execution authority.
		out, err := h.BusinessCatalog(ctx, q, current)
		if err == nil {
			raw, marshalErr := json.Marshal(out)
			if marshalErr == nil && bytes.Equal(raw, e.Data) {
				continue
			}
		}
		if q.Kind != "actions" && q.Kind != "workflows" {
			return conversationBusinessError("forbidden")
		}
		out, err = h.businessCatalogAccess(ctx, q, current, &original)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(out)
		if err != nil || !bytes.Equal(raw, e.Data) {
			return conversationBusinessError("forbidden")
		}
	}
	return nil
}

var _ conversationBusinessActionCatalogReceiptReader = (*actionapplication.ActionApplicationService)(nil)
var _ conversationBusinessWorkflowCatalogReceiptReader = (*workflowapplication.WorkflowApplicationService)(nil)
