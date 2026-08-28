package integration

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	connector "github.com/domainry/domainry-connector-sdk"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const publicBridgeContractSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type publicBridgeProvider struct {
	descriptor         connector.ProviderDescriptor
	callRequest        connector.CallRequest
	testRequest        connector.TestConnectionRequest
	webhookRequest     connector.VerifyWebhookRequest
	reconcileRequest   connector.ReconcileRequest
	callErr            error
	callSecretUpdates  map[string]string
	callPayload        json.RawMessage
	callResourceHealth *connector.ResourceHealthReport
	testResult         *connector.TestConnectionResult
	testErr            error
	webhookResult      *connector.VerifiedWebhook
	webhookErr         error
	reconcileResult    *connector.ReconcileResult
	reconcileErr       error
}

func (p *publicBridgeProvider) Descriptor() connector.ProviderDescriptor { return p.descriptor }

func (p *publicBridgeProvider) Call(_ context.Context, request connector.CallRequest) (connector.CallResult, error) {
	p.callRequest = request
	updates := p.callSecretUpdates
	if updates == nil {
		updates = map[string]string{"private_key": "value"}
	}
	payload := p.callPayload
	if payload == nil {
		payload = json.RawMessage(`{"accepted":"customer-1"}`)
	}
	return connector.CallResult{Payload: payload, ResponseRef: "provider-ref", SecretUpdates: updates, ResourceHealth: p.callResourceHealth}, p.callErr
}

func (p *publicBridgeProvider) TestConnection(_ context.Context, request connector.TestConnectionRequest) (connector.TestConnectionResult, error) {
	p.testRequest = request
	if p.testResult != nil || p.testErr != nil {
		if p.testResult == nil {
			return connector.TestConnectionResult{}, p.testErr
		}
		return *p.testResult, p.testErr
	}
	return connector.TestConnectionResult{Connected: true, Details: json.RawMessage(`{"region":"eu"}`), SecretUpdates: map[string]string{"private_key": "new"}}, nil
}

func (p *publicBridgeProvider) VerifyWebhook(_ context.Context, request connector.VerifyWebhookRequest) (connector.VerifiedWebhook, error) {
	p.webhookRequest = request
	if p.webhookResult != nil || p.webhookErr != nil {
		if p.webhookResult == nil {
			return connector.VerifiedWebhook{}, p.webhookErr
		}
		return *p.webhookResult, p.webhookErr
	}
	eventTime := time.Date(2026, 7, 23, 1, 2, 3, 4, time.FixedZone("offset", 8*60*60))
	return connector.VerifiedWebhook{
		EventType: "customer.changed", ExternalID: "event-1", Payload: json.RawMessage(`{"customer_id":"customer-1"}`),
		Security:  &connector.WebhookSecurityEvidence{SignatureVerified: true, Nonce: "nonce", DeviceIdentity: "device", EventTime: eventTime},
		Challenge: "challenge", ChallengeFormat: "plain",
		ExternalIdentity: &connector.WebhookExternalIdentity{Subject: "subject", SubjectType: "user", Name: "Ada", Group: "sales"},
		DeliveryReceipt:  &connector.WebhookDeliveryReceipt{ResponseRef: "webhook-ref", Status: "accepted", OccurredAt: eventTime},
	}, nil
}

func (p *publicBridgeProvider) Reconcile(_ context.Context, request connector.ReconcileRequest) (connector.ReconcileResult, error) {
	p.reconcileRequest = request
	if p.reconcileResult != nil || p.reconcileErr != nil {
		if p.reconcileResult == nil {
			return connector.ReconcileResult{}, p.reconcileErr
		}
		return *p.reconcileResult, p.reconcileErr
	}
	return connector.ReconcileResult{
		Outcome: connector.ReconciliationSucceeded,
		Result:  &connector.CallResult{Payload: json.RawMessage(`{"state":"completed"}`), ResponseRef: "reconciled-ref", SecretUpdates: map[string]string{"private_key": "reconciled"}},
	}, nil
}

