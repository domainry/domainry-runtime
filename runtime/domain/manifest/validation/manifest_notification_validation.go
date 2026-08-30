package validation

import (
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	notificationsdkcontract "github.com/domainry/domainry-notification-sdk/contract"
	notificationbinding "github.com/domainry/domainry-runtime/runtime/platform/notificationbinding"
)

func validateNotificationTemplates(values []notificationmodel.NotificationTemplate) error {
	capabilities, err := notificationbinding.TemplateCapabilityCatalog()
	if err != nil {
		return err
	}
	validator, err := notificationsdkcontract.NewNotificationTemplateValidator(capabilities)
	if err != nil {
		return err
	}
	return validator.ValidateAll(values)
}
