package policy

import (
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

const IntegrationCredentialMutationReceiptRetention = 90 * 24 * time.Hour

type IntegrationCredentialReplay struct {
	Key              string `json:"key"`
	Status           string `json:"status"`
	Fingerprint      string `json:"fingerprint,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
	RotatedAt        string `json:"rotated_at,omitempty"`
	TokenRecoverable bool   `json:"token_recoverable"`
}

func IntegrationSecretMutationFingerprint(useCase, secretKey string, request integrationmodel.IntegrationSecretUpsertRequest, pepper []byte) (string, error) {
	valueDigest, err := idempotency.SensitiveValueDigest(pepper, request.Value)
	if err != nil {
		return "", err
	}
	return idempotency.Fingerprint(idempotency.FingerprintInput{
		UseCase: useCase, ResourceType: "integration_secret", TargetID: secretKey,
		Payload: map[string]any{"kind": request.Kind, "status": request.Status, "description": request.Description, "value_ref": request.ValueRef, "value_digest": valueDigest, "expires_at": request.ExpiresAt},
	})
}

func IntegrationSecretReplay(secret integrationmodel.IntegrationSecret) IntegrationCredentialReplay {
	return IntegrationCredentialReplay{Key: secret.Key, Status: secret.Status, Fingerprint: secret.Fingerprint, UpdatedAt: secret.UpdatedAt, RotatedAt: secret.RotatedAt, TokenRecoverable: false}
}

func IntegrationAPIKeyReplay(key integrationmodel.IntegrationAPIKey) IntegrationCredentialReplay {
	return IntegrationCredentialReplay{Key: key.Key, Status: key.Status, UpdatedAt: key.UpdatedAt, TokenRecoverable: false}
}
