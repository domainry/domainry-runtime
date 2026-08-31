// Package connectors adapts internal connector test doubles to the public
// provider registry. Production composition must use real connector
// providers; this package exists only so cross-package tests do not restore the
// deleted legacy registration API.
package connectors

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	connector "github.com/domainry/domainry-connector-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type providerAdapter struct {
	descriptor connector.ProviderDescriptor
	operations map[string]connectormodel.ConnectorOperationSchema
	delegate   Adapter
}

type configValidator func(connector.Connection) error

func (validate configValidator) ValidateConfig(connection connector.Connection) error {
	return validate(connection)
}

type connectionTester func(context.Context, connector.TestConnectionRequest) (connector.TestConnectionResult, error)

func (test connectionTester) TestConnection(ctx context.Context, request connector.TestConnectionRequest) (connector.TestConnectionResult, error) {
	return test(ctx, request)
}

type webhookVerifier func(context.Context, connector.VerifyWebhookRequest) (connector.VerifiedWebhook, error)

func (verify webhookVerifier) VerifyWebhook(ctx context.Context, request connector.VerifyWebhookRequest) (connector.VerifiedWebhook, error) {
	return verify(ctx, request)
}

func (p *providerAdapter) Descriptor() connector.ProviderDescriptor { return p.descriptor }

func (p *providerAdapter) Call(ctx context.Context, request connector.CallRequest) (connector.CallResult, error) {
	operation, ok := p.operation(request.OperationKey)
	if !ok || request.ConnectorKey != p.descriptor.ConnectorKey || request.ProviderKey != p.descriptor.ProviderKey || request.ContractSHA256 != operation.ContractSHA256 || request.Mode != operation.Mode {
		return connector.CallResult{}, fmt.Errorf("connector test provider contract mismatch")
	}
	payload, err := decodePayload(request.Payload)
	if err != nil {
		return connector.CallResult{}, err
	}
	result, callErr := p.delegate.Call(ctx, integrationcontract.CallRequest{
		ConnectorKey: request.ConnectorKey, Connection: fromConnection(request.Connection), Operation: operation.Key,
		Method: p.operations[operation.Key].Method, Request: payload, RequestRef: request.RequestRef,
		Headers: cloneStrings(request.Headers), Secrets: cloneStrings(request.Secrets), Delivery: request.Delivery,
		Timeout: request.Timeout, Principal: fromPrincipal(request.Principal),
	})
	response, err := json.Marshal(result.Response)
	if err != nil {
		return connector.CallResult{}, err
	}
	return connector.CallResult{Payload: response, ResponseRef: result.ResponseRef, SecretUpdates: cloneStrings(result.SecretUpdates), ResourceHealth: resourceHealth(result.ResourceHealth)}, callErr
}

func resourceHealth(report *integrationsdk.ProviderResourceHealth) *connector.ResourceHealthReport {
	if report == nil {
		return nil
	}
	return &connector.ResourceHealthReport{
		ObservationID: report.ObservationID, Kind: report.Kind, State: report.State, PreviousState: report.PreviousState,
		EvidenceSource: report.EvidenceSource, QuotaUsedPercent: report.QuotaUsedPercent, BalanceBand: report.BalanceBand,
		CapabilityBlocked: report.CapabilityBlocked, ObservedAt: report.ObservedAt, ErrorCode: report.ErrorCode,
	}
}

func (p *providerAdapter) operation(key string) (connector.OperationDescriptor, bool) {
	for _, operation := range p.descriptor.Operations {
		if operation.Key == strings.TrimSpace(key) {
			return operation, true
		}
	}
	return connector.OperationDescriptor{}, false
}

