package notification

import (
	"context"
	"strings"

	sourcenotification "github.com/domainry/domainry-notification"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

func (s *NotificationApplicationService) BindWorkerWakeups(_ context.Context, broker *workerplatform.WakeupBroker) {
	if s != nil && broker != nil {
		s.inboxWakeups = broker.Subscribe("notification_inbox", 256)
		s.channelWakeups = broker.Subscribe("notification_channel", 256)
	}
}

func (s *NotificationApplicationService) ChannelWakeups(context.Context) <-chan workerplatform.DurableTaskLocator {
	if s == nil {
		return nil
	}
	return s.channelWakeups
}

func (s *NotificationApplicationService) InboxWakeups(context.Context) <-chan workerplatform.DurableTaskLocator {
	if s == nil {
		return nil
	}
	return s.inboxWakeups
}

func (s *NotificationApplicationService) ProcessInboxEvent(ctx context.Context, locator workerplatform.DurableTaskLocator, scope principalmodel.SystemScope) (bool, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil || scope.Kind != principalmodel.SystemScopeRuntimeGlobal {
		return false, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.system_scope_required", Err: err}
	}
	if s == nil || s.inboxProcessor == nil {
		return false, notificationInboxUnavailable()
	}
	if strings.TrimSpace(locator.QueueKind) != "notification_inbox" || strings.TrimSpace(locator.TaskID) == "" {
		return false, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.notification.inbox_locator_invalid"}
	}
	if _, err := principalmodel.NewWorkspaceID(locator.WorkspaceID); err != nil {
		return false, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.notification.inbox_locator_invalid", Err: err}
	}
	processed, err := s.inboxProcessor.Process(ctx, sourcenotification.WorkspaceID(strings.TrimSpace(locator.WorkspaceID)), strings.TrimSpace(locator.TaskID))
	return processed, mapNotificationModuleError(err)
}
