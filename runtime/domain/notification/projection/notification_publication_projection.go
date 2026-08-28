package projection

import notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"

func ActivePublishedTemplates(records []notificationmodel.NotificationTemplateRecord) []notificationmodel.NotificationTemplate {
	result := make([]notificationmodel.NotificationTemplate, 0, len(records))
	for _, record := range records {
		if record.Status == "active" && record.Published != nil {
			result = append(result, *record.Published)
		}
	}
	return result
}
