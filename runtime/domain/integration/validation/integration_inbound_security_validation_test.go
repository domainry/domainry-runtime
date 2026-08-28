package validation

import (
	"testing"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestIntegrationDeviceInboundSecurityRequiresCompleteProviderNeutralEvidence(t *testing.T) {
	now := time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC)
	policy := integrationmodel.IntegrationInboundSecurityPolicy{Profile: integrationmodel.IntegrationInboundSecurityProfileDevice, MaxSkewSeconds: 60}
	valid := integrationmodel.IntegrationInboundSecurityEvidence{SignatureVerified: true, Nonce: "nonce-1", DeviceIdentity: "device-1", ExternalID: "event-1", EventTime: now.Format(time.RFC3339Nano), Now: now}
	if result := IntegrationValidateInboundSecurity(policy, valid); !result.Valid || !result.EventTime.Equal(now) {
		t.Fatalf("valid evidence rejected: %#v", result)
	}
	tests := []struct {
		name string
		edit func(*integrationmodel.IntegrationInboundSecurityEvidence)
		want string
	}{
		{name: "signature", edit: func(value *integrationmodel.IntegrationInboundSecurityEvidence) { value.SignatureVerified = false }, want: "signature_required"},
		{name: "nonce", edit: func(value *integrationmodel.IntegrationInboundSecurityEvidence) { value.Nonce = "" }, want: "nonce_required"},
		{name: "device", edit: func(value *integrationmodel.IntegrationInboundSecurityEvidence) { value.DeviceIdentity = "" }, want: "device_identity_required"},
		{name: "external id", edit: func(value *integrationmodel.IntegrationInboundSecurityEvidence) { value.ExternalID = "" }, want: "external_event_id_required"},
		{name: "event time", edit: func(value *integrationmodel.IntegrationInboundSecurityEvidence) { value.EventTime = "invalid" }, want: "event_time_invalid"},
		{name: "stale", edit: func(value *integrationmodel.IntegrationInboundSecurityEvidence) {
			value.EventTime = now.Add(-61 * time.Second).Format(time.RFC3339Nano)
		}, want: "event_time_out_of_range"},
		{name: "future", edit: func(value *integrationmodel.IntegrationInboundSecurityEvidence) {
			value.EventTime = now.Add(61 * time.Second).Format(time.RFC3339Nano)
		}, want: "event_time_out_of_range"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.edit(&candidate)
			if result := IntegrationValidateInboundSecurity(policy, candidate); result.Valid || result.Failure != test.want {
				t.Fatalf("result=%#v want failure=%s", result, test.want)
			}
		})
	}
	if result := IntegrationValidateInboundSecurity(integrationmodel.IntegrationInboundSecurityPolicy{}, integrationmodel.IntegrationInboundSecurityEvidence{}); !result.Valid {
		t.Fatalf("ordinary webhook profile unexpectedly rejected: %#v", result)
	}
}
