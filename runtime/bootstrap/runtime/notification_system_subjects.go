package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
)

type notificationSystemSubjectLifecycle struct {
	subjects notificationsdk.SystemSubjects
}

func (notificationSystemSubjectLifecycle) Owner(context.Context) string { return "notification" }

func (s notificationSystemSubjectLifecycle) PreviewSubject(ctx context.Context, workspaceID, subjectID string) (json.RawMessage, error) {
	if s.subjects == nil {
		return nil, fmt.Errorf("Notification system subject port is unavailable")
	}
	return s.subjects.PreviewSubject(ctx, workspaceID, subjectID)
}

func (s notificationSystemSubjectLifecycle) ExportSubject(ctx context.Context, workspaceID, subjectID string) (json.RawMessage, error) {
	if s.subjects == nil {
		return nil, fmt.Errorf("Notification system subject port is unavailable")
	}
	return s.subjects.ExportSubject(ctx, workspaceID, subjectID)
}

func (s notificationSystemSubjectLifecycle) ExportSubjectForRequest(ctx context.Context, _ string, workspaceID, subjectID string) (json.RawMessage, error) {
	return s.ExportSubject(ctx, workspaceID, subjectID)
}

func (s notificationSystemSubjectLifecycle) EraseSubject(ctx context.Context, workspaceID, subjectID string, holds []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	if s.subjects == nil {
		return nil, fmt.Errorf("Notification system subject port is unavailable")
	}
	evidence, err := json.Marshal(holds)
	if err != nil {
		return nil, err
	}
	return s.subjects.EraseSubject(ctx, workspaceID, subjectID, evidence)
}

func (s notificationSystemSubjectLifecycle) EraseSubjectForRequest(ctx context.Context, _ string, workspaceID, subjectID string, holds []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	return s.EraseSubject(ctx, workspaceID, subjectID, holds)
}

var _ lifecyclecontract.SubjectExecutionHandler = notificationSystemSubjectLifecycle{}

type notificationSubjectErasurePlan struct {
	RequestID      string          `json:"request_id"`
	WorkspaceID    string          `json:"workspace_id"`
	SubjectID      string          `json:"subject_id"`
	PreparedCounts json.RawMessage `json:"prepared_counts"`
}

func (s notificationSystemSubjectLifecycle) PrepareSubjectErasure(ctx context.Context, requestID, workspaceID, subjectID string) (json.RawMessage, error) {
	counts, err := s.PreviewSubject(ctx, workspaceID, subjectID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(notificationSubjectErasurePlan{RequestID: requestID, WorkspaceID: workspaceID, SubjectID: subjectID, PreparedCounts: counts})
}
func (s notificationSystemSubjectLifecycle) ErasePreparedSubject(ctx context.Context, requestID, workspaceID, subjectID string, raw json.RawMessage, holds []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	var plan notificationSubjectErasurePlan
	if json.Unmarshal(raw, &plan) != nil || requestID == "" || plan.RequestID != requestID || plan.WorkspaceID != workspaceID || plan.SubjectID != subjectID {
		return nil, fmt.Errorf("notification subject erasure plan scope mismatch")
	}
	if len(holds) > 0 {
		return nil, fmt.Errorf("notification subject erasure blocked by legal hold")
	}
	if _, err := s.EraseSubject(ctx, workspaceID, subjectID, holds); err != nil {
		return nil, err
	}
	// Source erasure is an idempotent redaction. Report the frozen inventory,
	// since attempt-local row counts change when execution resumes after a crash.
	return json.Marshal(struct {
		Plan   notificationSubjectErasurePlan `json:"plan"`
		Erased bool                           `json:"erased"`
	}{plan, true})
}

var _ lifecyclecontract.PreparedSubjectErasureHandler = notificationSystemSubjectLifecycle{}
