package connectors

import (
	"context"
	"time"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// Adapter is the narrow legacy-shaped test double accepted by Provider. It is
// deliberately testkit-owned so Runtime production domains do not regain a
// provider execution contract.
type Adapter interface {
	Call(context.Context, CallRequest) (CallResult, error)
}

type ConfigValidator interface {
	ValidateConfig(integrationsdk.Connection) error
}

type ConnectionTester interface {
	TestConnection(context.Context, CallRequest) (CallResult, error)
}

// ProviderSchema is testkit-owned metadata used only to adapt legacy-shaped
// test doubles to the public Connector SDK. It is not a Runtime application
// schema or a source Connector definition.
type ProviderSchema struct {
	ProviderRevision string
	ConfigFields     []definitionmodel.FieldSchema
	SecretFields     []definitionmodel.FieldSchema
	OperationKeys    []string
}

type SchemaProvider interface {
	ProviderSchema() ProviderSchema
}

type CallRequest struct {
	ConnectorKey string
	Connection   integrationsdk.Connection
	Operation    string
	Method       string
	Request      map[string]any
	RequestRef   string
	Headers      map[string]string
	Secrets      map[string]string
	Delivery     bool
	Timeout      time.Duration
	Principal    principalmodel.Principal
}

type CallResult struct {
	Response       map[string]any
	ResponseRef    string
	SecretUpdates  map[string]string
	ResourceHealth *integrationsdk.ProviderResourceHealth
}

type InboundWebhookRequest struct {
	Connection   integrationsdk.Connection
	Headers      map[string]string
	Query        map[string]string
	HeaderValues map[string][]string
	QueryValues  map[string][]string
	Secrets      map[string]string
	Body         []byte
	ReceivedAt   time.Time
}

type VerifiedInboundWebhook struct {
	EventType        string
	ExternalID       string
	Payload          map[string]any
	Security         *WebhookSecurityEvidence
	Challenge        string
	ChallengeFormat  string
	ExternalIdentity *WebhookExternalIdentity
	DeliveryReceipt  *WebhookDeliveryReceipt
}

type WebhookSecurityEvidence struct {
	SignatureVerified bool
	Nonce             string
	DeviceIdentity    string
	EventTime         string
}

type WebhookExternalIdentity struct {
	Subject     string
	SubjectType string
	Name        string
	Group       string
}

type WebhookDeliveryReceipt struct {
	ResponseRef string
	Status      string
	Error       string
	OccurredAt  string
}

type WebhookVerifier interface {
	VerifyWebhook(context.Context, InboundWebhookRequest) (VerifiedInboundWebhook, error)
}
