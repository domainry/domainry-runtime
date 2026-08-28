package validation

import (
	"fmt"
	"strings"
	"unicode"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
)

func NotificationValidateEventTypes(values []notificationmodel.NotificationEventType, rules []notificationmodel.NotificationRule) error {
	seen := map[string]bool{}
	for index, value := range values {
		validated, err := NotificationValidateEventType(value)
		if err != nil {
			return fmt.Errorf("notification_event_types[%d]: %w", index, err)
		}
		if seen[validated.Key] {
			return notificationBadRequest("backend.notification.event_type_duplicate", "event_type", validated.Key)
		}
		seen[validated.Key] = true
	}
	for index, rule := range rules {
		key := strings.TrimSpace(rule.EventTypeKey)
		if key == "" || !seen[key] || rule.DedupeWindowSeconds < 0 || rule.AggregationWindowSeconds < 0 || rule.ReminderIntervalSeconds < 0 || rule.MaximumReminders < 0 {
			return notificationBadRequest("backend.notification.rule_invalid", "rule_index", fmt.Sprint(index))
		}
		if rule.RecoveryEventTypeKey != "" && !seen[strings.TrimSpace(rule.RecoveryEventTypeKey)] {
			return notificationBadRequest("backend.notification.rule_recovery_event_invalid", "event_type", key)
		}
		seenChannels := map[string]bool{}
		for _, channel := range rule.Channels {
			channelKey := strings.TrimSpace(channel.Channel)
			channelIdentity := fmt.Sprintf("%s:%d", channelKey, channel.EscalationStep)
			if seenChannels[channelIdentity] || channel.DelaySeconds < 0 || channel.EscalationStep < 0 || channel.DigestWindowSeconds < 0 || channel.DigestMaximumItems < 0 {
				return notificationBadRequest("backend.notification.rule_channel_duplicate", "event_type", key, "channel", channelKey)
			}
			seenChannels[channelIdentity] = true
			deliveryMode := strings.TrimSpace(channel.DeliveryMode)
			if deliveryMode == "" {
				deliveryMode = "immediate"
			}
			if deliveryMode != "immediate" && deliveryMode != "digest" {
				return notificationBadRequest("backend.notification.rule_channel_delivery_mode_invalid", "event_type", key, "channel", channelKey)
			}
			if deliveryMode == "digest" && (channel.DigestWindowSeconds <= 0 || channel.DelaySeconds != 0 || channel.EscalationStep != 0) {
				return notificationBadRequest("backend.notification.rule_channel_digest_invalid", "event_type", key, "channel", channelKey)
			}
			switch channelKey {
			case "in_app":
				if strings.TrimSpace(channel.TemplateKey) != "" || strings.TrimSpace(channel.ConnectorKey) != "" || strings.TrimSpace(channel.Operation) != "" || channel.DelaySeconds != 0 || channel.EscalationStep != 0 || deliveryMode != "immediate" || channel.DigestWindowSeconds != 0 {
					return notificationBadRequest("backend.notification.rule_in_app_channel_invalid", "event_type", key)
				}
			case "email", "collaboration", "whatsapp":
				if !stableTemplateKeyPattern.MatchString(strings.TrimSpace(channel.TemplateKey)) || strings.TrimSpace(channel.ConnectorKey) == "" || strings.TrimSpace(channel.Operation) == "" {
					return notificationBadRequest("backend.notification.rule_external_channel_invalid", "event_type", key, "channel", channelKey)
				}
			default:
				return notificationBadRequest("backend.notification.rule_channel_unsupported", "event_type", key, "channel", channelKey)
			}
		}
	}
	return nil
}

