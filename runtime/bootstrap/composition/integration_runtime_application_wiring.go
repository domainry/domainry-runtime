package composition

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
)

const runtimeAutomationConnectorKey = "__automation__"

type runtimePublicationDelivery struct {
	external integrationsdk.Delivery
	records  *runtimeAssembly
}

func (d runtimePublicationDelivery) Accept(ctx context.Context, request integrationsdk.DeliveryRequest) (integrationsdk.DeliveryReceipt, error) {
	if strings.TrimSpace(request.ConnectorKey) != runtimeAutomationConnectorKey {
		if d.external == nil {
			return integrationsdk.DeliveryReceipt{}, fmt.Errorf("integration delivery is unavailable")
		}
		return d.external.Accept(ctx, request)
	}
	if d.records == nil || d.records.automationApplicationService == nil {
		return integrationsdk.DeliveryReceipt{}, fmt.Errorf("automation delivery is unavailable")
	}
	payload := map[string]any{}
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		return integrationsdk.DeliveryReceipt{}, fmt.Errorf("decode automation delivery payload: %w", err)
	}
	message := publicationmodel.Message{
		ID: request.MessageID, WorkspaceID: request.WorkspaceID, ConnectorKey: request.ConnectorKey,
		ConnectionKey: request.ConnectionKey, Operation: request.Operation, DedupKey: request.DeduplicationKey,
		EventID: request.MessageID, Payload: payload,
	}
	if err := d.records.automationApplicationService.ExecuteOutboxMessage(ctx, message); err != nil {
		return integrationsdk.DeliveryReceipt{}, err
	}
	return integrationsdk.DeliveryReceipt{
		MessageID: request.MessageID, InvocationID: "automation:" + request.MessageID, Status: integrationsdk.DeliveryStatusSucceeded,
	}, nil
}

func (d runtimePublicationDelivery) Query(ctx context.Context, invocationID string) (integrationsdk.DeliveryReceipt, error) {
	if strings.HasPrefix(strings.TrimSpace(invocationID), "automation:") {
		messageID := strings.TrimPrefix(strings.TrimSpace(invocationID), "automation:")
		return integrationsdk.DeliveryReceipt{MessageID: messageID, InvocationID: invocationID, Status: integrationsdk.DeliveryStatusSucceeded}, nil
	}
	if d.external == nil {
		return integrationsdk.DeliveryReceipt{}, fmt.Errorf("integration delivery is unavailable")
	}
	return d.external.Query(ctx, invocationID)
}

func publicationHandoffApplication(records *runtimeAssembly) *publicationhandoff.PublicationHandoffApplicationService {
	if records == nil {
		return publicationhandoff.NewPublicationHandoffApplicationService(publicationhandoff.Dependencies{})
	}
	return publicationhandoff.NewPublicationHandoffApplicationService(publicationhandoff.Dependencies{
		Repository:       records.publicationRepository,
		WorkerRepository: records.integrationPublicationWorkerRepo,
		Delivery:         runtimePublicationDelivery{external: records.integrationOwnerDelivery, records: records},
		Worker:           records.workerDependencies,
		Wakeups:          records.workerWakeups,
		PreparePayload:   records.prepareOutboxPayload,
	})
}
