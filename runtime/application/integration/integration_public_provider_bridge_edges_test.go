package integration

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/domainry/domainry-connector-sdk"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
)

func TestPublicProviderAdapterCallAndIdentityEdges(t *testing.T) {
	descriptor := publicBridgeDescriptor(true)
	nilAdapter := &publicProviderAdapter{descriptor: descriptor}
	if _, err := nilAdapter.Call(t.Context(), integrationcontract.CallRequest{}); err == nil {
		t.Fatal("nil provider accepted")
	}

	provider := &publicBridgeProvider{descriptor: descriptor}
	adapter := &publicProviderAdapter{provider: provider, descriptor: descriptor}
	request := integrationcontract.CallRequest{
		ConnectorKey: "crm", Operation: "push", Request: map[string]any{}, Connection: publicBridgeConnection(),
		Secrets: map[string]string{"private_key": "secret"},
	}
	for _, mutation := range []func(*integrationcontract.CallRequest){
		func(request *integrationcontract.CallRequest) { request.ConnectorKey = "wrong" },
		func(request *integrationcontract.CallRequest) { request.Connection.ConnectorKey = "wrong" },
		func(request *integrationcontract.CallRequest) { request.Connection.ProviderKey = "wrong" },
	} {
		current := request
		mutation(&current)
		if _, err := adapter.Call(t.Context(), current); err == nil {
			t.Fatal("mismatched provider identity accepted")
		}
	}
	unknown := request
	unknown.Operation = "missing"
	if _, err := adapter.Call(t.Context(), unknown); err == nil {
		t.Fatal("unknown operation accepted")
	}
	invalidRequest := request
	invalidRequest.Request = map[string]any{"invalid": func() {}}
	if _, err := adapter.Call(t.Context(), invalidRequest); err == nil {
		t.Fatal("unencodable request accepted")
	}
	invalidSecret := request
	invalidSecret.Secrets = map[string]string{" private_key ": "secret"}
	if _, err := adapter.Call(t.Context(), invalidSecret); err == nil {
		t.Fatal("non-canonical secret accepted")
	}
	provider.callPayload = json.RawMessage(`{`)
	if _, err := adapter.Call(t.Context(), request); err == nil {
		t.Fatal("invalid provider response accepted")
	}
	provider.callPayload = nil
	provider.callSecretUpdates = map[string]string{" private_key ": "updated"}
	if _, err := adapter.Call(t.Context(), request); err == nil {
		t.Fatal("non-canonical secret update accepted")
	}

	if _, ok := adapter.OperationIdentity("missing"); ok {
		t.Fatal("unknown operation identity found")
	}
	if identity, ok := adapter.OperationIdentity(" push "); !ok || identity.Key != "push" {
		t.Fatalf("operation identity=%#v found=%v", identity, ok)
	}
}

type publicBridgeConfigProvider struct {
	*publicBridgeProvider
	err error
}

func (p *publicBridgeConfigProvider) ValidateConfig(connector.Connection) error { return p.err }

func TestPublicProviderAdapterConfigAndConnectionTestEdges(t *testing.T) {
	descriptor := publicBridgeDescriptor(true)
	base := &publicProviderAdapter{provider: callOnlyPublicBridgeProvider{delegate: &publicBridgeProvider{descriptor: descriptor}}, descriptor: descriptor}
	if err := base.ValidateConfig(publicBridgeConnection()); err != nil {
		t.Fatalf("provider without config validator: %v", err)
	}
	configProvider := &publicBridgeConfigProvider{publicBridgeProvider: &publicBridgeProvider{descriptor: descriptor}, err: errors.New("invalid config")}
	configAdapter := &publicProviderAdapter{provider: configProvider, descriptor: descriptor}
	if err := configAdapter.ValidateConfig(publicBridgeConnection()); err == nil {
		t.Fatal("config validator error ignored")
	}
	wrong := publicBridgeConnection()
	wrong.ProviderKey = "wrong"
	if err := configAdapter.ValidateConfig(wrong); err == nil {
		t.Fatal("config identity mismatch accepted")
	}

	provider := &publicBridgeProvider{descriptor: descriptor}
	adapter := newPublicProviderAdapter(provider, descriptor).(integrationcontract.ConnectionTester)
	request := integrationcontract.CallRequest{ConnectorKey: "crm", Connection: publicBridgeConnection(), Secrets: map[string]string{"private_key": "secret"}}
	wrongRequest := request
	wrongRequest.Connection.ProviderKey = "wrong"
	if _, err := adapter.TestConnection(t.Context(), wrongRequest); err == nil {
		t.Fatal("test connection identity mismatch accepted")
	}
	undeclared := request
	undeclared.Secrets = map[string]string{"other": "secret"}
	if _, err := adapter.TestConnection(t.Context(), undeclared); err == nil {
		t.Fatal("test connection undeclared secret accepted")
	}
	for _, details := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`"scalar"`), json.RawMessage(`{"connected":"shadow","region":"eu"}`)} {
		provider.testResult = &connector.TestConnectionResult{Connected: true, Details: details}
		if result, err := adapter.TestConnection(t.Context(), request); err != nil || result.Response["connected"] != true {
			t.Fatalf("details=%s result=%#v err=%v", details, result, err)
		}
	}
	provider.testResult = &connector.TestConnectionResult{Connected: true, Details: json.RawMessage(`{`)}
	if _, err := adapter.TestConnection(t.Context(), request); err == nil {
		t.Fatal("invalid connection details accepted")
	}
	provider.testResult = &connector.TestConnectionResult{Connected: true, SecretUpdates: map[string]string{"other": "secret"}}
	if _, err := adapter.TestConnection(t.Context(), request); err == nil {
		t.Fatal("undeclared connection-test secret update accepted")
	}
}

