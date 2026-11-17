package transport

import (
	"context"

	"github.com/domainry/domainry-runtime/pkg/runtimeengine"
)

func (e *projectEngine) ReadRecordWorkflow(ctx context.Context, objectKey, recordID, workflowKey string) (runtimeengine.RecordWorkflowStatus, error) {
	principal, err := e.requestPrincipal(ctx)
	if err != nil {
		return runtimeengine.RecordWorkflowStatus{}, err
	}
	if e.workflows == nil {
		return runtimeengine.RecordWorkflowStatus{}, runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.workflow.record_status_unavailable", nil, nil)
	}
	status, err := e.workflows.ReadRecordWorkflow(ctx, principal, objectKey, recordID, workflowKey)
	if err != nil {
		return runtimeengine.RecordWorkflowStatus{}, projectEngineError(err)
	}
	return runtimeengine.RecordWorkflowStatus{Found: status.Found, ProcessID: status.ProcessID, WorkflowKey: status.WorkflowKey, Status: status.Status, ErrorCode: status.ErrorCode, StartedAt: status.StartedAt, UpdatedAt: status.UpdatedAt, CompletedAt: status.CompletedAt}, nil
}
