package contract

import notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"

// NotificationInboxActionRegistry is compiled from published Event Type
// definitions. Runtime core does not own a hard-coded list of business actions.
type NotificationInboxActionRegistry interface {
	ResolveNotificationInboxAction(string) (notificationmodel.NotificationInboxActionDescriptor, bool)
}

type NotificationEventTypeRegistry interface {
	ResolveNotificationEventType(string) (notificationmodel.NotificationEventType, bool)
}

type NotificationRuleRegistry interface {
	ResolveNotificationRule(string) (notificationmodel.NotificationRule, bool)
}