func TestPublicProviderAdapterWebhookEdges(t *testing.T) {
	descriptor := publicBridgeDescriptor(true)
	provider := &publicBridgeProvider{descriptor: descriptor}
	adapter := newPublicProviderAdapter(provider, descriptor).(integrationcontract.WebhookVerifier)
	request := integrationcontract.InboundWebhookRequest{Connection: publicBridgeConnection(), Headers: map[string]string{"X-Test": "one"}, Query: map[string]string{"page": "one"}, Secrets: map[string]string{"private_key": "secret"}}

	wrong := request
	wrong.Connection.ProviderKey = "wrong"
	if _, err := adapter.VerifyWebhook(t.Context(), wrong); err == nil {
		t.Fatal("webhook identity mismatch accepted")
	}
	undeclared := request
	undeclared.Secrets = map[string]string{"other": "secret"}
	if _, err := adapter.VerifyWebhook(t.Context(), undeclared); err == nil {
		t.Fatal("webhook undeclared secret accepted")
	}
	provider.webhookErr = connector.PermanentError("acme.invalid_webhook", errors.New("invalid"))
	if _, err := adapter.VerifyWebhook(t.Context(), request); err == nil {
		t.Fatal("webhook provider error ignored")
	}
	provider.webhookErr = nil
	provider.webhookResult = &connector.VerifiedWebhook{EventType: "event", ExternalID: "id", Payload: json.RawMessage(`{`)}
	if _, err := adapter.VerifyWebhook(t.Context(), request); err == nil {
		t.Fatal("invalid webhook payload accepted")
	}
}

func TestPublicProviderAdapterReconcileEdges(t *testing.T) {
	descriptor := publicBridgeDescriptor(true)
	provider := &publicBridgeProvider{descriptor: descriptor}
	adapter := newPublicProviderAdapter(provider, descriptor).(integrationcontract.Reconciler)
	request := integrationcontract.ReconcileRequest{
		ConnectorKey: "crm", Connection: publicBridgeConnection(), Operation: "push", Request: map[string]any{},
		RequestRef: "request-ref", Secrets: map[string]string{"private_key": "secret"},
	}
	wrong := request
	wrong.Connection.ProviderKey = "wrong"
	if _, err := adapter.Reconcile(t.Context(), wrong); err == nil {
		t.Fatal("reconcile identity mismatch accepted")
	}
	unknown := request
	unknown.Operation = "missing"
	if _, err := adapter.Reconcile(t.Context(), unknown); err == nil {
		t.Fatal("unknown reconcile operation accepted")
	}
	nonReconcilingDescriptor := publicBridgeDescriptor(false)
	nonReconciling := newPublicProviderAdapter(provider, nonReconcilingDescriptor).(integrationcontract.Reconciler)
	if _, err := nonReconciling.Reconcile(t.Context(), request); err == nil {
		t.Fatal("non-reconciling operation accepted")
	}
	invalidRequest := request
	invalidRequest.Request = map[string]any{"invalid": func() {}}
	if _, err := adapter.Reconcile(t.Context(), invalidRequest); err == nil {
		t.Fatal("unencodable reconcile request accepted")
	}
	undeclared := request
	undeclared.Secrets = map[string]string{"other": "secret"}
	if _, err := adapter.Reconcile(t.Context(), undeclared); err == nil {
		t.Fatal("reconcile undeclared secret accepted")
	}
	invalidEnvelope := request
	invalidEnvelope.RequestRef = ""
	if _, err := adapter.Reconcile(t.Context(), invalidEnvelope); err == nil {
		t.Fatal("invalid reconcile envelope accepted")
	}
	provider.reconcileErr = connector.RetryableError("acme.retry", errors.New("retry"))
	if _, err := adapter.Reconcile(t.Context(), request); err == nil {
		t.Fatal("reconcile provider error ignored")
	}
	provider.reconcileErr = nil
	provider.reconcileResult = &connector.ReconcileResult{Outcome: "invalid"}
	if _, err := adapter.Reconcile(t.Context(), request); err == nil {
		t.Fatal("invalid reconcile result accepted")
	}
	provider.reconcileResult = &connector.ReconcileResult{Outcome: connector.ReconciliationNotFound}
	if result, err := adapter.Reconcile(t.Context(), request); err != nil || result.Outcome != integrationcontract.ReconciliationNotFound {
		t.Fatalf("nil reconcile result=%#v err=%v", result, err)
	}
	provider.reconcileResult = &connector.ReconcileResult{Outcome: connector.ReconciliationSucceeded, Result: &connector.CallResult{Payload: json.RawMessage(`{`)}}
	if _, err := adapter.Reconcile(t.Context(), request); err == nil {
		t.Fatal("invalid reconcile payload accepted")
	}
	provider.reconcileResult = &connector.ReconcileResult{Outcome: connector.ReconciliationSucceeded, Result: &connector.CallResult{Payload: json.RawMessage(`{}`), SecretUpdates: map[string]string{"other": "secret"}}}
	if _, err := adapter.Reconcile(t.Context(), request); err == nil {
		t.Fatal("undeclared reconcile secret update accepted")
	}
}
