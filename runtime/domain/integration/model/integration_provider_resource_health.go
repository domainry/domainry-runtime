package integrationmodel

// IntegrationProviderResourceHealth is a provider-normalized, safe health
// observation. It deliberately excludes raw balances, currency, provider
// bodies and credentials; absence means the provider has no reliable signal.
type IntegrationProviderResourceHealth struct {
	ObservationID     string `json:"observation_id"`
	Kind              string `json:"kind"`
	State             string `json:"state"`
	PreviousState     string `json:"previous_state,omitempty"`
	EvidenceSource    string `json:"evidence_source"`
	QuotaUsedPercent  *int   `json:"quota_used_percent,omitempty"`
	BalanceBand       string `json:"balance_band,omitempty"`
	CapabilityBlocked bool   `json:"capability_blocked"`
	ObservedAt        string `json:"observed_at"`
	ErrorCode         string `json:"error_code,omitempty"`
}

const (
	IntegrationResourceKindQuota   = "quota"
	IntegrationResourceKindBilling = "billing"

	IntegrationResourceStateHealthy         = "healthy"
	IntegrationResourceStateWarning         = "warning"
	IntegrationResourceStateExhausted       = "exhausted"
	IntegrationResourceStatePaymentRequired = "payment_required"

	IntegrationResourceEvidenceProviderAPI   = "provider_api"
	IntegrationResourceEvidenceProviderError = "provider_error"
)
