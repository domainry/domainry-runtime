package validation

import (
	"fmt"
	"strings"

	notificationcontract "github.com/domainry/domainry-notification-sdk/contract"
)

// ValidateNotificationAudienceResolverReferences closes the owner/host seam:
// Notification validates catalog syntax, while Runtime must prove that every
// referenced resolver has a mounted host implementation before publication or
// startup.
func ValidateNotificationAudienceResolverReferences(eventTypes []notificationcontract.NotificationEventType, rules []notificationcontract.NotificationRule, resolverKeys []string) error {
	supported := make(map[string]bool, len(resolverKeys))
	for index, raw := range resolverKeys {
		key := strings.TrimSpace(raw)
		if key == "" || supported[key] {
			return fmt.Errorf("notification audience resolver inventory contains blank or duplicate key at index %d", index)
		}
		supported[key] = true
	}
	for eventIndex, eventType := range eventTypes {
		if err := validateNotificationAudienceResolverList(eventType.AudienceResolvers, supported); err != nil {
			return fmt.Errorf("notification_event_types[%d].audience_resolvers: %w", eventIndex, err)
		}
	}
	for ruleIndex, rule := range rules {
		if err := validateNotificationAudienceResolverList(rule.AudienceResolvers, supported); err != nil {
			return fmt.Errorf("notification_rules[%d].audience_resolvers: %w", ruleIndex, err)
		}
	}
	return nil
}

func validateNotificationAudienceResolverList(values []string, supported map[string]bool) error {
	seen := make(map[string]bool, len(values))
	for index, raw := range values {
		key := strings.TrimSpace(raw)
		if key == "" {
			return fmt.Errorf("resolver at index %d is blank", index)
		}
		if seen[key] {
			return fmt.Errorf("resolver %q is duplicated", key)
		}
		if !supported[key] {
			return fmt.Errorf("resolver %q is not implemented by the Runtime host", key)
		}
		seen[key] = true
	}
	return nil
}
