package workflow

import (
	"context"
	"fmt"
	"strings"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

func workflowApprovalDecisionTasks(ctx context.Context, store workflowcontract.WorkflowProcessStore, process workflowmodel.WorkflowProcessInstance, task workflowmodel.WorkflowTask) ([]workflowmodel.WorkflowTask, error) {
	node, _ := workflowpolicy.WorkflowGraphNode(process.DefinitionSnapshot.Graph, task.NodeID)
	if strings.TrimSpace(workflowpolicy.WorkflowApprovalNodeContract(node).Mode) != "quorum" {
		return store.ListTasks(ctx, process.WorkspaceID, process.ID, "", "", 500)
	}
	reader, ok := store.(workflowcontract.WorkflowApprovalTaskReader)
	if !ok {
		return nil, fmt.Errorf("workflow quorum requires a complete node task reader")
	}
	tasks, err := reader.ListApprovalTasks(ctx, process.WorkspaceID, process.ID, task.NodeInstanceID)
	if err != nil {
		return nil, err
	}
	if err := workflowpolicy.WorkflowValidateApprovalAssigneeCount(node, len(tasks)); err != nil {
		return nil, err
	}
	return tasks, nil
}

func workflowApprovalDecisionCommit(process workflowmodel.WorkflowProcessInstance, task workflowmodel.WorkflowTask) transactionmodel.WorkflowDecisionCommit {
	commit := transactionmodel.WorkflowDecisionCommit{WorkspaceID: process.WorkspaceID, DecidedTask: task, ExpectedTaskStatus: "open", ExpectedAssigneeID: task.CompletedBy}
	node, _ := workflowpolicy.WorkflowGraphNode(process.DefinitionSnapshot.Graph, task.NodeID)
	if strings.TrimSpace(workflowpolicy.WorkflowApprovalNodeContract(node).Mode) == "quorum" {
		commit.ExpectedProcessUpdatedAt = process.UpdatedAt
	}
	return commit
}
