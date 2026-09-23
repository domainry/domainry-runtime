package recordtimer

import (
	"context"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func (s *RecordTimerApplicationService) InspectFailure(ctx context.Context, id string, principal principalmodel.Principal) (recordmodel.Record, error) {
	if err := recordTimerAuthorizeRecoveryRead(principal); err != nil {
		return recordmodel.Record{}, err
	}
	return s.failedRecordTimer(ctx, strings.TrimSpace(id), principal)
}

func (s *RecordTimerApplicationService) RetryFailure(ctx context.Context, id, reason string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.transitionRecordTimerFailure(ctx, id, reason, "requeued", "scheduled", principal)
}

func (s *RecordTimerApplicationService) ResolveFailure(ctx context.Context, id, reason string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.transitionRecordTimerFailure(ctx, id, reason, "resolved", "cancelled", principal)
}

func (s *RecordTimerApplicationService) failedRecordTimer(ctx context.Context, id string, principal principalmodel.Principal) (recordmodel.Record, error) {
	if s == nil || s.repository == nil {
		return recordmodel.Record{}, recordTimerError(apperror.KindUnavailable, "backend.record_timer.repository_unavailable", nil)
	}
	object, err := s.objectForPrincipal(ctx, recordTimerWorkerPrincipal(), "record_timer")
	if err != nil {
		return recordmodel.Record{}, err
	}
	record, found, err := s.repository.GetRecord(ctx, principal.WorkspaceID, object, id)
	if err != nil {
		return recordmodel.Record{}, recordTimerInternalError("get failed record timer", err)
	}
	if !found {
		return recordmodel.Record{}, recordTimerError(apperror.KindNotFound, "backend.record_timer.not_found", nil, "record_timer_id", id)
	}
	if strings.TrimSpace(recordTimerString(record, "status")) != "failed" {
		return recordmodel.Record{}, recordTimerError(apperror.KindConflict, "backend.record_timer.not_failed", nil)
	}
	return record, nil
}

func (s *RecordTimerApplicationService) transitionRecordTimerFailure(ctx context.Context, id, reason, eventType, status string, principal principalmodel.Principal) (recordmodel.Record, error) {
	if err := recordTimerAuthorizeCommand(principal); err != nil {
		return recordmodel.Record{}, err
	}
	if err := recordTimerAuthorizeRecoveryWrite(principal); err != nil {
		return recordmodel.Record{}, err
	}
	record, err := s.failedRecordTimer(ctx, strings.TrimSpace(id), principal)
	if err != nil {
		return recordmodel.Record{}, err
	}
	object, err := s.objectForPrincipal(ctx, recordTimerWorkerPrincipal(), "record_timer")
	if err != nil {
		return recordmodel.Record{}, err
	}
	eventObject, err := s.recordTimerEventObject(ctx)
	if err != nil {
		return recordmodel.Record{}, err
	}
	now := s.worker.Clock.Now().UTC()
	record = cloneRecordTimer(record)
	if operationID := requestcontext.OwnerExecutionID(ctx); operationID != "" {
		record.Data["operation_id"] = operationID
	}
	record.Data["status"] = status
	record.Data["lease_owner"], record.Data["lease_expires_at"] = "", ""
	record.Data["last_error"], record.Data["failed_at"] = "", ""
	if status == "scheduled" {
		record.Data["attempt"] = 0
		record.Data["due_at"] = now.Format(time.RFC3339Nano)
	} else {
		record.Data["cancelled_at"] = now.Format(time.RFC3339Nano)
	}
	record.UpdatedAt = now.Format(time.RFC3339Nano)
	commits := []transactionmodel.RecordMutationCommit{
		{Operation: "update", Object: object, Record: record, Conditions: map[string]any{"status": "failed", "fencing_token": record.Data["fencing_token"]}},
		buildRecordTimerEventCommit(eventObject, record, eventType, principal.UserID, "", strings.TrimSpace(reason), recordTimerString(record, "due_at"), now),
	}
	if err := s.repository.CommitRecordMutationBatch(ctx, principal.WorkspaceID, commits); err != nil {
		return recordmodel.Record{}, recordTimerInternalError("transition failed record timer", err)
	}
	return record, nil
}
