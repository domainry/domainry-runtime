package validation

import (
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	notificationsdkcontract "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-notification-sdk/modulehost"
)

func validateNotificationTemplates(values []notificationmodel.NotificationTemplate) error {
	capabilities, err := modulehost.DefaultTemplateCapabilityCatalog()
	if err != nil {
		return err
	}
	validator, err := notificationsdkcontract.NewNotificationTemplateValidator(capabilities)
	if err != nil {
		return err
	}
	return validator.ValidateAll(values)
}
