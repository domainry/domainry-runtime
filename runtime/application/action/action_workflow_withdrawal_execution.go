package action

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

var _ runtimeext.WorkflowWithdrawalExecution = (*businessActionExecution)(nil)

func (e *businessActionExecution) StageWorkflowWithdrawal(ctx context.Context, request runtimeext.WorkflowWithdrawal) (runtimeext.WorkflowWithdrawalReceipt, error) {
	if e == nil || e.dependencies.StageWorkflowWithdrawal == nil {
		return runtimeext.WorkflowWithdrawalReceipt{}, apperror.New(apperror.KindInternal, "backend.action.workflow_withdrawal_unavailable", nil, nil)
	}
	request.WorkflowKey = strings.TrimSpace(request.WorkflowKey)
	if !e.hasWorkflowOperationGrant(request.WorkflowKey, runtimeext.WorkflowWithdrawOperation) {
		return runtimeext.WorkflowWithdrawalReceipt{}, apperror.New(apperror.KindForbidden, runtimeext.WorkflowGrantDeniedErrorCode, nil, nil)
	}
	phase := e.unitOfWork.phases.current()
	if phase != runtimeext.ExecutionPhasePrewrite && phase != runtimeext.ExecutionPhaseWriting {
		return runtimeext.WorkflowWithdrawalReceipt{}, apperror.New(apperror.KindConflict, "backend.action.execution_phase_invalid", nil, nil)
	}
	for _, staged := range e.workflowWithdrawals {
		if staged.Process.ID == strings.TrimSpace(request.ProcessID) {
			if staged.Process.ObjectKey != strings.TrimSpace(request.ObjectKey) || staged.Process.RecordID != strings.TrimSpace(request.RecordID) || staged.Process.WorkflowKey != request.WorkflowKey {
				return runtimeext.WorkflowWithdrawalReceipt{}, apperror.New(apperror.KindForbidden, "backend.workflow.withdrawal_binding_mismatch", nil, nil)
			}
			return runtimeext.WorkflowWithdrawalReceipt{ProcessID: staged.Process.ID, CommandID: staged.CommandID, WithdrawnAt: staged.Process.CompletedAt}, nil
		}
	}
	commit, err := e.dependencies.StageWorkflowWithdrawal(e.unitOfWork.executionContext(ctx), request, e.identity.ExecutionID, e.invocation.Principal)
	if err != nil {
		return runtimeext.WorkflowWithdrawalReceipt{}, err
	}
	e.workflowWithdrawals = append(e.workflowWithdrawals, commit)
	return runtimeext.WorkflowWithdrawalReceipt{ProcessID: commit.Process.ID, CommandID: commit.CommandID, WithdrawnAt: commit.Process.CompletedAt}, nil
}

func attachWorkflowWithdrawals(commits []transactionmodel.RecordMutationCommit, withdrawals []transactionmodel.WorkflowWithdrawalCommit) ([]transactionmodel.RecordMutationCommit, error) {
	if len(withdrawals) == 0 {
		return commits, nil
	}
	if len(commits) == 0 {
		return nil, apperror.New(apperror.KindBadRequest, "backend.action.workflow_withdrawal_requires_business_mutation", nil, nil)
	}
	commits[len(commits)-1].WorkflowWithdrawals = append(commits[len(commits)-1].WorkflowWithdrawals, withdrawals...)
	return commits, nil
}
