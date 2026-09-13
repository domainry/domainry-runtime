package workflow

import (
	"context"
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
)

// AgentWorkflowReceipt exposes acceptance identifiers, never process variables
// or raw execution outputs. Accepted does not mean the process has completed.
type AgentWorkflowReceipt struct {
	ExecutionID, ProcessID, WorkflowKey string
}

func workflowReceiptPermissionPresent(key string, p principalmodel.Principal) bool {
	return p.AccessBundle != nil && p.HasExactPermission(workflowcontract.ReceiptReadActionKey(key))
}

func (s *WorkflowApplicationService) AgentWorkflowReceiptDefinition(ctx context.Context, key string, p principalmodel.Principal) (definitionmodel.WorkflowSchema, error) {
	return s.agentWorkflowDefinition(ctx, key, p, true)
}

// ReadAgentWorkflowReceipt reads the original start ledger and rechecks current
// process participation. Neither a start nor a receipt reconciliation is run.
func (s *WorkflowApplicationService) ReadAgentWorkflowReceipt(ctx context.Context, workflowKey string, payload map[string]any, callerKey string, p principalmodel.Principal) (AgentWorkflowReceipt, bool, error) {
	workflow, err := s.AgentWorkflowReceiptDefinition(ctx, workflowKey, p)
	if err != nil {
		return AgentWorkflowReceipt{}, false, err
	}
	execution, found, err := s.inspectAgentWorkflowInvocation(ctx, workflow, payload, callerKey, p)
	if err != nil || !found {
		return AgentWorkflowReceipt{}, found, err
	}
	detail, err := s.WorkflowProcess(ctx, execution.ProcessID, p)
	if err != nil {
		return AgentWorkflowReceipt{}, false, err
	}
	process := detail.Process
	if process.ID != execution.ProcessID || process.WorkflowKey != workflow.Key || process.WorkspaceID != p.WorkspaceID {
		return AgentWorkflowReceipt{}, false, forbidden("backend.workflow.receipt_read_denied")
	}
	key := workflowcontract.ReceiptReadActionKey(workflow.Key)
	decision, err := evaluator.Evaluate(*p.AccessBundle, identitysdk.AccessRequest{ObjectKey: strings.TrimSuffix(key, ".read"), Action: "read"}, identitysdk.ResourceFacts{
		"owner_user_id": execution.ActorID, "workspace_id": execution.WorkspaceID,
		"workflow_key": workflow.Key, "process_id": process.ID,
		"object_key": process.ObjectKey, "record_id": process.RecordID,
	}, time.Now().UTC())
	if err != nil || !decision.Allowed {
		return AgentWorkflowReceipt{}, false, forbidden("backend.workflow.receipt_read_denied")
	}
	return AgentWorkflowReceipt{ExecutionID: execution.ID, ProcessID: process.ID, WorkflowKey: workflow.Key}, true, nil
}