type callOnlyPublicBridgeProvider struct{ delegate *publicBridgeProvider }

func (p callOnlyPublicBridgeProvider) Descriptor() connector.ProviderDescriptor {
	return p.delegate.Descriptor()
}

func (p callOnlyPublicBridgeProvider) Call(ctx context.Context, request connector.CallRequest) (connector.CallResult, error) {
	return p.delegate.Call(ctx, request)
}

func publicBridgeDescriptor(reconcile bool) connector.ProviderDescriptor {
	reconciliation := connector.ReconciliationNone
	if reconcile {
		reconciliation = connector.ReconciliationProviderLookup
	}
	min, max := 1.5, 99.5
	return connector.ProviderDescriptor{
		ConnectorKey: "crm", ProviderKey: "acme", ProviderRevision: "provider-v3",
		ConfigFields: []connector.ConfigField{
			{Key: "endpoint", Name: "Endpoint", Description: "API endpoint", Type: connector.ConfigFieldText, Required: true, I18n: map[string]connector.FieldLocalization{"zh-CN": {Name: "地址", Description: "接口地址"}}, Validation: connector.ConfigValidation{MinLength: 3, MaxLength: 100, Pattern: `^https://`}},
			{Key: "limit", Name: "Limit", Type: connector.ConfigFieldDecimal, Default: json.RawMessage(`2.5`), Validation: connector.ConfigValidation{Min: &min, Max: &max}, RequiredWith: []string{"endpoint"}},
			{Key: "metadata", Name: "Metadata", Type: connector.ConfigFieldJSON, Default: json.RawMessage(`"scalar-json"`)},
		},
		SecretFields: []connector.SecretField{{
			Key: "private_key", Name: "Private key", Required: true,
			CredentialKind: connector.SecretCredentialPrivateKey, MaterialFormat: connector.SecretMaterialPEM,
			RotationPolicy: connector.SecretRotationOAuthRefresh, ExpiryPolicy: connector.SecretExpiryRequired, TestRequirement: connector.SecretTestOptional,
		}},
		Operations: []connector.OperationDescriptor{{
			ConnectorKey: "crm", ProviderKey: "acme", Key: "push", Mode: connector.ModeCall, ContractSHA256: publicBridgeContractSHA256,
			Reliability: connector.ReliabilityContract{Effect: connector.EffectWrite, Idempotency: connector.IdempotencyContract{Strategy: connector.IdempotencyProviderKey, KeyRetentionSeconds: 86400}, Reconciliation: reconciliation, Compensation: connector.CompensationContract{Mode: connector.CompensationNone}},
		}},
	}
}

func registerPublicBridgeProvider(t *testing.T, provider connector.Adapter) (*ConnectorRegistry, integrationcontract.Adapter) {
	t.Helper()
	providers := connector.NewRegistry()
	if err := providers.Register(provider); err != nil {
		t.Fatal(err)
	}
	providers.Freeze()
	registry := NewConnectorRegistryWithProviders(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{
		Key: "crm", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "acme", Name: "Acme", Description: "Acme CRM"}},
	}}}, providers)
	adapter, ok := registry.ProviderAdapter("crm", "acme")
	if !ok {
		t.Fatal("public provider was not registered under its exact pair")
	}
	return registry, adapter
}

func publicBridgeConnection() integrationmodel.IntegrationConnection {
	return integrationmodel.IntegrationConnection{
		Key: "primary", WorkspaceID: "workspace", ConnectorKey: "crm", ProviderKey: "acme", Name: "Primary", Status: "verified",
		Config: map[string]any{"endpoint": "https://api.example"}, SecretRefs: map[string]string{"private_key": "secret:private-key"},
		CreatedBy: "user", CreatedAt: "created", UpdatedAt: "updated",
	}
}

func publicBridgePrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user", WorkspaceID: "workspace", DepartmentID: "department"}, RequestID: "request",
		CorrelationID: "correlation", CausationID: "causation", SurfaceKey: "surface",
	}, accessfixture.Bundle{Key: "manager"},
	)
}

func TestPublicConnectorProviderBridgeConvertsCallAndSchemaWithoutGuessing(t *testing.T) {
	quotaUsed := 92
	provider := &publicBridgeProvider{descriptor: publicBridgeDescriptor(true), callResourceHealth: &connector.ResourceHealthReport{
		ObservationID: "quota-bridge-1", Kind: "quota", State: "warning", PreviousState: "healthy", EvidenceSource: "provider_api",
		QuotaUsedPercent: &quotaUsed, CapabilityBlocked: false, ObservedAt: "2026-07-28T12:00:00Z", ErrorCode: "acme.quota_warning",
	}}
	registry, adapter := registerPublicBridgeProvider(t, provider)
	if _, ok := adapter.(interface {
		Invoke(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error)
	}); ok {
		t.Fatal("public provider bridge must not create a second Invoke channel")
	}
	result, err := adapter.Call(t.Context(), integrationcontract.CallRequest{
		ConnectorKey: "crm", Operation: "push", Method: "ignored-internal-legacy", Request: map[string]any{"value": "customer-1"}, RequestRef: "request-ref",
		Connection: publicBridgeConnection(), Headers: map[string]string{"trace": "value"}, Secrets: map[string]string{"private_key": "secret"}, Delivery: true,
		Timeout: 3 * time.Second, Principal: publicBridgePrincipal(),
	})
	if err != nil || result.Response["accepted"] != "customer-1" || result.ResponseRef != "provider-ref" || result.SecretUpdates["private_key"] != "value" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if result.ResourceHealth == nil || result.ResourceHealth.ObservationID != "quota-bridge-1" || result.ResourceHealth.QuotaUsedPercent == nil || *result.ResourceHealth.QuotaUsedPercent != 92 || result.ResourceHealth.ErrorCode != "acme.quota_warning" {
		t.Fatalf("resource health bridge=%+v", result.ResourceHealth)
	}
	request := provider.callRequest
	if request.ConnectorKey != "crm" || request.ProviderKey != "acme" || request.OperationKey != "push" || request.ContractSHA256 != publicBridgeContractSHA256 || request.Mode != connector.ModeCall || request.RequestRef != "request-ref" || request.Timeout != 3*time.Second || !request.Delivery {
		t.Fatalf("call envelope=%+v", request)
	}
	if string(request.Payload) != `{"value":"customer-1"}` || request.Connection.Config["endpoint"] != "https://api.example" || request.Connection.SecretRefs["private_key"] != "secret:private-key" || request.Headers["trace"] != "value" || request.Secrets["private_key"] != "secret" || request.Principal.RoleKey != "manager" || !request.Principal.IsAuthenticated {
		t.Fatalf("call DTO=%+v", request)
	}

	schema := adapter.(integrationcontract.SchemaProvider).ProviderSchema()
	if schema.Key != "acme" || schema.ProviderRevision != "provider-v3" || !reflect.DeepEqual(schema.OperationKeys, []string{"push"}) || len(schema.ConfigFields) != 3 || len(schema.SecretFields) != 1 {
		t.Fatalf("provider schema=%#v", schema)
	}
	endpoint, limit, metadata, secret := schema.ConfigFields[0], schema.ConfigFields[1], schema.ConfigFields[2], schema.SecretFields[0]
	if endpoint.I18n["zh-CN"]["description"] != "接口地址" || endpoint.Validation.Pattern != `^https://` || endpoint.Config["contract_owner"] != "connector" || limit.Default != json.Number("2.5") || !reflect.DeepEqual(limit.Config["required_with"], []string{"endpoint"}) || metadata.Default != "scalar-json" {
		t.Fatalf("mapped config fields=%#v", schema.ConfigFields)
	}
	if secret.Type != "text" || secret.Config["sensitive"] != true || secret.Config["material_format"] != "pem" || secret.Config["rotation_policy"] != "oauth_refresh" || secret.Config["expiry_policy"] != "required" || secret.Config["test_requirement"] != "optional" {
		t.Fatalf("mapped secret=%#v", secret)
	}
	projected := registry.Schema().Connectors[0].Providers[0]
	if projected.Name != "Acme" || projected.ProviderRevision != "provider-v3" || len(projected.ConfigFields) != 3 {
		t.Fatalf("projected provider=%#v", projected)
	}
	if _, fallback := registry.ProviderAdapter("crm", ""); fallback {
		t.Fatal("public provider must not register a connector-only fallback")
	}
}

