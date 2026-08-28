package validation

import (
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	notificationsdkcontract "github.com/domainry/domainry-notification-sdk/contract"
	notificationcontract "github.com/domainry/domainry-runtime/runtime/domain/notification/contract"
)

func validateNotificationTemplates(values []notificationmodel.NotificationTemplate) error {
	capabilities, err := notificationcontract.NotificationTemplateCapabilityCatalog()
	if err != nil {
		return err
	}
	validator, err := notificationsdkcontract.NewNotificationTemplateValidator(capabilities)
	if err != nil {
		return err
	}
	return validator.ValidateAll(values)
}
