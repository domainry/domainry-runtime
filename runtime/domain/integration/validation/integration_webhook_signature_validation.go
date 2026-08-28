package validation

import (
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"github.com/domainry/domainry-runtime/runtime/platform/webhooksignature"
)

func IntegrationNormalizeWebhookSignatureAlgorithm(value string) string {
	return webhooksignature.NormalizeAlgorithm(value)
}

func IntegrationVerifyWebhookSignature(check integrationmodel.IntegrationWebhookSignatureCheck) integrationmodel.IntegrationWebhookSignatureCheckResult {
	algorithm := webhooksignature.NormalizeAlgorithm(check.Algorithm)
	result := integrationmodel.IntegrationWebhookSignatureCheckResult{Algorithm: algorithm}
	if algorithm == "" {
		result.Failure = "invalid_algorithm"
		return result
	}
	err := webhooksignature.Verify(webhooksignature.Verification{Algorithm: algorithm, Secret: check.Secret, Timestamp: check.Timestamp, Nonce: check.Nonce, Signature: check.Signature, Body: check.Body, MaxSkewSeconds: check.MaxSkewSeconds, Now: check.Now})
	switch {
	case err == nil:
	case webhooksignature.IsError(err, webhooksignature.ErrorInvalidTimestamp):
		result.Failure = "invalid_timestamp"
	case webhooksignature.IsError(err, webhooksignature.ErrorTimestampOutOfRange):
		result.Failure = "timestamp_out_of_range"
	case webhooksignature.IsError(err, webhooksignature.ErrorMissingNonce):
		result.Failure = "missing_nonce"
	default:
		result.Failure = "invalid"
	}
	return result
}
