package policy

import (
	"strings"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
)

func NotificationSelectTemplateContent(template notificationmodel.NotificationTemplate, requestedLocale, runtimeLocale string) (string, notificationmodel.NotificationTemplateContent) {
	for _, locale := range []string{strings.TrimSpace(requestedLocale), strings.TrimSpace(runtimeLocale), strings.TrimSpace(template.DefaultLocale)} {
		if content, ok := template.Locales[locale]; ok {
			return locale, content
		}
	}
	return "", notificationmodel.NotificationTemplateContent{}
}
