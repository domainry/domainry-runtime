package integrationcontract

import (
	"context"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

type InboundWebhookRequest struct {
	Connection   integrationmodel.IntegrationConnection
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

// WebhookSecurityEvidence is provider-neutral proof produced by a registered
// Adapter after it verifies an inbound request. Device security profiles use
// this evidence to enforce replay protection before an IntegrationEvent is
// persisted or dispatched to any business owner.
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
