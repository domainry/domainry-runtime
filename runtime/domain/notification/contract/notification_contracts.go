package contract

import (
	"context"
	identitysdk "github.com/domainry/domainry-identity-sdk"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
)

type NotificationRecipientDirectory interface {
	FindUser(context.Context, string) (identitysdk.User, bool, error)
}

type NotificationRenderRequest struct {
	TemplateKey string
	Locale      string
	Recipients  []string
	Variables   map[string]any
	Metadata    map[string]any
}

type NotificationRenderer interface {
	Render(context.Context, NotificationRenderRequest) (notificationmodel.RenderedNotification, error)
}
