package notification

import (
	"context"
	"strings"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

type NotificationPublicationLocator struct {
	RequestID string
}

func (s *NotificationApplicationService) PublicationWakeups(context.Context) <-chan NotificationPublicationLocator {
	if s == nil {
		return nil
	}
	return s.publicationWakeups
}

func (s *NotificationApplicationService) WakePublication(_ context.Context, locator NotificationPublicationLocator) {
	if s == nil || s.publicationWakeups == nil || strings.TrimSpace(locator.RequestID) == "" {
		return
	}
	locator.RequestID = strings.TrimSpace(locator.RequestID)
	select {
	case s.publicationWakeups <- locator:
	default:
		// Durable recovery owns progress if the local acceleration hint is full.
	}
}

func (s *NotificationApplicationService) ProcessPublication(ctx context.Context, locator NotificationPublicationLocator, scope principalmodel.SystemScope) (bool, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil || scope.Kind != principalmodel.SystemScopeInstallation {
		return false, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.system_scope_required", Err: err}
	}
	if s == nil || s.publicationProcessor == nil || strings.TrimSpace(locator.RequestID) == "" {
		return false, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.notification.publication_locator_invalid"}
	}
	var processed bool
	var err error
	processed, err = s.publicationProcessor.Process(ctx, strings.TrimSpace(locator.RequestID))
	err = mapNotificationModuleError(err)
	if err != nil {
		workerplatform.ObserveOutcome("notification_publication", "failed")
		return processed, err
	}
	if processed {
		workerplatform.ObserveOutcome("notification_publication", "claimed")
		workerplatform.ObserveOutcome("notification_publication", "completed")
	}
	return processed, nil
}
