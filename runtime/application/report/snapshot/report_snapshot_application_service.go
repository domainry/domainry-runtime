package snapshot

import (
	"context"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/query"
	reportsnapshot "github.com/domainry/domainry-runtime/runtime/domain/report/snapshot"
)

type ReportSnapshotNotificationCommitter interface {
	CompleteReportSnapshotWithNotification(context.Context, reportcontract.ReportSnapshotCompleteRequest, notificationmodel.NotificationEvent) error
	FailReportSnapshotWithNotification(context.Context, reportcontract.ReportSnapshotFailRequest, notificationmodel.NotificationEvent) error
}

func reportApplicationError(err error) error {
	return &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal_error", Err: err}
}

func reportWorkspaceError(err error) error {
	return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
}

type ReportSnapshotApplicationDependencies struct {
	Domain                *reportservice.ReportDomainService
	NotificationCompiler  func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	NotificationCommitter ReportSnapshotNotificationCommitter
}

// ReportSnapshotApplicationService owns command authorization and the
// cross-owner terminal transition between Report Snapshot and Notification.
type ReportSnapshotApplicationService struct {
	domain              *reportservice.ReportDomainService
	compileNotification func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	commitNotification  ReportSnapshotNotificationCommitter
}

func NewReportSnapshotApplicationService(dependencies ReportSnapshotApplicationDependencies) *ReportSnapshotApplicationService {
	return &ReportSnapshotApplicationService{
		domain: dependencies.Domain, compileNotification: dependencies.NotificationCompiler,
		commitNotification: dependencies.NotificationCommitter,
	}
}

func (s *ReportSnapshotApplicationService) RefreshSnapshot(ctx context.Context, reportKey, idempotencyKey string, principal principalmodel.Principal) (reportmodel.ReportSnapshot, error) {
	if _, err := principalmodel.CommandScopeForPrincipal(principal); err != nil {
		return reportmodel.ReportSnapshot{}, reportWorkspaceError(err)
	}
	refresh, terminalErr := s.domain.PrepareSnapshotRefresh(ctx, reportKey, idempotencyKey, principal)
	if refresh.TerminalStatus == "" {
		return refresh.Snapshot, terminalErr
	}
	event, notify, err := s.snapshotTerminalNotification(reportKey, idempotencyKey, refresh, principal)
	if err != nil {
		return reportmodel.ReportSnapshot{}, reportApplicationError(err)
	}
	if notify {
		if s.commitNotification == nil {
			return reportmodel.ReportSnapshot{}, &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.report.snapshot_notification_committer_unavailable"}
		}
		if refresh.TerminalStatus == "succeeded" {
			err = s.commitNotification.CompleteReportSnapshotWithNotification(ctx, reportcontract.ReportSnapshotCompleteRequest{Snapshot: refresh.Snapshot, ExpectedStatus: "refreshing", LeaseOwner: refresh.Snapshot.LeaseOwner, FencingToken: refresh.Snapshot.FencingToken}, event)
		} else {
			err = s.commitNotification.FailReportSnapshotWithNotification(ctx, reportcontract.ReportSnapshotFailRequest{WorkspaceID: refresh.Snapshot.WorkspaceID, ID: refresh.Snapshot.ID, ExpectedStatus: "refreshing", ErrorCode: refresh.ErrorCode, LeaseOwner: refresh.Snapshot.LeaseOwner, FencingToken: refresh.Snapshot.FencingToken}, event)
		}
	} else {
		err = s.domain.CommitSnapshotRefresh(ctx, refresh)
	}
	if err != nil {
		return reportmodel.ReportSnapshot{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.snapshot_terminal_commit_failed", Err: err}
	}
	if terminalErr != nil {
		return reportmodel.ReportSnapshot{}, terminalErr
	}
	return refresh.Snapshot, nil
}

func (s *ReportSnapshotApplicationService) snapshotTerminalNotification(reportKey, idempotencyKey string, refresh reportsnapshot.ReportSnapshotRefresh, principal principalmodel.Principal) (notificationmodel.NotificationEvent, bool, error) {
	if s.compileNotification == nil || strings.TrimSpace(principal.UserID) == "" {
		return notificationmodel.NotificationEvent{}, false, nil
	}
	status := "completed"
	if refresh.TerminalStatus == "failed" {
		status = "failed"
	}
	sourceID := "report-snapshot:" + strings.TrimSpace(reportKey) + ":" + strings.TrimSpace(idempotencyKey) + ":" + status
	intent := notificationmodel.NotificationIntent{
		WorkspaceID: principal.WorkspaceID, SourceEventID: sourceID, EventType: "report.snapshot." + status, Surface: "business_workspace",
		RecipientUserIDs: []string{principal.UserID}, SubjectType: "report", SubjectID: strings.TrimSpace(reportKey), DedupeKey: sourceID,
		OccurredAt: reportSnapshotOccurredAt(refresh.Snapshot), Variables: map[string]any{"report_key": strings.TrimSpace(reportKey), "status": status, "error_code": refresh.ErrorCode},
	}
	event, err := s.compileNotification(intent)
	return event, true, err
}

func reportSnapshotOccurredAt(snapshot reportmodel.ReportSnapshot) string {
	if value := strings.TrimSpace(snapshot.RefreshedAt); value != "" {
		return value
	}
	return time.Now().UTC().Format(time.RFC3339Nano)
}
