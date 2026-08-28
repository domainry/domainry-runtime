package policy

import (
	"encoding/json"
	"strings"
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestIntegrationCredentialReceiptContractNeverPersistsSecretOrToken(t *testing.T) {
	request := integrationmodel.IntegrationSecretUpsertRequest{Kind: "api_token", Value: "plain-secret", ValueRef: "vault:primary"}
	fingerprint, err := IntegrationSecretMutationFingerprint("integration.secret.rotate", "primary", request, []byte("runtime-owned-pepper"))
	if err != nil || fingerprint == "" || strings.Contains(fingerprint, request.Value) {
		t.Fatalf("fingerprint=%q err=%v", fingerprint, err)
	}
	secretReplay, _ := json.Marshal(IntegrationSecretReplay(integrationmodel.IntegrationSecret{Key: "primary", Status: "active", Fingerprint: "sha256:metadata", ValueRef: request.ValueRef, RotatedAt: "2026-07-19T00:00:00Z"}))
	apiKeyReplay, _ := json.Marshal(IntegrationAPIKeyReplay(integrationmodel.IntegrationAPIKey{Key: "service", Status: "active", TokenHash: "hash", TokenPrefix: "vrt_"}))
	combined := string(secretReplay) + string(apiKeyReplay)
	for _, forbidden := range []string{request.Value, request.ValueRef, "TokenHash", "hash", "vrt_"} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("receipt exposed %q: %s", forbidden, combined)
		}
	}
}

func TestIntegrationSecretMutationFingerprintRequiresPepper(t *testing.T) {
	if _, err := IntegrationSecretMutationFingerprint("integration.secret.rotate", "primary", integrationmodel.IntegrationSecretUpsertRequest{Value: "secret"}, nil); err == nil {
		t.Fatal("missing pepper must fail")
	}
}
