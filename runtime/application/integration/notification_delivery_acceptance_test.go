package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-notification-sdk/deliverygateway"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
)

type notificationDeliveryRepositoryStub struct {
	integrationrepository.IntegrationDeliveryRepository
	stored integrationmodel.IntegrationOutboxMessage
}

func (r *notificationDeliveryRepositoryStub) InsertOutbox(_ context.Context, _ string, value integrationmodel.IntegrationOutboxMessage) (integrationmodel.IntegrationOutboxMessage, error) {
	if r.stored.ID != "" {
		if r.stored.RequestRef == value.RequestRef && r.stored.RequestFingerprint == value.RequestFingerprint {
			return r.stored, nil
		}
		return integrationmodel.IntegrationOutboxMessage{}, errors.New("idempotency conflict")
	}
	value.ID = "integration-message"
	r.stored = value
	return value, nil
}

func TestAcceptNotificationDeliveryPersistsStableRequestIdentity(t *testing.T) {
	repository := &notificationDeliveryRepositoryStub{}
	service := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: repository})
	request := deliverygateway.Request{
		RequestID: "request", WorkspaceID: "workspace", PlanID: "plan", EventID: "event", Channel: "email", ConnectorKey: "smtp", Operation: "send", DedupeKey: "dedupe", CreatedAt: "2026-08-28T00:00:00Z",
		Rendered: contract.RenderedNotification{Channel: "email", Recipients: []string{"user@example.com"}, Subject: "Ready", Text: "Ready"},
	}
	first, err := service.AcceptNotificationDelivery(t.Context(), request, "Product")
	if err != nil || first.RequestID != request.RequestID || first.MessageID != "integration-message" {
		t.Fatalf("receipt=%+v err=%v", first, err)
	}
	second, err := service.AcceptNotificationDelivery(t.Context(), request, "Product")
	if err != nil || second != first {
		t.Fatalf("duplicate receipt=%+v err=%v", second, err)
	}
	if repository.stored.RequestRef != request.RequestID || repository.stored.RequestFingerprint == "" || repository.stored.CreatedBy != "notification-saas" {
		t.Fatalf("stored=%+v", repository.stored)
	}
	request.Rendered.Subject = "Changed"
	if _, err := service.AcceptNotificationDelivery(t.Context(), request, "Product"); err == nil {
		t.Fatal("conflicting Delivery Gateway retry was accepted")
	}
}
