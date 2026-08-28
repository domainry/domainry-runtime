package validation

import (
	"testing"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestIntegrationInboundSecurityUsesDefaultClockAndSkew(t *testing.T) {
	evidence := integrationmodel.IntegrationInboundSecurityEvidence{
		SignatureVerified: true,
		Nonce:             "nonce",
		DeviceIdentity:    "device",
		ExternalID:        "event",
		EventTime:         time.Now().UTC().Format(time.RFC3339Nano),
	}
	result := IntegrationValidateInboundSecurity(integrationmodel.IntegrationInboundSecurityPolicy{Profile: integrationmodel.IntegrationInboundSecurityProfileDevice}, evidence)
	if !result.Valid {
		t.Fatalf("result=%#v", result)
	}
}

func TestIntegrationReadLimitRejectsZeroBeforeMaximumCheck(t *testing.T) {
	if err := IntegrationValidateReadLimit(0, 100); err == nil {
		t.Fatal("zero limit was accepted")
	}
	if err := IntegrationValidateReadLimit(1, 100); err != nil {
		t.Fatal(err)
	}
	if err := IntegrationValidateReadLimit(101, 100); err == nil {
		t.Fatal("oversized limit was accepted")
	}
}
