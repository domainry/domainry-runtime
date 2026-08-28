package notification

import (
	"context"
	"fmt"

	sourcetemplate "github.com/domainry/domainry-notification/template"
	notificationcontract "github.com/domainry/domainry-runtime/runtime/domain/notification/contract"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
)

// NotificationTemplateRenderer exposes the module Engine to Plane callers
// that still consume the Runtime rendering contract. The module Engine remains
// the only mutable published-template catalog.
type NotificationTemplateRenderer struct{ engine *sourcetemplate.Engine }

func NewNotificationTemplateRenderer(engine *sourcetemplate.Engine) (*NotificationTemplateRenderer, error) {
	if engine == nil {
		return nil, fmt.Errorf("notification template engine is required")
	}
	return &NotificationTemplateRenderer{engine: engine}, nil
}

func (r *NotificationTemplateRenderer) Render(ctx context.Context, request notificationcontract.NotificationRenderRequest) (notificationmodel.RenderedNotification, error) {
	value, err := r.engine.Render(ctx, sourcetemplate.RenderRequest{
		TemplateKey: request.TemplateKey, Locale: request.Locale, Recipients: moduleUserIDs(request.Recipients),
		Variables: cloneModuleVariables(request.Variables), Metadata: cloneModuleVariables(request.Metadata),
	})
	return planeRenderedNotification(value), mapNotificationModuleError(err)
}
