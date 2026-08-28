package notification

import (
	"context"
	"fmt"
	"strings"

	sourcedelivery "github.com/domainry/domainry-notification/delivery"
	sourcetemplate "github.com/domainry/domainry-notification/template"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
)

// NotificationOutboxDispatcher is the Plane-owned boundary between portable
// notification delivery and the Integration Outbox. Provider-native request
// compilation happens later inside the selected Connector provider.
type NotificationOutboxDispatcher struct {
	outbox      integrationrepository.IntegrationDeliveryRepository
	productName string
	wakeup      func(integrationmodel.IntegrationOutboxMessage)
}

func NewNotificationOutboxDispatcher(outbox integrationrepository.IntegrationDeliveryRepository, productName string, wakeup func(integrationmodel.IntegrationOutboxMessage)) (*NotificationOutboxDispatcher, error) {
	if outbox == nil || strings.TrimSpace(productName) == "" {
		return nil, fmt.Errorf("notification outbox dispatcher dependencies are required")
	}
	return &NotificationOutboxDispatcher{outbox: outbox, productName: strings.TrimSpace(productName), wakeup: wakeup}, nil
}

func (d *NotificationOutboxDispatcher) BindWakeup(wakeup func(integrationmodel.IntegrationOutboxMessage)) {
	if d != nil {
		d.wakeup = wakeup
	}
}

func (d *NotificationOutboxDispatcher) Dispatch(ctx context.Context, request sourcedelivery.DispatchRequest) (sourcedelivery.DispatchReceipt, error) {
	payload, err := d.outboxPayload(request.Content)
	if err != nil {
		return sourcedelivery.DispatchReceipt{}, err
	}
	if request.Decision.DeliverAfter != "" {
		payload["notification_deliver_after"] = request.Decision.DeliverAfter
	}
	if len(request.Fallbacks) > 0 {
		fallbacks := make([]any, 0, len(request.Fallbacks))
		for index, fallback := range request.Fallbacks {
			fallbackPayload, fallbackErr := d.outboxPayload(fallback.Content)
			if fallbackErr != nil {
				return sourcedelivery.DispatchReceipt{}, fallbackErr
			}
			fallbacks = append(fallbacks, map[string]any{
				"connector_key": fallback.ConnectorKey, "connection_key": fallback.ConnectionKey, "operation": fallback.Operation,
				"channel": fallback.Content.Channel, "payload": fallbackPayload, "source_index": index,
			})
		}
		payload["notification_fallback_plan"], payload["notification_fallback_hop"] = fallbacks, 0
	}
	message, err := d.outbox.InsertOutbox(ctx, request.WorkspaceID.String(), integrationmodel.IntegrationOutboxMessage{
		WorkspaceID: request.WorkspaceID.String(), ConnectorKey: request.ConnectorKey, ConnectionKey: request.ConnectionKey,
		Operation: request.Operation, Status: "queued", Payload: payload, EventID: request.EventID,
		DedupKey: request.DeduplicationKey, CreatedBy: "notification", CreatedAt: request.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	})
	if err != nil {
		return sourcedelivery.DispatchReceipt{}, err
	}
	if d.wakeup != nil {
		d.wakeup(message)
	}
	return sourcedelivery.DispatchReceipt{MessageID: message.ID, AcceptedAt: request.CreatedAt}, nil
}

func (d *NotificationOutboxDispatcher) outboxPayload(content sourcetemplate.Rendered) (map[string]any, error) {
	metadata := cloneModuleVariables(content.Metadata)
	common := map[string]any{
		"template_key": content.TemplateKey, "template_version": content.TemplateVersion, "template_locale": content.TemplateLocale,
		"template_content_hash": content.TemplateContentHash, "variables_hash": content.VariablesHash,
		"notification_channel": content.Channel, "notification_provider": content.Provider, "notification_metadata": metadata,
	}
	if content.Channel == "email" {
		common["to"], common["subject"], common["text"], common["html"] = append([]string(nil), content.Recipients...), content.Subject, content.Text, content.HTML
		return common, nil
	}
	if (content.Channel != "collaboration" && content.Channel != "whatsapp") || len(content.Recipients) != 1 {
		return nil, fmt.Errorf("notification delivery content has unsupported channel or recipient cardinality")
	}
	common["recipient"], common["message"] = content.Recipients[0], content.Message
	common["notification_content"] = portableNotificationContent(content, d.productName)
	return common, nil
}

func portableNotificationContent(content sourcetemplate.Rendered, productName string) map[string]any {
	facts := make([]any, len(content.Facts))
	for index, fact := range content.Facts {
		facts[index] = map[string]any{"key": fact.Key, "value": fact.Value}
	}
	actions := make([]any, len(content.Actions))
	for index, action := range content.Actions {
		actions[index] = map[string]any{"label": action.Label, "url": action.URL, "style": action.Style}
	}
	value := map[string]any{
		"schema_version": 1, "product_name": productName, "subject": content.Subject, "title": content.Title,
		"text": content.Text, "html": content.HTML, "markdown": content.Markdown, "message": content.Message,
		"facts": facts, "actions": actions, "metadata": cloneModuleVariables(content.Metadata),
		"template": map[string]any{"key": content.TemplateKey, "version": content.TemplateVersion, "locale": content.TemplateLocale, "content_hash": content.TemplateContentHash, "variables_hash": content.VariablesHash},
	}
	if content.ProviderTemplate != nil {
		components := make([]any, len(content.ProviderTemplate.Components))
		for index, component := range content.ProviderTemplate.Components {
			components[index] = map[string]any{"type": component.Type, "sub_type": component.SubType, "index": component.Index, "parameters": append([]string(nil), component.Parameters...)}
		}
		value["provider_template"] = map[string]any{"name": content.ProviderTemplate.Name, "language": content.ProviderTemplate.Language, "components": components}
	}
	return value
}

var _ sourcedelivery.Dispatcher = (*NotificationOutboxDispatcher)(nil)
