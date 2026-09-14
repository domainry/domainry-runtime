package workflow

import (
	"context"
	"strings"
	"time"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

// StageWorkflowWithdrawal only describes the transition. The Action owns the
// physical commit, including the project's receipt and released occupancy.
func (s *WorkflowApplicationService) StageWorkflowWithdrawal(ctx context.Context, request runtimeext.WorkflowWithdrawal, callerKey string, principal principalmodel.Principal) (transactionmodel.WorkflowWithdrawalCommit, error) {
	if err := workflowAuthorizeCommand(principal); err != nil {
		return transactionmodel.WorkflowWithdrawalCommit{}, err
	}
	if strings.TrimSpace(request.WorkflowKey) == "" || strings.TrimSpace(request.ObjectKey) == "" || strings.TrimSpace(request.RecordID) == "" || strings.TrimSpace(request.ProcessID) == "" || strings.TrimSpace(callerKey) == "" {
		return transactionmodel.WorkflowWithdrawalCommit{}, badRequest("backend.workflow.withdrawal_binding_required")
	}
	process, ok, err := s.processRepo.GetProcess(ctx, principal.WorkspaceID, strings.TrimSpace(request.ProcessID))
	if err != nil {
		return transactionmodel.WorkflowWithdrawalCommit{}, internalError("get withdrawal workflow process", err)
	}
	if !ok {
		return transactionmodel.WorkflowWithdrawalCommit{}, notFound("backend.workflow.process_not_found")
	}
	if process.WorkflowKey != strings.TrimSpace(request.WorkflowKey) || process.ObjectKey != strings.TrimSpace(request.ObjectKey) || process.RecordID != strings.TrimSpace(request.RecordID) {
		return transactionmodel.WorkflowWithdrawalCommit{}, forbidden("backend.workflow.withdrawal_binding_mismatch")
	}
	return prepareWorkflowWithdrawal(process, callerKey, principal)
}

func prepareWorkflowWithdrawal(process workflowmodel.WorkflowProcessInstance, callerKey string, principal principalmodel.Principal) (transactionmodel.WorkflowWithdrawalCommit, error) {
	if process.InitiatorID != principal.UserID {
		return transactionmodel.WorkflowWithdrawalCommit{}, forbidden("backend.workflow.process_cancel_denied")
	}
	// Waiting is a durable decision boundary. A running engine must finish its
	// current node before withdrawal; it cannot race an external side effect.
	if process.Status != "waiting" {
		return transactionmodel.WorkflowWithdrawalCommit{}, conflict("backend.workflow.process_not_cancellable")
	}
	if strings.TrimSpace(process.UpdatedAt) == "" {
		return transactionmodel.WorkflowWithdrawalCommit{}, conflict("backend.workflow.withdrawal_revision_required")
	}
	commit := transactionmodel.WorkflowWithdrawalCommit{ExpectedUpdatedAt: process.UpdatedAt, ExpectedStatus: process.Status, ActorID: principal.UserID}
	commit.CommandID = workflowCommandKey("process.cancel", process.ID, callerKey, map[string]any{"command": "cancel"})
	process.Result = cloneWorkflowWithdrawalMap(process.Result)
	process.Variables = cloneWorkflowWithdrawalMap(process.Variables)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	process.Status, process.CurrentNodeIDs = "cancelled", nil
	process.UpdatedAt, process.CompletedAt = now, now
	process.Variables["approval_decision"] = "withdrawn"
	workflowRecordCommand(&process, "process.cancel", commit.CommandID)
	commit.Process = process
	return commit, nil
}

func cloneWorkflowWithdrawalMap(source map[string]any) map[string]any {
	target := make(map[string]any, len(source)+1)
	for key, value := range source {
		target[key] = value
	}
	return target
}
