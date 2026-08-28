package integration

import (
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestAppointmentDeliveryProjection(t *testing.T) {
	events := &integrationManagementEventRepo{}
	service := NewIntegrationApplicationService(ApplicationDependencies{EventRepository: events})
	message := integrationmodel.IntegrationOutboxMessage{
		ID: "outbox-one", WorkspaceID: "workspace", ConnectorKey: "appointment_scheduling", ConnectionKey: "google_calendar_primary", Operation: "enqueue_booking",
		Payload: map[string]any{"start": "2026-08-10T10:00:00+08:00", "end": "2026-08-10T11:00:00+08:00", "time_zone": "Asia/Shanghai", "metadata": map[string]any{"schedule_id": "schedule-1", "process_id": "candidate-1", "inbound_message_id": "message-1"}},
	}
	principal := integrationManagementPrincipal(PermissionInvoke)
	if err := service.emitSuccessfulAppointmentBookingEvent(t.Context(), message, OutboxSendResult{Provider: "google_calendar", ResponseRef: "google:event-1", Response: map[string]any{"id": "event-1", "hangoutLink": "https://meet.google.com/abc"}}, principal); err != nil {
		t.Fatal(err)
	}
	if events.event.EventType != appointmentBookingCreatedEvent || events.event.Provider != "google_calendar" || events.event.Payload["schedule_id"] != "schedule-1" || events.event.Payload["event_id"] != "event-1" || events.event.Payload["join_url"] != "https://meet.google.com/abc" {
		t.Fatalf("event=%#v", events.event)
	}
	if err := service.emitSuccessfulAppointmentBookingEvent(t.Context(), message, OutboxSendResult{Response: map[string]any{"id": "event-1"}}, principal); err == nil {
		t.Fatal("missing meeting URL accepted")
	}
	if err := service.emitSuccessfulAppointmentBookingEvent(t.Context(), message, OutboxSendResult{ResponseRef: "event", Response: map[string]any{"event_id": "event-2", "join_url": "https://meeting"}}, principal); err != nil || events.event.Provider != "appointment_scheduling" {
		t.Fatalf("default provider event=%#v err=%v", events.event, err)
	}
	if err := service.emitFailedAppointmentBookingEvent(t.Context(), message, "backend.integration.provider.failed", principal); err != nil {
		t.Fatal(err)
	}
	if events.event.EventType != appointmentBookingFailedEvent || events.event.Payload["error_code"] != "backend.integration.provider.failed" {
		t.Fatalf("failed event=%#v", events.event)
	}
	wrongOperation := message
	wrongOperation.Operation = "other"
	if err := service.emitSuccessfulAppointmentBookingEvent(t.Context(), wrongOperation, OutboxSendResult{}, principal); err != nil {
		t.Fatalf("unrelated successful operation=%v", err)
	}
	if err := service.emitFailedAppointmentBookingEvent(t.Context(), wrongOperation, "failure", principal); err != nil {
		t.Fatalf("unrelated failed operation=%v", err)
	}
	withoutMetadata := message
	withoutMetadata.Payload = map[string]any{"metadata": "invalid"}
	if payload := appointmentProjectionPayload(withoutMetadata); payload["schedule_id"] != nil {
		t.Fatalf("invalid metadata projected=%#v", payload)
	}
	nilMetadataValue := message
	nilMetadataValue.Payload = map[string]any{"metadata": map[string]any{"schedule_id": nil}}
	if payload := appointmentProjectionPayload(nilMetadataValue); payload["schedule_id"] != nil {
		t.Fatalf("nil metadata projected=%#v", payload)
	}
	if appointmentString(map[string]any{}, "missing") != "" {
		t.Fatal("missing appointment string was not empty")
	}
	if appointmentString(nil, "missing") != "" {
		t.Fatal("nil appointment values were not empty")
	}
	if appointmentProvider("feishu-primary") != "feishu_calendar" || appointmentProvider("custom") != "appointment_scheduling" {
		t.Fatal("appointment provider mapping changed")
	}
}
