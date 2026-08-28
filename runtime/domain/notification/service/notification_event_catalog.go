package service

import (
	"sort"
	"strings"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	notificationvalidation "github.com/domainry/domainry-runtime/runtime/domain/notification/validation"
)

// NotificationEventCatalog is Plane's manifest-derived governance view. Inbox
// execution uses the corresponding immutable catalog in domainry-notification.
type NotificationEventCatalog struct {
	eventTypes map[string]notificationmodel.NotificationEventType
	actions    map[string]notificationmodel.NotificationInboxActionDescriptor
	rules      map[string]notificationmodel.NotificationRule
}

func (r *NotificationEventCatalog) GovernanceCatalog() notificationmodel.NotificationGovernanceCatalog {
	result := notificationmodel.NotificationGovernanceCatalog{EventTypes: []notificationmodel.NotificationEventType{}, Rules: []notificationmodel.NotificationRule{}}
	if r == nil {
		return result
	}
	for _, value := range r.eventTypes {
		result.EventTypes = append(result.EventTypes, value)
	}
	for _, value := range r.rules {
		result.Rules = append(result.Rules, value)
	}
	sort.Slice(result.EventTypes, func(i, j int) bool { return result.EventTypes[i].Key < result.EventTypes[j].Key })
	sort.Slice(result.Rules, func(i, j int) bool { return result.Rules[i].EventTypeKey < result.Rules[j].EventTypeKey })
	return result
}

func NewNotificationInboxActionRegistry(descriptors []notificationmodel.NotificationInboxActionDescriptor) *NotificationEventCatalog {
	registry := &NotificationEventCatalog{eventTypes: map[string]notificationmodel.NotificationEventType{}, actions: map[string]notificationmodel.NotificationInboxActionDescriptor{}, rules: map[string]notificationmodel.NotificationRule{}}
	for _, descriptor := range descriptors {
		descriptor.Key, descriptor.Kind, descriptor.ResourceType = strings.TrimSpace(descriptor.Key), strings.TrimSpace(descriptor.Kind), strings.TrimSpace(descriptor.ResourceType)
		if descriptor.Key != "" {
			registry.actions[descriptor.Key] = descriptor
		}
	}
	return registry
}

func NewNotificationEventCatalog(eventTypes []notificationmodel.NotificationEventType) (*NotificationEventCatalog, error) {
	return NewNotificationEventCatalogWithRules(eventTypes, nil)
}

func NewNotificationEventCatalogWithRules(eventTypes []notificationmodel.NotificationEventType, rules []notificationmodel.NotificationRule) (*NotificationEventCatalog, error) {
	if err := notificationvalidation.NotificationValidateEventTypes(eventTypes, rules); err != nil {
		return nil, err
	}
	catalog := &NotificationEventCatalog{eventTypes: map[string]notificationmodel.NotificationEventType{}, actions: map[string]notificationmodel.NotificationInboxActionDescriptor{}, rules: map[string]notificationmodel.NotificationRule{}}
	for _, eventType := range eventTypes {
		validated, _ := notificationvalidation.NotificationValidateEventType(eventType)
		catalog.eventTypes[validated.Key] = validated
		for _, descriptor := range validated.Actions {
			if current, exists := catalog.actions[descriptor.Key]; exists && (current.Kind != descriptor.Kind || current.ResourceType != descriptor.ResourceType) {
				return nil, notificationBadRequest("backend.notification.inbox_action_contract_conflict", "action_key", descriptor.Key)
			}
			catalog.actions[descriptor.Key] = descriptor
		}
	}
	for _, rule := range rules {
		catalog.rules[strings.TrimSpace(rule.EventTypeKey)] = rule
	}
	return catalog, nil
}

func (r *NotificationEventCatalog) ResolveNotificationInboxAction(key string) (notificationmodel.NotificationInboxActionDescriptor, bool) {
	if r == nil {
		return notificationmodel.NotificationInboxActionDescriptor{}, false
	}
	value, found := r.actions[strings.TrimSpace(key)]
	return value, found
}

func (r *NotificationEventCatalog) ResolveNotificationEventType(key string) (notificationmodel.NotificationEventType, bool) {
	if r == nil {
		return notificationmodel.NotificationEventType{}, false
	}
	value, found := r.eventTypes[strings.TrimSpace(key)]
	return value, found
}

func (r *NotificationEventCatalog) ResolveNotificationRule(key string) (notificationmodel.NotificationRule, bool) {
	if r == nil {
		return notificationmodel.NotificationRule{}, false
	}
	value, found := r.rules[strings.TrimSpace(key)]
	return value, found
}
