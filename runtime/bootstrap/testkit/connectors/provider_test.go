package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/domainry/domainry-connector-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

type testCallAdapter struct {
	result integrationcontract.CallResult
	err    error
}

func (a *testCallAdapter) Call(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	return a.result, a.err
}

type testFullAdapter struct {
	testCallAdapter
	schema        integrationmodel.ConnectorProviderSchema
	validateErr   error
	testResult    integrationcontract.CallResult
	testErr       error
	webhookResult integrationcontract.VerifiedInboundWebhook
	webhookErr    error
}

func (a *testFullAdapter) ProviderSchema() integrationmodel.ConnectorProviderSchema { return a.schema }
func (a *testFullAdapter) ValidateConfig(integrationmodel.IntegrationConnection) error {
	return a.validateErr
}
func (a *testFullAdapter) TestConnection(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	return a.testResult, a.testErr
}
func (a *testFullAdapter) VerifyWebhook(context.Context, integrationcontract.InboundWebhookRequest) (integrationcontract.VerifiedInboundWebhook, error) {
	return a.webhookResult, a.webhookErr
}

func TestProviderCallAndOptionalContracts(t *testing.T) {
	delegate := &testFullAdapter{
		testCallAdapter: testCallAdapter{result: integrationcontract.CallResult{
			Response: map[string]any{"ok": true}, ResponseRef: "response", SecretUpdates: map[string]string{"token": "next"},
			ResourceHealth: &integrationmodel.IntegrationProviderResourceHealth{ObservationID: "observation", State: "healthy"},
		}},
		schema: integrationmodel.ConnectorProviderSchema{ProviderRevision: " revision ", OperationKeys: []string{"read", "missing"}},
	}
	provider := Provider("connector", "provider", delegate, []integrationmodel.ConnectorOperationSchema{{Key: "read", Method: "GET"}})
	descriptor := provider.Descriptor()
	if descriptor.ProviderRevision != "revision" || len(descriptor.Operations) != 2 {
		t.Fatalf("descriptor=%#v", descriptor)
	}
	operation := descriptor.Operations[0]
	request := connector.CallRequest{ConnectorKey: "connector", ProviderKey: "provider", OperationKey: operation.Key, ContractSHA256: operation.ContractSHA256, Mode: operation.Mode, Payload: json.RawMessage(`{"id":1}`)}
	result, err := provider.Call(t.Context(), request)
	if err != nil || result.ResponseRef != "response" || result.ResourceHealth == nil {
		t.Fatalf("call result=%#v err=%v", result, err)
	}

	for _, mutate := range []func(*connector.CallRequest){
		func(value *connector.CallRequest) { value.OperationKey = "unknown" },
		func(value *connector.CallRequest) { value.ConnectorKey = "other" },
		func(value *connector.CallRequest) { value.ProviderKey = "other" },
		func(value *connector.CallRequest) { value.ContractSHA256 = "other" },
		func(value *connector.CallRequest) { value.Mode = connector.ModeEnqueue },
	} {
		candidate := request
		mutate(&candidate)
		if _, err := provider.Call(t.Context(), candidate); err == nil {
			t.Fatal("contract mismatch accepted")
		}
	}
	invalidPayload := request
	invalidPayload.Payload = json.RawMessage(`{`)
	if _, err := provider.Call(t.Context(), invalidPayload); err == nil {
		t.Fatal("invalid payload accepted")
	}
	delegate.result.Response = map[string]any{"bad": make(chan int)}
	if _, err := provider.Call(t.Context(), request); err == nil {
		t.Fatal("unencodable response accepted")
	}
	delegate.result.Response = map[string]any{"ok": true}
	delegate.err = errors.New("call")
	if _, err := provider.Call(t.Context(), request); !errors.Is(err, delegate.err) {
		t.Fatalf("call error=%v", err)
	}

	validator := provider.(connector.ConfigValidator)
	delegate.validateErr = errors.New("validate")
	if err := validator.ValidateConfig(connector.Connection{}); !errors.Is(err, delegate.validateErr) {
		t.Fatalf("validation error=%v", err)
	}
	tester := provider.(connector.ConnectionTester)
	delegate.testErr = errors.New("test")
	if _, err := tester.TestConnection(t.Context(), connector.TestConnectionRequest{}); !errors.Is(err, delegate.testErr) {
		t.Fatalf("test error=%v", err)
	}
	delegate.testErr = nil
	delegate.testResult = integrationcontract.CallResult{Response: map[string]any{"connected": false}, SecretUpdates: map[string]string{"token": "next"}}
	if got, err := tester.TestConnection(t.Context(), connector.TestConnectionRequest{}); err != nil || got.Connected {
		t.Fatalf("connection test=%#v err=%v", got, err)
	}
	delegate.testResult.Response = map[string]any{"connected": "unknown"}
	if got, err := tester.TestConnection(t.Context(), connector.TestConnectionRequest{}); err != nil || !got.Connected {
		t.Fatalf("default connection test=%#v err=%v", got, err)
	}
	delegate.testResult.Response = map[string]any{"bad": make(chan int)}
	if _, err := tester.TestConnection(t.Context(), connector.TestConnectionRequest{}); err == nil {
		t.Fatal("unencodable connection result accepted")
	}

	verifier := provider.(connector.WebhookVerifier)
	delegate.webhookErr = errors.New("verify")
	if _, err := verifier.VerifyWebhook(t.Context(), connector.VerifyWebhookRequest{}); !errors.Is(err, delegate.webhookErr) {
		t.Fatalf("webhook error=%v", err)
	}
	delegate.webhookErr = nil
	delegate.webhookResult = integrationcontract.VerifiedInboundWebhook{
		EventType: "created", Payload: map[string]any{"id": 1},
		Security:         &integrationcontract.WebhookSecurityEvidence{SignatureVerified: true, EventTime: "2026-01-02T03:04:05Z"},
		ExternalIdentity: &integrationcontract.WebhookExternalIdentity{Subject: "subject"},
		DeliveryReceipt:  &integrationcontract.WebhookDeliveryReceipt{ResponseRef: "response", OccurredAt: "2026-01-02T03:04:05Z"},
	}
	if got, err := verifier.VerifyWebhook(t.Context(), connector.VerifyWebhookRequest{Headers: map[string][]string{"X": {"one", "two"}, "Empty": {}}, Query: map[string][]string{"q": {"one"}}}); err != nil || got.Security == nil || got.ExternalIdentity == nil || got.DeliveryReceipt == nil {
		t.Fatalf("verified webhook=%#v err=%v", got, err)
	}
	delegate.webhookResult.Payload = map[string]any{"bad": make(chan int)}
	if _, err := verifier.VerifyWebhook(t.Context(), connector.VerifyWebhookRequest{}); err == nil {
		t.Fatal("unencodable webhook accepted")
	}
}