func NotificationValidateEventType(value notificationmodel.NotificationEventType) (notificationmodel.NotificationEventType, error) {
	value.Key, value.Source, value.Category = strings.TrimSpace(value.Key), strings.TrimSpace(value.Source), strings.TrimSpace(value.Category)
	value.DefaultSeverity, value.TemplateKey, value.Status = strings.TrimSpace(value.DefaultSeverity), strings.TrimSpace(value.TemplateKey), strings.TrimSpace(value.Status)
	value.DefaultLocale = strings.TrimSpace(value.DefaultLocale)
	if !stableTemplateKeyPattern.MatchString(value.Key) || !stableTemplateKeyPattern.MatchString(value.Source) || !stableTemplateKeyPattern.MatchString(value.Category) || !stableTemplateKeyPattern.MatchString(value.TemplateKey) || value.DefaultLocale == "" || len(value.Locales) == 0 || value.Version < 1 || value.Status != "published" {
		return value, notificationBadRequest("backend.notification.event_type_invalid", "event_type", value.Key)
	}
	if _, ok := value.Locales[value.DefaultLocale]; !ok {
		return value, notificationBadRequest("backend.notification.event_type_locale_invalid", "event_type", value.Key)
	}
	if !notificationInboxSeverities[value.DefaultSeverity] {
		return value, notificationBadRequest("backend.notification.event_type_severity_invalid", "event_type", value.Key)
	}
	if len(value.Surfaces) == 0 {
		return value, notificationBadRequest("backend.notification.event_type_surface_invalid", "event_type", value.Key)
	}
	variableKeys := map[string]bool{}
	for index, variable := range value.Variables {
		variable.Key, variable.Type = strings.TrimSpace(variable.Key), strings.TrimSpace(variable.Type)
		if !stableTemplateKeyPattern.MatchString(variable.Key) || variableKeys[variable.Key] {
			return value, notificationBadRequest("backend.notification.event_type_variable_invalid", "event_type", value.Key, "variable", variable.Key)
		}
		if !notificationInboxVariableTypes[variable.Type] || notificationInboxSensitiveVariableKey(variable.Key) {
			return value, notificationBadRequest("backend.notification.event_type_variable_unsafe", "event_type", value.Key, "variable", variable.Key)
		}
		variableKeys[variable.Key] = true
		value.Variables[index] = variable
	}
	for _, surface := range value.Surfaces {
		if !notificationInboxSurfaces[strings.TrimSpace(surface)] {
			return value, notificationBadRequest("backend.notification.event_type_surface_invalid", "event_type", value.Key)
		}
	}
	for actionIndex, descriptor := range value.Actions {
		descriptor.Key, descriptor.Kind, descriptor.ResourceType = strings.TrimSpace(descriptor.Key), strings.TrimSpace(descriptor.Kind), strings.TrimSpace(descriptor.ResourceType)
		if !stableTemplateKeyPattern.MatchString(descriptor.Key) || (descriptor.Kind != "route" && descriptor.Kind != "business_action") || !stableTemplateKeyPattern.MatchString(descriptor.ResourceType) || len(descriptor.SurfaceRoutes) == 0 {
			return value, notificationBadRequest("backend.notification.event_type_action_invalid", "event_type", value.Key)
		}
		for surface, routeKey := range descriptor.SurfaceRoutes {
			if !notificationInboxSurfaces[strings.TrimSpace(surface)] || !stableTemplateKeyPattern.MatchString(strings.TrimSpace(routeKey)) {
				return value, notificationBadRequest("backend.notification.event_type_action_route_invalid", "action_key", descriptor.Key)
			}
		}
		value.Actions[actionIndex] = descriptor
		for locale, content := range value.Locales {
			if strings.TrimSpace(content.ActionLabels[descriptor.Key]) == "" {
				return value, notificationBadRequest("backend.notification.event_type_action_label_missing", "event_type", value.Key, "locale", locale, "action_key", descriptor.Key)
			}
		}
	}
	for locale, content := range value.Locales {
		if strings.TrimSpace(locale) == "" || strings.TrimSpace(content.Title) == "" || strings.TrimSpace(content.Body) == "" {
			return value, notificationBadRequest("backend.notification.event_type_content_invalid", "event_type", value.Key)
		}
		for _, source := range append([]string{content.Title, content.Body}, notificationInboxContentFragments(content)...) {
			for _, match := range notificationRenderTokenPattern.FindAllStringSubmatch(source, -1) {
				// notificationRenderTokenPattern has one fixed capture group, so every
				// match returned by FindAllStringSubmatch contains the variable key.
				if !variableKeys[match[1]] {
					return value, notificationBadRequest("backend.notification.event_type_variable_unknown", "event_type", value.Key, "locale", locale)
				}
			}
		}
	}
	return value, nil
}

func notificationInboxContentFragments(content notificationmodel.NotificationInboxEventTypeContent) []string {
	result := make([]string, 0, len(content.Facts)*2+len(content.ActionLabels))
	for _, fact := range content.Facts {
		result = append(result, fact.Key, fact.Value)
	}
	for _, label := range content.ActionLabels {
		result = append(result, label)
	}
	return result
}

var notificationInboxVariableTypes = map[string]bool{
	"boolean": true, "date": true, "datetime": true, "email": true,
	"number": true, "string": true, "text": true,
}

func notificationInboxSensitiveVariableKey(key string) bool {
	parts := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(key)), func(value rune) bool {
		return value == '.' || value == '_' || value == '-' || unicode.IsSpace(value)
	})
	joined := strings.Join(parts, "_")
	for _, forbidden := range []string{
		"secret", "password", "authorization", "cookie", "token", "payload", "stack", "stacktrace", "exception", "dsn",
		"request_body", "response_body", "raw_request", "raw_response", "error_message", "error_detail",
	} {
		if joined == forbidden || strings.Contains(joined, "_"+forbidden+"_") || strings.HasPrefix(joined, forbidden+"_") || strings.HasSuffix(joined, "_"+forbidden) {
			return true
		}
	}
	return false
}
