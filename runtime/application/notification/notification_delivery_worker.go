package notification

import (
	"context"
	"strings"

	sourcenotification "github.com/domainry/domainry-notification"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

func (s *NotificationApplicationService) ProcessDueNotificationChannels(ctx context.Context, limit int, scope principalmodel.SystemScope) (int, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return 0, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.system_scope_required", Err: err}
	}
	if s == nil || s.deliveryProcessor == nil {
		return 0, &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.notification.channel_delivery_unavailable"}
	}
	processed, err := s.deliveryProcessor.ProcessDue(ctx, limit)
	return processed, mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) ProcessNotificationChannelPlan(ctx context.Context, locator workerplatform.DurableTaskLocator, scope principalmodel.SystemScope) (bool, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil || scope.Kind != principalmodel.SystemScopeRuntimeGlobal {
		return false, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.system_scope_required", Err: err}
	}
	if s == nil || s.deliveryProcessor == nil {
		return false, &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.notification.channel_delivery_unavailable"}
	}
	if strings.TrimSpace(locator.QueueKind) != "notification_channel" || strings.TrimSpace(locator.TaskID) == "" {
		return false, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.notification.channel_locator_invalid"}
	}
	if _, err := principalmodel.NewWorkspaceID(locator.WorkspaceID); err != nil {
		return false, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.notification.channel_locator_invalid", Err: err}
	}
	processed, err := s.deliveryProcessor.Process(ctx, sourcenotification.WorkspaceID(strings.TrimSpace(locator.WorkspaceID)), strings.TrimSpace(locator.TaskID))
	return processed, mapNotificationModuleError(err)
}
