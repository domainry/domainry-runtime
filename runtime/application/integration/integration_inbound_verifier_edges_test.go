package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

type integrationWebhookVerifierAdapter struct {
	verified integrationcontract.VerifiedInboundWebhook
	err      error
	request  integrationcontract.InboundWebhookRequest
}

func (a *integrationWebhookVerifierAdapter) Call(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	return integrationcontract.CallResult{}, nil
}

func (a *integrationWebhookVerifierAdapter) VerifyWebhook(_ context.Context, request integrationcontract.InboundWebhookRequest) (integrationcontract.VerifiedInboundWebhook, error) {
	a.request = request
	return a.verified, a.err
}

type integrationCodedWebhookError struct {
	code   string
	params map[string]string
}

func (e integrationCodedWebhookError) Error() string                  { return e.code }
func (e integrationCodedWebhookError) ErrorCode() string              { return e.code }
func (e integrationCodedWebhookError) ErrorParams() map[string]string { return e.params }

func integrationWebhookVerifierService(adapter integrationcontract.Adapter) *IntegrationApplicationService {
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{
		Key: "webhook", Provider: "provider", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider"}},
	}}})
	if adapter != nil {
		registerTestRegistryProvider(registry, "webhook", "provider", adapter)
	}
	return NewIntegrationApplicationService(ApplicationDependencies{Registry: registry})
}

func TestVerifyRegisteredInboundWebhookDispatchesAndClassifiesErrors(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{ConnectorKey: "webhook", ProviderKey: "provider"}
	request := integrationcontract.InboundWebhookRequest{Connection: connection, Headers: map[string]string{"x": "y"}, Body: []byte("body")}
	if _, err := NewIntegrationApplicationService(ApplicationDependencies{}).verifyRegisteredInboundWebhook(t.Context(), request); apperror.CodeOf(err) != "backend.integration.webhook.provider_unsupported" {
		t.Fatalf("unsupported error = %v", err)
	}
	if _, err := integrationWebhookVerifierService(&independentAdapter{}).verifyRegisteredInboundWebhook(t.Context(), request); apperror.CodeOf(err) != "backend.integration.webhook.provider_does_not_accept_events" {
		t.Fatalf("non-verifier error = %v", err)
	}
	adapter := &integrationWebhookVerifierAdapter{verified: integrationcontract.VerifiedInboundWebhook{EventType: "created", ExternalID: "event"}}
	verified, err := integrationWebhookVerifierService(adapter).verifyRegisteredInboundWebhook(t.Context(), request)
	if err != nil || verified.ExternalID != "event" || string(adapter.request.Body) != "body" || adapter.request.Headers["x"] != "y" {
		t.Fatalf("verified=%#v request=%#v err=%v", verified, adapter.request, err)
	}
	for name, test := range map[string]struct {
		err  error
		kind apperror.ErrorKind
		code string
	}{
		"signature":    {err: integrationCodedWebhookError{code: "backend.integration.webhook.signature_invalid", params: map[string]string{"provider": "probe"}}, kind: apperror.KindForbidden, code: "backend.integration.webhook.signature_invalid"},
		"timestamp":    {err: errors.New("backend.integration.webhook.timestamp_invalid: stale"), kind: apperror.KindForbidden, code: "backend.integration.webhook.timestamp_invalid"},
		"token":        {err: errors.New("backend.integration.webhook.token_invalid"), kind: apperror.KindForbidden, code: "backend.integration.webhook.token_invalid"},
		"client state": {err: errors.New("backend.integration.webhook.client_state_invalid"), kind: apperror.KindForbidden, code: "backend.integration.webhook.client_state_invalid"},
		"payload":      {err: integrationCodedWebhookError{code: "backend.integration.webhook.payload_invalid", params: map[string]string{"z": "last", "a": "first"}}, kind: apperror.KindBadRequest, code: "backend.integration.webhook.payload_invalid"},
		"empty code":   {err: integrationCodedWebhookError{}, kind: apperror.KindBadRequest, code: "backend.integration.webhook.invalid"},
	} {
		t.Run(name, func(t *testing.T) {
			adapter := &integrationWebhookVerifierAdapter{err: test.err}
			_, err := integrationWebhookVerifierService(adapter).verifyRegisteredInboundWebhook(t.Context(), request)
			if apperror.KindOf(err) != test.kind || apperror.CodeOf(err) != test.code {
				t.Fatalf("error = %#v", err)
			}
			if name == "payload" {
				var appErr *apperror.AppError
				if !errors.As(err, &appErr) || appErr.Params["a"] != "first" || appErr.Params["z"] != "last" {
					t.Fatalf("params = %#v", err)
				}
			}
		})
	}
}

func TestWebhookErrorDetailsAndInboundPayloadNormalization(t *testing.T) {
	code, params := webhookErrorDetails(errors.New(" backend.integration.webhook.body_invalid : details"))
	if code != "backend.integration.webhook.body_invalid" || params != nil {
		t.Fatalf("plain details code=%q params=%#v", code, params)
	}
	code, params = webhookErrorDetails(integrationCodedWebhookError{code: " ", params: map[string]string{"field": "body"}})
	if code != "backend.integration.webhook.invalid" || params["field"] != "body" {
		t.Fatalf("coded details code=%q params=%#v", code, params)
	}
	connection := integrationmodel.IntegrationConnection{Key: "primary", ConnectorKey: "webhook", ProviderKey: "provider"}
	if payload := inboundEventPayload(nil, connection); len(payload) != 1 {
		t.Fatalf("nil payload = %#v", payload)
	}
	original := map[string]any{"name": "event", "password": "secret", "nested": map[string]any{"token": "secret"}}
	payload := inboundEventPayload(original, connection)
	if payload["name"] != "event" || payload["password"] == "secret" || original["password"] != "secret" {
		t.Fatalf("payload=%#v original=%#v", payload, original)
	}
	contextValue, ok := payload["_integration_context"].(map[string]any)
	if !ok || contextValue["connector_key"] != "webhook" || contextValue["provider_key"] != "provider" || contextValue["connection_key"] != "primary" {
		t.Fatalf("integration context = %#v", payload["_integration_context"])
	}
}
