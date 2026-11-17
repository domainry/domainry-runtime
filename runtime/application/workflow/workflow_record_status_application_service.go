package workflow

import (
	"context"
	"regexp"
	"strings"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

// RecordWorkflowStatus deliberately contains no executable or model payload.
// Record visibility is the authority for this projection; the caller does not
// acquire the separate Workflow participant, administration or recovery APIs.
type RecordWorkflowStatus struct {
	Found                                     bool
	ProcessID, WorkflowKey, Status, ErrorCode string
	StartedAt, UpdatedAt, CompletedAt         int64
}

var recordWorkflowErrorCode = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,159}$`)

func (s *WorkflowApplicationService) ReadRecordWorkflow(ctx context.Context, principal principalmodel.Principal, objectKey, recordID, workflowKey string) (RecordWorkflowStatus, error) {
	if err := workflowAuthorizeQuery(principal); err != nil {
		return RecordWorkflowStatus{}, err
	}
	if !principal.Known || principal.UserID == "" {
		return RecordWorkflowStatus{}, forbidden("backend.workflow.record_status_identity_required")
	}
	objectKey, recordID, workflowKey = strings.TrimSpace(objectKey), strings.TrimSpace(recordID), strings.TrimSpace(workflowKey)
	if objectKey == "" || recordID == "" || workflowKey == "" || len(objectKey) > 160 || len(recordID) > 220 || len(workflowKey) > 220 {
		return RecordWorkflowStatus{}, badRequest("backend.workflow.record_status_request_invalid")
	}
	if s.objectForAction == nil || s.recordReader == nil || s.canAccess == nil || s.processRepo == nil {
		return RecordWorkflowStatus{}, internalError("record Workflow status unavailable", nil)
	}
	authorize := func() error {
		object, err := s.objectForAction(ctx, principal, objectKey, "read")
		if err != nil {
			return err
		}
		record, found, err := s.recordReader.GetWorkflowRecord(ctx, principal.WorkspaceID, object, recordID, principal)
		if err != nil {
			return internalError("read Workflow source record", err)
		}
		if !found || record.RecordID != recordID || !s.canAccess(ctx, principal, object, record) {
			return notFound("backend.record.not_found")
		}
		return nil
	}
	if err := authorize(); err != nil {
		return RecordWorkflowStatus{}, err
	}
	// Two rows detect ambiguity instead of silently choosing a successful or
	// failed attempt. The database owns filtering and the bounded lookup.
	processes, err := s.processRepo.ListProcesses(ctx, principal.WorkspaceID, workflowmodel.WorkflowProcessFilter{WorkflowKey: workflowKey, ObjectKey: objectKey, RecordID: recordID, Limit: 2})
	if err != nil {
		return RecordWorkflowStatus{}, internalError("read record Workflow status", err)
	}
	if err := authorize(); err != nil {
		return RecordWorkflowStatus{}, err
	}
	if len(processes) == 0 {
		return RecordWorkflowStatus{}, nil
	}
	if len(processes) != 1 {
		return RecordWorkflowStatus{}, conflict("backend.workflow.record_status_ambiguous")
	}
	p := processes[0]
	if p.WorkspaceID != principal.WorkspaceID || p.WorkflowKey != workflowKey || p.ObjectKey != objectKey || p.RecordID != recordID {
		return RecordWorkflowStatus{}, internalError("record Workflow scope mismatch", nil)
	}
	if p.ProcessID == "" {
		return RecordWorkflowStatus{}, internalError("record Workflow reference missing", nil)
	}
	switch p.Status {
	case "pending", "running", "waiting", "completed", "failed", "configuration_error", "cancelled", "rejected", "withdrawn":
	default:
		return RecordWorkflowStatus{}, internalError("record Workflow status invalid", nil)
	}
	errorCode := ""
	if p.Status == "failed" || p.Status == "configuration_error" {
		errorCode = p.ErrorCode
		if errorCode != "" && !recordWorkflowErrorCode.MatchString(errorCode) {
			return RecordWorkflowStatus{}, internalError("record Workflow error code invalid", nil)
		}
	}
	instant := func(value string) (int64, error) {
		if value == "" {
			return 0, nil
		}
		at, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return 0, internalError("record Workflow timestamp invalid", err)
		}
		return at.UTC().UnixMilli(), nil
	}
	started, err := instant(p.CreatedAt)
	if err != nil {
		return RecordWorkflowStatus{}, err
	}
	updated, err := instant(p.UpdatedAt)
	if err != nil {
		return RecordWorkflowStatus{}, err
	}
	completed, err := instant(p.CompletedAt)
	if err != nil {
		return RecordWorkflowStatus{}, err
	}
	return RecordWorkflowStatus{Found: true, ProcessID: p.ProcessID, WorkflowKey: workflowKey, Status: p.Status, ErrorCode: errorCode, StartedAt: started, UpdatedAt: updated, CompletedAt: completed}, nil
}
