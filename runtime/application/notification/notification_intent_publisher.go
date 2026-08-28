package notification

import (
	"context"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// NotificationIntentPublisher is the small cross-owner Application contract.
// Producers depend on this intent boundary rather than Inbox persistence.
type NotificationIntentPublisher interface {
	PublishInboxIntent(context.Context, notificationmodel.NotificationIntent, principalmodel.SystemScope) (notificationmodel.NotificationEvent, bool, error)
}