// Provider wraps one internal test double as a public provider. Operations
// should come from the same Integration schema exercised by the test.
func Provider(connectorKey, providerKey string, adapter Adapter, operations []connectormodel.ConnectorOperationSchema, fallbackSchemas ...ProviderSchema) connector.Adapter {
	providerSchema := ProviderSchema{}
	if len(fallbackSchemas) > 0 {
		providerSchema = fallbackSchemas[0]
	}
	if schemaProvider, ok := adapter.(integrationcontract.SchemaProvider); ok {
		providerSchema = integrationprojection.IntegrationMergeConnectorProviderSchema(providerSchema, schemaProvider.ProviderSchema())
	}
	if len(providerSchema.OperationKeys) > 0 {
		available := make(map[string]connectormodel.ConnectorOperationSchema, len(operations))
		for _, operation := range operations {
			available[operation.Key] = operation
		}
		operations = operations[:0]
		for _, key := range providerSchema.OperationKeys {
			operation, ok := available[key]
			if !ok {
				operation = connectormodel.ConnectorOperationSchema{Key: key, ExecutionMode: "sync", SideEffect: "read"}
			}
			operations = append(operations, operation)
		}
	}
	if len(operations) == 0 {
		operations = []connectormodel.ConnectorOperationSchema{{Key: "test_connection", ExecutionMode: "sync", SideEffect: "read"}}
	}
	revision := strings.TrimSpace(providerSchema.ProviderRevision)
	if revision == "" {
		revision = "test-v1"
	}
	descriptor := connector.ProviderDescriptor{ConnectorKey: connectorKey, ProviderKey: providerKey, ProviderRevision: revision, ConfigFields: configFields(providerSchema.ConfigFields), SecretFields: secretFields(providerSchema.SecretFields)}
	byKey := make(map[string]connectormodel.ConnectorOperationSchema, len(operations))
	for _, operation := range operations {
		if operation.Key == "" {
			continue
		}
		mode := connector.ModeCall
		if operation.ExecutionMode == "async" {
			mode = connector.ModeEnqueue
		}
		effect := connector.OperationEffect(operation.SideEffect)
		if effect == "" {
			effect = connector.EffectRead
		}
		idempotency := connector.IdempotencyNone
		if effect == connector.EffectRead {
			idempotency = connector.IdempotencyNatural
		}
		sum := sha256.Sum256([]byte(connectorKey + "\x00" + providerKey + "\x00" + operation.Key))
		descriptor.Operations = append(descriptor.Operations, connector.OperationDescriptor{
			ConnectorKey: connectorKey, ProviderKey: providerKey, Key: operation.Key, Mode: mode, ContractSHA256: hex.EncodeToString(sum[:]),
			Reliability: connector.ReliabilityContract{
				Effect: effect, Idempotency: connector.IdempotencyContract{Strategy: idempotency},
				Reconciliation: connector.ReconciliationNone, Compensation: connector.CompensationContract{Mode: connector.CompensationNone},
			},
		})
		byKey[operation.Key] = operation
	}
	base := &providerAdapter{descriptor: descriptor, operations: byKey, delegate: adapter}
	validator, hasValidator := adapter.(integrationcontract.ConfigValidator)
	tester, hasTester := adapter.(integrationcontract.ConnectionTester)
	verifier, hasVerifier := adapter.(integrationcontract.WebhookVerifier)
	validate := configValidator(func(connection connector.Connection) error {
		return validator.ValidateConfig(fromConnection(connection))
	})
	test := connectionTester(func(ctx context.Context, request connector.TestConnectionRequest) (connector.TestConnectionResult, error) {
		result, err := tester.TestConnection(ctx, integrationcontract.CallRequest{ConnectorKey: connectorKey, Connection: fromConnection(request.Connection), Operation: "test_connection", Secrets: cloneStrings(request.Secrets), Timeout: request.Timeout, Principal: fromPrincipal(request.Principal)})
		if err != nil {
			return connector.TestConnectionResult{}, err
		}
		connected := true
		if value, ok := result.Response["connected"].(bool); ok {
			connected = value
		}
		details, err := json.Marshal(result.Response)
		return connector.TestConnectionResult{Connected: connected, Details: details, SecretUpdates: cloneStrings(result.SecretUpdates)}, err
	})
	verify := webhookVerifier(func(ctx context.Context, request connector.VerifyWebhookRequest) (connector.VerifiedWebhook, error) {
		result, err := verifier.VerifyWebhook(ctx, integrationcontract.InboundWebhookRequest{Connection: fromConnection(request.Connection), Headers: firstValues(request.Headers), Query: firstValues(request.Query), HeaderValues: cloneMultiStrings(request.Headers), QueryValues: cloneMultiStrings(request.Query), Secrets: cloneStrings(request.Secrets), Body: append([]byte(nil), request.Body...), ReceivedAt: request.ReceivedAt})
		if err != nil {
			return connector.VerifiedWebhook{}, err
		}
		payload, err := json.Marshal(result.Payload)
		if err != nil {
			return connector.VerifiedWebhook{}, err
		}
		return connector.VerifiedWebhook{EventType: result.EventType, ExternalID: result.ExternalID, Payload: payload, Security: webhookSecurity(result.Security), Challenge: result.Challenge, ChallengeFormat: result.ChallengeFormat, ExternalIdentity: webhookIdentity(result.ExternalIdentity), DeliveryReceipt: webhookReceipt(result.DeliveryReceipt)}, nil
	})
	return combine(base, validate, test, verify, hasValidator, hasTester, hasVerifier)
}

