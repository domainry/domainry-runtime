package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	connector "github.com/domainry/domainry-connector-sdk"
)

type notificationEmulatorInput struct {
	SubscriptionID string         `json:"subscription_id"`
	Endpoint       string         `json:"endpoint,omitempty"`
	P256DH         string         `json:"p256dh,omitempty"`
	Auth           string         `json:"auth,omitempty"`
	Payload        map[string]any `json:"payload"`
	EmulatorStatus string         `json:"emulator_status,omitempty"`
	Telemetry      map[string]any `json:"_telemetry,omitempty"`
}

var notificationEmulatorSend = connector.EnqueueOperation[notificationEmulatorInput]{
	ConnectorKey: "notification", ProviderKey: "deterministic_emulator", Key: "send",
	ContractSHA256: "9279b82133298849994dcb257c632bad4f5979f061fd668a8dd6c7701c4a17e9",
	Reliability:    connector.ReliabilityContract{Effect: connector.EffectWrite, Idempotency: connector.IdempotencyContract{Strategy: connector.IdempotencyNone}, Reconciliation: connector.ReconciliationNone, Compensation: connector.CompensationContract{Mode: connector.CompensationNone}},
}

var notificationEmulatorTest = connector.CallOperation[struct{}, map[string]any]{
	ConnectorKey: "notification", ProviderKey: "deterministic_emulator", Key: "test_connection",
	ContractSHA256: "d66a0f8d3be976ecbc3125daa0c3aea1231c4858104fe87ebbdd5907dd9ced5f",
	Reliability:    connector.ReliabilityContract{Effect: connector.EffectRead, Idempotency: connector.IdempotencyContract{Strategy: connector.IdempotencyNatural}, Reconciliation: connector.ReconciliationNone, Compensation: connector.CompensationContract{Mode: connector.CompensationNone}},
}

// NotificationEmulator returns a deterministic test-only Provider. It is not
// available to production composition or the official Connector Catalog.
func NotificationEmulator() connector.Adapter {
	send, err := connector.BindEnqueueDelivery(notificationEmulatorSend, func(_ context.Context, request connector.TypedRequest[notificationEmulatorInput]) (connector.DeliveryResult, error) {
		status := strings.TrimSpace(request.Input.EmulatorStatus)
		if status == "" {
			status, _ = request.Input.Payload["emulator_status"].(string)
			status = strings.TrimSpace(status)
		}
		switch status {
		case "expired":
			return connector.DeliveryResult{}, connector.PermanentError("notification.subscription_expired", errors.New("emulated expired subscription"))
		case "retry":
			return connector.DeliveryResult{}, connector.RetryableError("notification.retryable", errors.New("emulated retryable failure"))
		default:
			return connector.DeliveryResult{ResponseRef: "emulator:accepted"}, nil
		}
	})
	if err != nil {
		panic(err)
	}
	test, err := connector.BindCall(notificationEmulatorTest, func(context.Context, connector.TypedRequest[struct{}]) (connector.TypedResult[map[string]any], error) {
		return connector.TypedResult[map[string]any]{Output: map[string]any{"provider_status": "emulator"}, ResponseRef: "emulator:configured"}, nil
	})
	if err != nil {
		panic(err)
	}
	provider, err := connector.NewProvider(connector.ProviderSchema{ConnectorKey: "notification", ProviderKey: "deterministic_emulator", ProviderRevision: "test-v1"}, send, test)
	if err != nil {
		panic(err)
	}
	return &notificationEmulatorProvider{Adapter: provider}
}

type notificationEmulatorProvider struct{ connector.Adapter }

func (provider *notificationEmulatorProvider) TestConnection(ctx context.Context, _ connector.TestConnectionRequest) (connector.TestConnectionResult, error) {
	if err := ctx.Err(); err != nil {
		return connector.TestConnectionResult{}, err
	}
	details, _ := json.Marshal(map[string]any{"provider_status": "emulator"})
	return connector.TestConnectionResult{Connected: true, Details: details}, nil
}
