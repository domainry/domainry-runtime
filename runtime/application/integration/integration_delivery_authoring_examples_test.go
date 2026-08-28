package integration

import (
	"encoding/json"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestIntegrationDeliveryAuthoringMutationExamplesExecuteOwnerServices(t *testing.T) {
	definitions := map[string]capabilitycontract.CapabilityAuthoringDefinition{}
	for _, definition := range integrationcontract.IntegrationDeliveryAuthoringCapabilities() {
		definitions[definition.Key] = definition
	}

	t.Run("enqueue", func(t *testing.T) {
		delivery := &integrationManagementDeliveryRepo{}
		registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "webhook"}}})
		service := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: delivery, Registry: registry})
		principal := integrationManagementPrincipal(PermissionInvoke)
		for _, example := range definitions["integration.outbox.enqueue"].Examples {
			request := decodeIntegrationAuthoringExample[integrationmodel.IntegrationOutboxEnqueueRequest](t, example.Value)
			_, err := service.EnqueueIntegrationOutboxMessage(t.Context(), request, principal)
			assertIntegrationAuthoringExampleError(t, example, err)
		}
	})

	t.Run("status", func(t *testing.T) {
		delivery := &integrationManagementDeliveryRepo{}
		service := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: delivery})
		principal := integrationManagementPrincipal(PermissionInvoke)
		for _, example := range definitions["integration.outbox.status"].Examples {
			request := decodeIntegrationAuthoringExample[integrationmodel.IntegrationOutboxStatusRequest](t, example.Value)
			messageID, _ := example.Value["message_id"].(string)
			_, err := service.UpdateIntegrationOutboxStatus(t.Context(), messageID, request, principal)
			assertIntegrationAuthoringExampleError(t, example, err)
		}
	})

	t.Run("retry", func(t *testing.T) {
		delivery := &integrationManagementDeliveryRepo{found: true}
		service := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: delivery})
		principal := integrationManagementPrincipal(PermissionRetry)
		for _, example := range definitions["integration.outbox.retry"].Examples {
			messageID, _ := example.Value["message_id"].(string)
			delivery.outboxes = []integrationmodel.IntegrationOutboxMessage{{ID: messageID, ConnectorKey: "__automation__", Status: "failed"}}
			if len(example.ExpectedErrorCodes) > 0 {
				delivery.outboxes[0].Status = "quarantined"
			}
			request := decodeIntegrationAuthoringExample[integrationmodel.IntegrationOutboxRetryRequest](t, example.Value)
			_, err := service.ScheduleIntegrationOutboxRetry(t.Context(), messageID, request, principal)
			assertIntegrationAuthoringExampleError(t, example, err)
		}
	})
}

func decodeIntegrationAuthoringExample[T any](t *testing.T, value map[string]any) T {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result T
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertIntegrationAuthoringExampleError(t *testing.T, example capabilitycontract.CapabilityAuthoringExample, err error) {
	t.Helper()
	if len(example.ExpectedErrorCodes) == 0 {
		if err != nil {
			t.Fatalf("example=%#v err=%v", example.Value, err)
		}
		return
	}
	for _, code := range example.ExpectedErrorCodes {
		if apperror.CodeOf(err) == code {
			return
		}
	}
	t.Fatalf("example=%#v err=%v expected=%v", example.Value, err, example.ExpectedErrorCodes)
}
