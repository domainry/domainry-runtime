package action

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
)

func (e *businessActionExecution) ReadWorkflowAgentTaskEvidence(ctx context.Context, request runtimeext.WorkflowAgentTaskEvidenceRequest) (runtimeext.WorkflowAgentTaskEvidence, error) {
	if e == nil || e.unitOfWork == nil || e.dependencies.ReadWorkflowAgentTaskEvidence == nil {
		return runtimeext.WorkflowAgentTaskEvidence{}, apperror.New(apperror.KindUnavailable, runtimeext.WorkflowAgentTaskEvidenceUnavailableErrorCode, nil, nil)
	}
	if e.unitOfWork.phases.current() != runtimeext.ExecutionPhasePrewrite {
		return runtimeext.WorkflowAgentTaskEvidence{}, apperror.New(apperror.KindConflict, "backend.action.execution_phase_invalid", nil, nil)
	}
	if e.invocation.Source != actionmodel.ActionSourceWorkflow || strings.TrimSpace(e.invocation.ProcessID) == "" || strings.TrimSpace(e.invocation.NodeID) == "" || !e.invocation.Principal.Known || !e.hasWorkflowOperationGrant(strings.TrimSpace(request.WorkflowKey), runtimeext.WorkflowReadAgentTaskEvidenceOperation) {
		return runtimeext.WorkflowAgentTaskEvidence{}, apperror.New(apperror.KindForbidden, runtimeext.WorkflowAgentTaskEvidenceDeniedErrorCode, nil, nil)
	}
	return e.dependencies.ReadWorkflowAgentTaskEvidence(e.unitOfWork.executionContext(ctx), request, e.invocation)
}

var _ runtimeext.WorkflowAgentTaskEvidenceExecution = (*businessActionExecution)(nil)
