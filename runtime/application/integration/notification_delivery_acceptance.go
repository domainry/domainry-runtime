package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-notification-sdk/deliverygateway"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

// AcceptNotificationDelivery is the Runtime-owned durable acceptance boundary
// used by Notification SaaS. Provider invocation always remains asynchronous.
func (s *IntegrationApplicationService) AcceptNotificationDelivery(ctx context.Context, request deliverygateway.Request, productName string) (deliverygateway.Receipt, error) {
	if s == nil || s.publicationRepo == nil {
		return deliverygateway.Receipt{}, fmt.Errorf("notification Delivery Gateway repository is unavailable")
	}
	payload, err := notificationDeliveryPayload(request.Rendered, productName)
	if err != nil {
		return deliverygateway.Receipt{}, err
	}
	if request.DeliverAfter != "" {
		payload["notification_deliver_after"] = request.DeliverAfter
	}
	if len(request.Fallbacks) > 0 {
		fallbacks := make([]any, len(request.Fallbacks))
		for index, fallback := range request.Fallbacks {
			fallbackPayload, fallbackErr := notificationDeliveryPayload(fallback.Rendered, productName)
			if fallbackErr != nil {
				return deliverygateway.Receipt{}, fallbackErr
			}
			fallbacks[index] = map[string]any{"connector_key": fallback.ConnectorKey, "connection_key": fallback.ConnectionKey, "operation": fallback.Operation, "channel": fallback.Rendered.Channel, "payload": fallbackPayload, "source_index": index}
		}
		payload["notification_fallback_plan"], payload["notification_fallback_hop"] = fallbacks, 0
	}
	fingerprint, err := notificationDeliveryFingerprint(request)
	if err != nil {
		return deliverygateway.Receipt{}, err
	}
	message, err := s.publicationRepo.InsertOutbox(ctx, request.WorkspaceID, integrationmodel.IntegrationOutboxMessage{
		WorkspaceID: request.WorkspaceID, ConnectorKey: request.ConnectorKey, ConnectionKey: request.ConnectionKey,
		Operation: request.Operation, Status: "queued", Payload: payload, EventID: request.EventID,
		RequestRef: request.RequestID, DedupKey: request.DedupeKey, RequestFingerprint: fingerprint,
		CreatedBy: "notification-saas", CreatedAt: request.CreatedAt,
	})
	if err != nil {
		return deliverygateway.Receipt{}, err
	}
	s.wakeIntegrationOutbox(message)
	return deliverygateway.Receipt{RequestID: request.RequestID, MessageID: message.ID}, nil
}

func notificationDeliveryFingerprint(request deliverygateway.Request) (string, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encode notification Delivery Gateway fingerprint: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func notificationDeliveryPayload(content contract.RenderedNotification, productName string) (map[string]any, error) {
	metadata := cloneNotificationMap(content.Metadata)
	payload := map[string]any{
		"template_key": content.TemplateKey, "template_version": content.TemplateVersion, "template_locale": content.TemplateLocale,
		"template_content_hash": content.TemplateContentHash, "variables_hash": content.VariablesHash,
		"notification_channel": content.Channel, "notification_provider": content.Provider, "notification_metadata": metadata,
	}
	if content.Channel == "email" {
		payload["to"], payload["subject"], payload["text"], payload["html"] = append([]string(nil), content.Recipients...), content.Subject, content.Text, content.HTML
		return payload, nil
	}
	if (content.Channel != "collaboration" && content.Channel != "whatsapp") || len(content.Recipients) != 1 {
		return nil, fmt.Errorf("notification delivery content has unsupported channel or recipient cardinality")
	}
	payload["recipient"], payload["message"] = content.Recipients[0], content.Message
	payload["notification_content"] = map[string]any{
		"schema_version": 1, "product_name": strings.TrimSpace(productName), "subject": content.Subject, "title": content.Title,
		"text": content.Text, "html": content.HTML, "markdown": content.Markdown, "message": content.Message,
		"metadata": metadata, "template": map[string]any{"key": content.TemplateKey, "version": content.TemplateVersion, "locale": content.TemplateLocale, "content_hash": content.TemplateContentHash, "variables_hash": content.VariablesHash},
	}
	return payload, nil
}

func cloneNotificationMap(source map[string]any) map[string]any {
	if len(source) == 0 {
		return map[string]any{}
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
