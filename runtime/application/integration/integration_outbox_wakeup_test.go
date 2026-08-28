package integration

import (
	"context"
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestEnqueueIntegrationOutboxWakesOnlyAfterSuccessfulInsert(t *testing.T) {
	repository := &integrationManagementDeliveryRepo{}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "connector"}}})
	service := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: repository, Registry: registry})
	principal := integrationManagementPrincipal(PermissionInvoke)
	request := integrationmodel.IntegrationOutboxEnqueueRequest{ConnectorKey: "connector", Operation: "send", RequestRef: "request"}
	message, err := service.EnqueueIntegrationOutboxMessage(t.Context(), request, principal)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case locator := <-IntegrationOutboxWakeups(service):
		if locator.WorkspaceID != principal.WorkspaceID || locator.TaskID != message.ID {
			t.Fatalf("locator=%#v", locator)
		}
	default:
		t.Fatal("committed outbox message did not wake the worker")
	}
	repository.err = errIntegrationManagementTest
	request.RequestRef = "failed"
	if _, err := service.EnqueueIntegrationOutboxMessage(t.Context(), request, principal); err == nil {
		t.Fatal("expected insert failure")
	}
	select {
	case locator := <-IntegrationOutboxWakeups(service):
		t.Fatalf("failed insert emitted wakeup=%#v", locator)
	default:
	}
}

func TestProcessIntegrationOutboxTargetsExactCommittedMessage(t *testing.T) {
	message := integrationOutboxWorkerMessage()
	worker := &integrationOutboxWorkerEdgeRepository{claim: message, claimOK: true}
	sender := integrationOutboxSenderFunc(func(context.Context, integrationmodel.IntegrationOutboxMessage, principalmodel.Principal) (OutboxSendResult, error) {
		return OutboxSendResult{Status: "sent"}, nil
	})
	service := integrationOutboxWorkerService(worker, sender)
	service.deliveryRepo = &integrationManagementDeliveryRepo{outboxes: []integrationmodel.IntegrationOutboxMessage{message}, found: true}
	result, err := service.ProcessIntegrationOutbox(t.Context(), IntegrationOutboxLocator{WorkspaceID: message.WorkspaceID, MessageID: message.ID})
	if err != nil || result.Sent != 1 || worker.lastStatus != "sent" {
		t.Fatalf("result=%#v status=%q err=%v", result, worker.lastStatus, err)
	}
}
