package validation

import (
	"errors"

	notificationsdkcontract "github.com/domainry/domainry-notification-sdk/contract"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func NotificationValidateEventTypes(values []notificationmodel.NotificationEventType, rules []notificationmodel.NotificationRule) error {
	return mapNotificationCatalogValidationError(notificationsdkcontract.ValidateEventTypes(values, rules))
}

func NotificationValidateEventType(value notificationmodel.NotificationEventType) (notificationmodel.NotificationEventType, error) {
	validated, err := notificationsdkcontract.ValidateEventType(value)
	return validated, mapNotificationCatalogValidationError(err)
}

func mapNotificationCatalogValidationError(err error) error {
	if err == nil {
		return nil
	}
	var source *notificationsdkcontract.CatalogValidationError
	if !errors.As(err, &source) {
		return err
	}
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: source.Code, Params: source.Params, Err: err}
}