func configFields(fields []definitionmodel.FieldSchema) []connector.ConfigField {
	result := make([]connector.ConfigField, 0, len(fields))
	for _, field := range fields {
		name := strings.TrimSpace(field.Name)
		if name == "" {
			name = field.Key
		}
		var defaultValue json.RawMessage
		if field.Default != nil {
			defaultValue, _ = json.Marshal(field.Default)
		}
		options := append([]string(nil), field.Validation.Options...)
		if len(options) == 0 {
			options = stringSlice(field.Options)
		}
		fieldType := connector.ConfigFieldType(field.Type)
		if fieldType == connector.ConfigFieldText && len(options) > 0 {
			fieldType = connector.ConfigFieldSelect
		}
		result = append(result, connector.ConfigField{
			Key: field.Key, Name: name, Description: field.Description, Type: fieldType,
			I18n: localization(field.I18n), Required: field.Required, Default: defaultValue,
			Validation:   connector.ConfigValidation{MinLength: field.Validation.MinLength, MaxLength: field.Validation.MaxLength, Min: cloneFloat(field.Validation.Min), Max: cloneFloat(field.Validation.Max), Pattern: field.Validation.Pattern, Options: options},
			RequiredWith: stringSlice(field.Config["required_with"]),
		})
	}
	return result
}

func secretFields(fields []definitionmodel.FieldSchema) []connector.SecretField {
	result := make([]connector.SecretField, 0, len(fields))
	for _, field := range fields {
		name := strings.TrimSpace(field.Name)
		if name == "" {
			name = field.Key
		}
		result = append(result, connector.SecretField{
			Key: field.Key, Name: name, Description: field.Description, I18n: localization(field.I18n), Required: field.Required,
			CredentialKind:  connector.SecretCredentialKind(configString(field.Config, "credential_kind", string(connector.SecretCredentialGeneric))),
			MaterialFormat:  connector.SecretMaterialFormat(configString(field.Config, "material_format", string(connector.SecretMaterialOpaque))),
			RotationPolicy:  connector.SecretRotationPolicy(configString(field.Config, "rotation_policy", string(connector.SecretRotationManual))),
			ExpiryPolicy:    connector.SecretExpiryPolicy(configString(field.Config, "expiry_policy", string(connector.SecretExpiryOptional))),
			TestRequirement: connector.SecretTestRequirement(configString(field.Config, "test_requirement", string(connector.SecretTestWhenBound))),
		})
	}
	return result
}

