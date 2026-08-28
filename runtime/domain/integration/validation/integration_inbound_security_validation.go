package validation

import (
	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func IntegrationValidateInboundSecurity(policy integrationmodel.IntegrationInboundSecurityPolicy, evidence integrationmodel.IntegrationInboundSecurityEvidence) integrationmodel.IntegrationInboundSecurityValidation {
	if strings.TrimSpace(policy.Profile) != integrationmodel.IntegrationInboundSecurityProfileDevice {
		return integrationmodel.IntegrationInboundSecurityValidation{Valid: true}
	}
	if !evidence.SignatureVerified {
		return integrationmodel.IntegrationInboundSecurityValidation{Failure: "signature_required"}
	}
	if strings.TrimSpace(evidence.Nonce) == "" {
		return integrationmodel.IntegrationInboundSecurityValidation{Failure: "nonce_required"}
	}
	if strings.TrimSpace(evidence.DeviceIdentity) == "" {
		return integrationmodel.IntegrationInboundSecurityValidation{Failure: "device_identity_required"}
	}
	if strings.TrimSpace(evidence.ExternalID) == "" {
		return integrationmodel.IntegrationInboundSecurityValidation{Failure: "external_event_id_required"}
	}
	eventTime, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(evidence.EventTime))
	if err != nil {
		return integrationmodel.IntegrationInboundSecurityValidation{Failure: "event_time_invalid"}
	}
	now := evidence.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	maxSkew := time.Duration(policy.MaxSkewSeconds) * time.Second
	if maxSkew <= 0 {
		maxSkew = 5 * time.Minute
	}
	if eventTime.Before(now.Add(-maxSkew)) || eventTime.After(now.Add(maxSkew)) {
		return integrationmodel.IntegrationInboundSecurityValidation{Failure: "event_time_out_of_range"}
	}
	return integrationmodel.IntegrationInboundSecurityValidation{Valid: true, EventTime: eventTime.UTC()}
}
