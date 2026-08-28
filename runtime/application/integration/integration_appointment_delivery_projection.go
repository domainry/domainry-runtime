package integration

import (
	"context"
	"fmt"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const (
	appointmentBookingCreatedEvent = "appointment.booking.created"
	appointmentBookingFailedEvent  = "appointment.booking.failed"
)

// emitSuccessfulAppointmentBookingEvent projects the durable outbox result into
// a credential-free business event. Project workflows can persist the provider
// event identity and meeting URL only after Runtime has fenced the outbox sent.
func (s *IntegrationApplicationService) emitSuccessfulAppointmentBookingEvent(ctx context.Context, message integrationmodel.IntegrationOutboxMessage, result OutboxSendResult, principal principalmodel.Principal) error {
	if strings.TrimSpace(message.ConnectorKey) != "appointment_scheduling" || strings.TrimSpace(message.Operation) != "enqueue_booking" {
		return nil
	}
	eventID := appointmentString(result.Response, "event_id")
	if eventID == "" {
		eventID = appointmentString(result.Response, "id")
	}
	joinURL := appointmentString(result.Response, "join_url")
	if joinURL == "" {
		joinURL = appointmentString(result.Response, "hangoutLink")
	}
	if eventID == "" || joinURL == "" {
		return fmt.Errorf("backend.integration.appointment_scheduling.delivery_receipt_invalid")
	}
	provider := strings.TrimSpace(result.Provider)
	if provider == "" {
		provider = "appointment_scheduling"
	}
	payload := appointmentProjectionPayload(message)
	payload["event_id"], payload["join_url"], payload["response_ref"] = eventID, joinURL, strings.TrimSpace(result.ResponseRef)
	_, _, err := s.RecordIntegrationEvent(ctx, integrationmodel.IntegrationEventRecordRequest{
		Provider: provider, EventType: appointmentBookingCreatedEvent,
		ExternalID: "appointment:" + strings.TrimSpace(message.ConnectionKey) + ":created:" + strings.TrimSpace(message.ID),
		Status:     "received", Payload: payload,
	}, principal)
	return err
}

func (s *IntegrationApplicationService) emitFailedAppointmentBookingEvent(ctx context.Context, message integrationmodel.IntegrationOutboxMessage, errorCode string, principal principalmodel.Principal) error {
	if strings.TrimSpace(message.ConnectorKey) != "appointment_scheduling" || strings.TrimSpace(message.Operation) != "enqueue_booking" {
		return nil
	}
	provider := appointmentProvider(message.ConnectionKey)
	payload := appointmentProjectionPayload(message)
	payload["error_code"] = strings.TrimSpace(errorCode)
	_, _, err := s.RecordIntegrationEvent(ctx, integrationmodel.IntegrationEventRecordRequest{
		Provider: provider, EventType: appointmentBookingFailedEvent,
		ExternalID: "appointment:" + strings.TrimSpace(message.ConnectionKey) + ":failed:" + strings.TrimSpace(message.ID),
		Status:     "received", Payload: payload,
	}, principal)
	return err
}

func appointmentProjectionPayload(message integrationmodel.IntegrationOutboxMessage) map[string]any {
	payload := map[string]any{
		"connection_key": message.ConnectionKey,
		"outbox_id":      message.ID,
		"start":          message.Payload["start"],
		"end":            message.Payload["end"],
		"timezone":       message.Payload["time_zone"],
	}
	if metadata, ok := message.Payload["metadata"].(map[string]any); ok {
		for _, key := range []string{"schedule_id", "process_id", "inbound_message_id"} {
			if value := metadata[key]; value != nil {
				payload[key] = value
			}
		}
	}
	return payload
}

func appointmentString(values map[string]any, key string) string {
	if values == nil || values[key] == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(values[key]))
}

func appointmentProvider(connectionKey string) string {
	connectionKey = strings.ToLower(strings.TrimSpace(connectionKey))
	switch {
	case strings.Contains(connectionKey, "google"):
		return "google_calendar"
	case strings.Contains(connectionKey, "feishu"):
		return "feishu_calendar"
	default:
		return "appointment_scheduling"
	}
}
