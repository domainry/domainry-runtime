package contract

import (
	sourcetemplate "github.com/domainry/domainry-notification-sdk/contract"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
)

// Runtime notification models are SDK aliases. These named projections remain
// temporarily for callers while preserving one canonical contract type.
func ModuleTemplate(value notificationmodel.NotificationTemplate) sourcetemplate.NotificationTemplate {
	return value
}

func PlaneTemplate(value sourcetemplate.NotificationTemplate) notificationmodel.NotificationTemplate {
	return value
}

func PlaneTemplateRecord(value sourcetemplate.NotificationTemplateRecord) notificationmodel.NotificationTemplateRecord {
	return value
}
