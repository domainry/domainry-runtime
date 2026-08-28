package projection

import (
	"html"
	"strings"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
)

func NotificationAppendPortableTextComponents(body string, facts []notificationmodel.NotificationTemplateFact, actions []notificationmodel.NotificationTemplateAction) string {
	parts := []string{strings.TrimSpace(body)}
	for _, fact := range facts {
		parts = append(parts, strings.TrimSpace(fact.Key)+": "+strings.TrimSpace(fact.Value))
	}
	for _, action := range actions {
		parts = append(parts, strings.TrimSpace(action.Label)+": "+strings.TrimSpace(action.URL))
	}
	return strings.Join(notificationNonEmptyStrings(parts), "\n")
}

func NotificationAppendPortableEmailComponents(text, htmlBody string, facts []notificationmodel.NotificationTemplateFact, actions []notificationmodel.NotificationTemplateAction) (string, string) {
	text = NotificationAppendPortableTextComponents(text, facts, actions)
	if len(facts) == 0 && len(actions) == 0 {
		return text, htmlBody
	}
	var extra strings.Builder
	if len(facts) > 0 {
		extra.WriteString(`<dl data-notification-facts="true">`)
		for _, fact := range facts {
			extra.WriteString("<dt><strong>" + html.EscapeString(fact.Key) + "</strong></dt><dd>" + html.EscapeString(fact.Value) + "</dd>")
		}
		extra.WriteString("</dl>")
	}
	if len(actions) > 0 {
		extra.WriteString(`<p data-notification-actions="true">`)
		for _, action := range actions {
			extra.WriteString(`<a href="` + html.EscapeString(action.URL) + `">` + html.EscapeString(action.Label) + `</a> `)
		}
		extra.WriteString("</p>")
	}
	if strings.TrimSpace(htmlBody) == "" {
		htmlBody = "<p>" + strings.ReplaceAll(html.EscapeString(text), "\n", "<br>") + "</p>"
	} else {
		htmlBody += extra.String()
	}
	return text, htmlBody
}

func notificationNonEmptyStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}
