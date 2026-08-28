package validation

import (
	"testing"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"github.com/domainry/domainry-runtime/runtime/platform/webhooksignature"
)

func TestIntegrationNormalizeWebhookSignatureAlgorithm(t *testing.T) {
	if got := IntegrationNormalizeWebhookSignatureAlgorithm(" hmac_sha256_hex "); got != "hmac_sha256_hex" {
		t.Fatalf("normalized algorithm = %q", got)
	}
	if got := IntegrationNormalizeWebhookSignatureAlgorithm("sha1"); got != "" {
		t.Fatalf("unsupported algorithm = %q, want empty", got)
	}
}

func TestIntegrationVerifyWebhookSignatureClassifiesFailures(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	base := integrationmodel.IntegrationWebhookSignatureCheck{
		Algorithm: "hmac_sha256_hex", Secret: "secret", Timestamp: "1700000000",
		Nonce: "nonce", Body: []byte("payload"), MaxSkewSeconds: 60, Now: now,
	}
	base.Signature = webhooksignature.Compute(base.Algorithm, base.Secret, base.Timestamp, base.Nonce, base.Body)

	tests := []struct {
		name   string
		mutate func(*integrationmodel.IntegrationWebhookSignatureCheck)
		want   string
	}{
		{name: "valid", mutate: func(*integrationmodel.IntegrationWebhookSignatureCheck) {}, want: ""},
		{name: "algorithm", mutate: func(check *integrationmodel.IntegrationWebhookSignatureCheck) { check.Algorithm = "sha1" }, want: "invalid_algorithm"},
		{name: "timestamp", mutate: func(check *integrationmodel.IntegrationWebhookSignatureCheck) { check.Timestamp = "bad" }, want: "invalid_timestamp"},
		{name: "skew", mutate: func(check *integrationmodel.IntegrationWebhookSignatureCheck) { check.Timestamp = "1699999939" }, want: "timestamp_out_of_range"},
		{name: "nonce", mutate: func(check *integrationmodel.IntegrationWebhookSignatureCheck) { check.Nonce = "" }, want: "missing_nonce"},
		{name: "signature", mutate: func(check *integrationmodel.IntegrationWebhookSignatureCheck) { check.Signature = "bad" }, want: "invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			check := base
			test.mutate(&check)
			result := IntegrationVerifyWebhookSignature(check)
			if result.Algorithm != webhooksignature.NormalizeAlgorithm(check.Algorithm) || result.Failure != test.want {
				t.Fatalf("result = %#v, want failure %q", result, test.want)
			}
		})
	}
}