func TestProviderDescriptorFieldsAndHelpers(t *testing.T) {
	minimum, maximum := 1.0, 10.0
	schema := integrationmodel.ConnectorProviderSchema{
		ConfigFields: []definitionmodel.FieldSchema{
			{Key: "limit", Type: "decimal", Validation: definitionmodel.FieldValidation{Min: &minimum, Max: &maximum}, Default: 2, Config: map[string]any{"required_with": []any{"account", 2}}},
			{Key: "mode", Name: "Mode", Type: "text", Validation: definitionmodel.FieldValidation{Options: []string{"fast"}}, I18n: map[string]map[string]string{"en": {"name": "Mode"}}},
			{Key: "bad-default", Type: "text", Default: make(chan int)},
		},
		SecretFields: []definitionmodel.FieldSchema{{Key: "token", Name: "Token", Type: "text", I18n: map[string]map[string]string{"en": {"name": "Token"}}, Config: map[string]any{"credential_kind": "api_key", "material_format": "text", "rotation_policy": "automatic", "expiry_policy": "required", "test_requirement": "always"}}, {Key: "fallback"}},
	}
	provider := Provider("connector", "provider", &testCallAdapter{}, nil, schema)
	descriptor := provider.Descriptor()
	if descriptor.ProviderRevision != "test-v1" || len(descriptor.ConfigFields) != 3 || descriptor.ConfigFields[1].Type != connector.ConfigFieldSelect || len(descriptor.SecretFields) != 2 {
		t.Fatalf("descriptor fields=%#v", descriptor)
	}
	if descriptor.ConfigFields[0].Validation.Min == &minimum || descriptor.ConfigFields[0].Validation.Max == &maximum {
		t.Fatal("validation float pointers were not cloned")
	}

	operations := []integrationmodel.ConnectorOperationSchema{{}, {Key: "async", ExecutionMode: "async", SideEffect: "write"}, {Key: "read"}}
	operationProvider := Provider("connector", "operations", &testCallAdapter{}, operations)
	if len(operationProvider.Descriptor().Operations) != 2 || operationProvider.Descriptor().Operations[0].Mode != connector.ModeEnqueue || operationProvider.Descriptor().Operations[1].Reliability.Idempotency.Strategy != connector.IdempotencyNatural {
		t.Fatalf("operations=%#v", operationProvider.Descriptor().Operations)
	}

	if got, err := decodePayload(nil); err != nil || len(got) != 0 {
		t.Fatalf("empty payload=%v err=%v", got, err)
	}
	if got, err := decodePayload(json.RawMessage(`null`)); err != nil || len(got) != 0 {
		t.Fatalf("null payload=%v err=%v", got, err)
	}
	if cloneStrings(nil) != nil || cloneAny(nil) != nil || localization(nil) != nil || cloneMultiStrings(nil) != nil || cloneFloat(nil) != nil {
		t.Fatal("nil clone contract failed")
	}
	if cloneAny(map[string]any{"key": "value"})["key"] != "value" || localization(map[string]map[string]string{"en": {"name": "Name"}})["en"].Name != "Name" {
		t.Fatal("non-empty clone contract failed")
	}
	if configString(nil, "missing", "fallback") != "fallback" || configString(map[string]any{"key": " value "}, "key", "fallback") != "value" {
		t.Fatal("config string contract failed")
	}
	if configString(map[string]any{"key": " "}, "key", "fallback") != "fallback" {
		t.Fatal("blank config string did not fall back")
	}
	if stringSlice([]string{"a"})[0] != "a" || len(stringSlice([]any{"a", 2})) != 1 || stringSlice(1) != nil {
		t.Fatal("string slice contract failed")
	}
	if resourceHealth(nil) != nil || webhookSecurity(nil) != nil || webhookIdentity(nil) != nil || webhookReceipt(nil) != nil {
		t.Fatal("nil optional projection failed")
	}
	Registry(operationProvider)
	t.Run("invalid registry panics", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("invalid provider registration did not panic")
			}
		}()
		Registry(&providerAdapter{})
	})
}

func TestCombineCapabilityMatrix(t *testing.T) {
	base := &providerAdapter{}
	validate := configValidator(func(connector.Connection) error { return nil })
	test := connectionTester(func(context.Context, connector.TestConnectionRequest) (connector.TestConnectionResult, error) {
		return connector.TestConnectionResult{}, nil
	})
	verify := webhookVerifier(func(context.Context, connector.VerifyWebhookRequest) (connector.VerifiedWebhook, error) {
		return connector.VerifiedWebhook{}, nil
	})
	for mask := 0; mask < 8; mask++ {
		adapter := combine(base, validate, test, verify, mask&1 != 0, mask&2 != 0, mask&4 != 0)
		if adapter == nil {
			t.Fatalf("nil adapter for mask %d", mask)
		}
	}
}