func TestPublicConnectorProviderBridgeScopesSecretAuthorityToDescriptor(t *testing.T) {
	provider := &publicBridgeProvider{descriptor: publicBridgeDescriptor(true)}
	_, adapter := registerPublicBridgeProvider(t, provider)
	connection := publicBridgeConnection()
	connection.SecretRefs["other"] = "secret:other"
	request := integrationcontract.CallRequest{
		ConnectorKey: "crm", Operation: "push", Request: map[string]any{"value": "customer-1"},
		Connection: connection, Secrets: map[string]string{"private_key": "material"}, Principal: publicBridgePrincipal(),
	}
	if _, err := adapter.Call(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if provider.callRequest.Connection.SecretRefs["private_key"] != "secret:private-key" {
		t.Fatalf("declared secret reference missing: %#v", provider.callRequest.Connection.SecretRefs)
	}
	if _, exists := provider.callRequest.Connection.SecretRefs["other"]; exists {
		t.Fatalf("undeclared secret reference reached provider: %#v", provider.callRequest.Connection.SecretRefs)
	}

	undeclared := request
	undeclared.Secrets = map[string]string{"private_key": "material", "other": "material"}
	if _, err := adapter.Call(t.Context(), undeclared); err == nil || !strings.Contains(err.Error(), "received undeclared secret other") {
		t.Fatalf("undeclared secret material accepted: %v", err)
	}

	provider.callSecretUpdates = map[string]string{"other": "rotated"}
	if _, err := adapter.Call(t.Context(), request); err == nil || !strings.Contains(err.Error(), "returned undeclared secret update other") {
		t.Fatalf("undeclared secret update accepted: %v", err)
	}
}

func TestPublicConnectorProviderBridgeConvertsOptionalCapabilities(t *testing.T) {
	provider := &publicBridgeProvider{descriptor: publicBridgeDescriptor(true)}
	_, adapter := registerPublicBridgeProvider(t, provider)
	connection, principal := publicBridgeConnection(), publicBridgePrincipal()

	tester, ok := adapter.(integrationcontract.ConnectionTester)
	if !ok {
		t.Fatal("declared public ConnectionTester was lost")
	}
	tested, err := tester.TestConnection(t.Context(), integrationcontract.CallRequest{ConnectorKey: "crm", Connection: connection, Secrets: map[string]string{"private_key": "secret"}, Timeout: time.Second, Principal: principal})
	if err != nil || tested.Response["connected"] != true || tested.Response["details"].(map[string]any)["region"] != "eu" || tested.SecretUpdates["private_key"] != "new" {
		t.Fatalf("test result=%#v err=%v", tested, err)
	}
	if provider.testRequest.Connection.Key != "primary" || provider.testRequest.Secrets["private_key"] != "secret" || provider.testRequest.Principal.RequestID != "request" || provider.testRequest.Timeout != time.Second {
		t.Fatalf("test request=%#v", provider.testRequest)
	}

	verifier, ok := adapter.(integrationcontract.WebhookVerifier)
	if !ok {
		t.Fatal("declared public WebhookVerifier was lost")
	}
	receivedAt := time.Date(2026, 7, 23, 4, 5, 6, 0, time.UTC)
	verified, err := verifier.VerifyWebhook(t.Context(), integrationcontract.InboundWebhookRequest{
		Connection: connection, HeaderValues: map[string][]string{"signature": {"sig-1", "sig-2"}}, QueryValues: map[string][]string{"code": {"value-1", "value-2"}}, Secrets: map[string]string{"private_key": "secret"}, Body: []byte("body"), ReceivedAt: receivedAt,
	})
	if err != nil || verified.EventType != "customer.changed" || verified.Payload["customer_id"] != "customer-1" || verified.Security == nil || verified.Security.EventTime != "2026-07-22T17:02:03.000000004Z" || verified.ExternalIdentity.Subject != "subject" || verified.DeliveryReceipt.OccurredAt != "2026-07-22T17:02:03.000000004Z" {
		t.Fatalf("verified=%#v err=%v", verified, err)
	}
	if !reflect.DeepEqual(provider.webhookRequest.Headers["signature"], []string{"sig-1", "sig-2"}) || !reflect.DeepEqual(provider.webhookRequest.Query["code"], []string{"value-1", "value-2"}) || string(provider.webhookRequest.Body) != "body" || !provider.webhookRequest.ReceivedAt.Equal(receivedAt) {
		t.Fatalf("webhook request=%#v", provider.webhookRequest)
	}

	reconciler, ok := adapter.(integrationcontract.Reconciler)
	if !ok {
		t.Fatal("declared public Reconciler was lost")
	}
	reconciled, err := reconciler.Reconcile(t.Context(), integrationcontract.ReconcileRequest{
		ConnectorKey: "crm", Connection: connection, Operation: "push", ContractSHA256: publicBridgeContractSHA256,
		Request: map[string]any{"value": "customer-1"}, RequestRef: "request-ref", ResponseRef: "partial-ref", Secrets: map[string]string{"private_key": "secret"}, Timeout: 2 * time.Second, Principal: principal,
	})
	if err != nil || reconciled.Outcome != integrationcontract.ReconciliationSucceeded || reconciled.Response["state"] != "completed" || reconciled.ResponseRef != "reconciled-ref" || reconciled.SecretUpdates["private_key"] != "reconciled" {
		t.Fatalf("reconcile result=%#v err=%v", reconciled, err)
	}
	if provider.reconcileRequest.OperationKey != "push" || provider.reconcileRequest.ContractSHA256 != publicBridgeContractSHA256 || string(provider.reconcileRequest.Payload) != `{"value":"customer-1"}` || provider.reconcileRequest.RequestRef != "request-ref" || provider.reconcileRequest.ResponseRef != "partial-ref" || provider.reconcileRequest.Principal.CorrelationID != "correlation" {
		t.Fatalf("reconcile request=%#v", provider.reconcileRequest)
	}
	if _, err := reconciler.Reconcile(t.Context(), integrationcontract.ReconcileRequest{ConnectorKey: "crm", Connection: connection, Operation: "push", ContractSHA256: strings.Repeat("b", 64), Request: map[string]any{}, RequestRef: "request-ref"}); err == nil || !strings.Contains(err.Error(), "contract mismatch") {
		t.Fatalf("stale reconciliation contract accepted: %v", err)
	}
}

func TestPublicConnectorProviderBridgePreservesCapabilityAbsenceAndErrorClassification(t *testing.T) {
	provider := &publicBridgeProvider{descriptor: publicBridgeDescriptor(false)}
	_, adapter := registerPublicBridgeProvider(t, callOnlyPublicBridgeProvider{delegate: provider})
	if _, ok := adapter.(integrationcontract.ConnectionTester); ok {
		t.Fatal("call-only provider gained ConnectionTester")
	}
	if _, ok := adapter.(integrationcontract.WebhookVerifier); ok {
		t.Fatal("call-only provider gained WebhookVerifier")
	}
	if _, ok := adapter.(integrationcontract.Reconciler); ok {
		t.Fatal("call-only provider gained Reconciler")
	}
	request := integrationcontract.CallRequest{ConnectorKey: "crm", Connection: publicBridgeConnection(), Operation: "push", Request: map[string]any{}, Secrets: map[string]string{"private_key": "secret"}}
	for _, test := range []struct {
		err      error
		category integrationpolicy.ErrorCategory
		code     string
	}{
		{connector.RetryableError("acme.rate_limited", errors.New("detail")), integrationpolicy.ErrorProviderRetryable, "acme.rate_limited"},
		{connector.PermanentError("acme.invalid_request", errors.New("detail")), integrationpolicy.ErrorProviderPermanent, "acme.invalid_request"},
		{connector.UncertainError("acme.outcome_unknown", errors.New("detail")), integrationpolicy.ErrorProviderUncertain, "acme.outcome_unknown"},
	} {
		provider.callErr = test.err
		_, err := adapter.Call(t.Context(), request)
		if category, ok := integrationpolicy.ErrorCategoryOf(err); !ok || category != test.category || integrationpolicy.ProviderErrorCode(err) != test.code || !errors.Is(err, test.err) {
			t.Fatalf("mapped error=%v category=%q code=%q", err, category, integrationpolicy.ProviderErrorCode(err))
		}
	}
	provider.callErr = nil
	wrong := request
	wrong.Connection.ProviderKey = "other"
	if _, err := adapter.Call(t.Context(), wrong); err == nil || !strings.Contains(err.Error(), "provider mismatch") {
		t.Fatalf("mismatched pair accepted: %v", err)
	}
}

func TestPublicConnectorProviderBridgePreservesEveryOptionalCapabilityCombination(t *testing.T) {
	delegate := &publicBridgeProvider{descriptor: publicBridgeDescriptor(false)}
	base := callOnlyPublicBridgeProvider{delegate: delegate}
	for _, test := range []struct {
		name                          string
		provider                      connector.Adapter
		wantTest, wantHook, wantRecon bool
	}{
		{name: "none", provider: base},
		{name: "test", provider: struct {
			connector.Adapter
			connector.ConnectionTester
		}{base, delegate}, wantTest: true},
		{name: "hook", provider: struct {
			connector.Adapter
			connector.WebhookVerifier
		}{base, delegate}, wantHook: true},
		{name: "reconcile", provider: struct {
			connector.Adapter
			connector.Reconciler
		}{base, delegate}, wantRecon: true},
		{name: "test-hook", provider: struct {
			connector.Adapter
			connector.ConnectionTester
			connector.WebhookVerifier
		}{base, delegate, delegate}, wantTest: true, wantHook: true},
		{name: "test-reconcile", provider: struct {
			connector.Adapter
			connector.ConnectionTester
			connector.Reconciler
		}{base, delegate, delegate}, wantTest: true, wantRecon: true},
		{name: "hook-reconcile", provider: struct {
			connector.Adapter
			connector.WebhookVerifier
			connector.Reconciler
		}{base, delegate, delegate}, wantHook: true, wantRecon: true},
		{name: "all", provider: delegate, wantTest: true, wantHook: true, wantRecon: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, adapter := registerPublicBridgeProvider(t, test.provider)
			_, hasTest := adapter.(integrationcontract.ConnectionTester)
			_, hasHook := adapter.(integrationcontract.WebhookVerifier)
			_, hasRecon := adapter.(integrationcontract.Reconciler)
			if hasTest != test.wantTest || hasHook != test.wantHook || hasRecon != test.wantRecon {
				t.Fatalf("capabilities test=%v hook=%v reconcile=%v", hasTest, hasHook, hasRecon)
			}
		})
	}
}