func combine(base *providerAdapter, validate configValidator, test connectionTester, verify webhookVerifier, hasValidator, hasTester, hasVerifier bool) connector.Adapter {
	switch {
	case hasValidator && hasTester && hasVerifier:
		return struct {
			*providerAdapter
			configValidator
			connectionTester
			webhookVerifier
		}{base, validate, test, verify}
	case hasValidator && hasTester:
		return struct {
			*providerAdapter
			configValidator
			connectionTester
		}{base, validate, test}
	case hasValidator && hasVerifier:
		return struct {
			*providerAdapter
			configValidator
			webhookVerifier
		}{base, validate, verify}
	case hasTester && hasVerifier:
		return struct {
			*providerAdapter
			connectionTester
			webhookVerifier
		}{base, test, verify}
	case hasValidator:
		return struct {
			*providerAdapter
			configValidator
		}{base, validate}
	case hasTester:
		return struct {
			*providerAdapter
			connectionTester
		}{base, test}
	case hasVerifier:
		return struct {
			*providerAdapter
			webhookVerifier
		}{base, verify}
	default:
		return base
	}
}

func Registry(providers ...connector.Adapter) *connector.Registry {
	registry := connector.NewRegistry()
	if err := registry.RegisterProviderSet(connector.ProviderSet{Providers: providers}); err != nil {
		panic(err)
	}
	registry.Freeze()
	return registry
}

func fromConnection(connection connector.Connection) integrationsdk.Connection {
	return integrationsdk.Connection{Key: connection.Key, WorkspaceID: connection.WorkspaceID, ConnectorKey: connection.ConnectorKey, ProviderKey: connection.ProviderKey, Name: connection.Name, Status: connection.Status, Config: cloneAny(connection.Config), SecretRefs: cloneStrings(connection.SecretRefs), CreatedBy: connection.CreatedBy, CreatedAt: connection.CreatedAt, UpdatedAt: connection.UpdatedAt}
}

func fromPrincipal(principal connector.Principal) principalmodel.Principal {
	return principalmodel.Principal{Principal: identitysdk.Principal{UserID: principal.UserID, WorkspaceID: principal.WorkspaceID, DepartmentID: principal.DepartmentID, RoleKey: principal.RoleKey, Known: principal.IsAuthenticated}, RequestID: principal.RequestID, CorrelationID: principal.CorrelationID, CausationID: principal.CausationID, SurfaceKey: principal.SurfaceKey}
}

func decodePayload(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, nil
	}
	var result map[string]any
	err := json.Unmarshal(raw, &result)
	return result, err
}

func cloneStrings(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func cloneAny(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func localization(values map[string]map[string]string) map[string]connector.FieldLocalization {
	if values == nil {
		return nil
	}
	result := make(map[string]connector.FieldLocalization, len(values))
	for locale, value := range values {
		result[locale] = connector.FieldLocalization{Name: value["name"], Description: value["description"]}
	}
	return result
}

func configString(config map[string]any, key, fallback string) string {
	if value, ok := config[key].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func stringSlice(value any) []string {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneMultiStrings(input map[string][]string) map[string][]string {
	if input == nil {
		return nil
	}
	result := make(map[string][]string, len(input))
	for key, value := range input {
		result[key] = append([]string(nil), value...)
	}
	return result
}

func firstValues(input map[string][]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		if len(value) > 0 {
			result[key] = value[0]
		}
	}
	return result
}

func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	return parsed
}

func webhookSecurity(value *integrationcontract.WebhookSecurityEvidence) *connector.WebhookSecurityEvidence {
	if value == nil {
		return nil
	}
	return &connector.WebhookSecurityEvidence{SignatureVerified: value.SignatureVerified, Nonce: value.Nonce, DeviceIdentity: value.DeviceIdentity, EventTime: parseTime(value.EventTime)}
}

func webhookIdentity(value *integrationcontract.WebhookExternalIdentity) *connector.WebhookExternalIdentity {
	if value == nil {
		return nil
	}
	return &connector.WebhookExternalIdentity{Subject: value.Subject, SubjectType: value.SubjectType, Name: value.Name, Group: value.Group}
}

func webhookReceipt(value *integrationcontract.WebhookDeliveryReceipt) *connector.WebhookDeliveryReceipt {
	if value == nil {
		return nil
	}
	return &connector.WebhookDeliveryReceipt{ResponseRef: value.ResponseRef, Status: value.Status, Error: value.Error, OccurredAt: parseTime(value.OccurredAt)}
}
