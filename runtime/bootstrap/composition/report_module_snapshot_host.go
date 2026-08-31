package composition

import (
	"context"
	"fmt"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	reportmodulehost "github.com/domainry/domainry-report-sdk/modulehost"
	reportpersistence "github.com/domainry/domainry-report-sdk/persistence"
)

type reportModuleSnapshotHost struct {
	compile   func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	committer ReportSnapshotNotificationCommitter
}

// ReportSnapshotNotificationCommitter is the Runtime host's atomic
// cross-owner transaction adapter. Report owns terminal orchestration; the
// host commits Report state and the Notification event in one transaction.
type ReportSnapshotNotificationCommitter interface {
	CompleteReportSnapshotWithNotification(context.Context, reportpersistence.SnapshotCompleteRequest, notificationmodel.NotificationEvent) error
	FailReportSnapshotWithNotification(context.Context, reportpersistence.SnapshotFailRequest, notificationmodel.NotificationEvent) error
}

func (h reportModuleSnapshotHost) CompleteReportSnapshot(ctx context.Context, request reportpersistence.SnapshotCompleteRequest, intent notificationmodel.NotificationIntent) error {
	event, err := h.notification(intent)
	if err != nil {
		return err
	}
	return h.committer.CompleteReportSnapshotWithNotification(ctx, request, event)
}

func (h reportModuleSnapshotHost) FailReportSnapshot(ctx context.Context, request reportpersistence.SnapshotFailRequest, intent notificationmodel.NotificationIntent) error {
	event, err := h.notification(intent)
	if err != nil {
		return err
	}
	return h.committer.FailReportSnapshotWithNotification(ctx, request, event)
}

func (h reportModuleSnapshotHost) notification(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
	if h.compile == nil || h.committer == nil {
		return notificationmodel.NotificationEvent{}, fmt.Errorf("Report snapshot notification host is unavailable")
	}
	return h.compile(intent)
}

var _ reportmodulehost.SnapshotTerminalCommitter = reportModuleSnapshotHost{}
