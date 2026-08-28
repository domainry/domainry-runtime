// Integration application service webhook-signature tests.
package integration

import (
	"context"
	"errors"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type webhookSignatureEventRepository struct {
	integrationrepository.IntegrationEventRepository
	duplicate bool
	err       error
	nonce     string
}

func (r *webhookSignatureEventRepository) RecordWebhookNonce(_ context.Context, _, _, nonce, _ string, _ string) (bool, error) {
	r.nonce = nonce
	return r.duplicate, r.err
}

func TestVerifyIntegrationWebhookSignatureOwnsPolicyAndStrategy(t *testing.T) {
	t.Setenv("DOMAINRY_WEBHOOK_TEST_SECRET", "secret-value")
	service := NewIntegrationApplicationService(ApplicationDependencies{ConnectorExists: func(key string) bool { return key == "webhook" }, WebhookAlgorithmNormalizer: func(value string) string {
		if value == "sha256" {
			return "hmac_sha256"
		}
		return ""
	}, WebhookSignatureVerifier: func(check integrationmodel.IntegrationWebhookSignatureCheck) integrationmodel.IntegrationWebhookSignatureCheckResult {
		if check.Algorithm != "hmac_sha256" || check.Secret != "secret-value" || string(check.Body) != "payload" {
			t.Fatalf("check = %#v", check)
		}
		return integrationmodel.IntegrationWebhookSignatureCheckResult{Algorithm: check.Algorithm}
	}})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{PermissionInvoke}})

	result, err := service.VerifyIntegrationWebhookSignature(t.Context(), integrationmodel.IntegrationWebhookSignatureVerificationRequest{
		ConnectorKey: "webhook", SecretRef: "env:DOMAINRY_WEBHOOK_TEST_SECRET", Algorithm: "sha256", Body: []byte("payload"),
	}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid || result.Algorithm != "hmac_sha256" || result.SecretRefName != "webhook_secret" || result.Strategy["signature_header"] != "X-Integration-Signature" {
		t.Fatalf("result = %#v", result)
	}
}

func TestVerifyIntegrationWebhookSignatureRejectsAlgorithmBeforeSecretResolution(t *testing.T) {
	service := NewIntegrationApplicationService(ApplicationDependencies{ConnectorExists: func(string) bool { return true }})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{PermissionInvoke}})

	_, err := service.VerifyIntegrationWebhookSignature(t.Context(), integrationmodel.IntegrationWebhookSignatureVerificationRequest{
		ConnectorKey: "webhook", SecretRef: "env:MISSING", Algorithm: "unknown",
	}, principal)
	if integrationErrorCode(err) != "backend.integration.webhook_signature.invalid_algorithm" {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyIntegrationWebhookSignatureEdges(t *testing.T) {
	t.Setenv("DOMAINRY_WEBHOOK_EDGE_SECRET", "secret")
	connection := integrationmodel.IntegrationConnection{Key: "connection", WorkspaceID: "workspace", ConnectorKey: "webhook", Status: "active", Config: map[string]any{
		"signature_algorithm": "sha256", "signature_secret_ref_name": "custom", "max_skew_seconds": 30,
	}, SecretRefs: map[string]string{"custom": "env:DOMAINRY_WEBHOOK_EDGE_SECRET"}}
	config := &connectionResolutionRepo{connections: []integrationmodel.IntegrationConnection{connection}}
	events := &webhookSignatureEventRepository{}
	checkResult := integrationmodel.IntegrationWebhookSignatureCheckResult{}
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: config, EventRepository: events,
		ConnectorExists: func(key string) bool { return key == "webhook" },
		WebhookAlgorithmNormalizer: func(value string) string {
			if value == "sha256" || value == "hmac_sha256" {
				return "hmac_sha256"
			}
			return ""
		},
		WebhookSignatureVerifier: func(integrationmodel.IntegrationWebhookSignatureCheck) integrationmodel.IntegrationWebhookSignatureCheckResult {
			return checkResult
		},
	})
	principal := integrationManagementPrincipal(PermissionInvoke)
	request := integrationmodel.IntegrationWebhookSignatureVerificationRequest{ConnectorKey: "webhook", ConnectionKey: "connection", Signature: "signature", Timestamp: "timestamp", Nonce: "nonce"}
	if _, err := service.VerifyIntegrationWebhookSignature(t.Context(), request, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization error=%v", err)
	}
	if _, err := service.VerifyIntegrationWebhookSignature(t.Context(), request, integrationManagementPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission error=%v", err)
	}
	missingConnector := request
	missingConnector.ConnectorKey = " "
	if _, err := service.VerifyIntegrationWebhookSignature(t.Context(), missingConnector, principal); apperror.CodeOf(err) != "backend.integration.webhook_signature.missing_connector" {
		t.Fatalf("missing connector error=%v", err)
	}
	unknownConnector := request
	unknownConnector.ConnectorKey = "unknown"
	if _, err := service.VerifyIntegrationWebhookSignature(t.Context(), unknownConnector, principal); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("unknown connector error=%v", err)
	}
	config.listErr = errIntegrationManagementTest
	if _, err := service.VerifyIntegrationWebhookSignature(t.Context(), request, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("connection list error=%v", err)
	}
	config.listErr, config.connections = nil, nil
	if _, err := service.VerifyIntegrationWebhookSignature(t.Context(), request, principal); apperror.CodeOf(err) != "backend.integration.connection.not_found" {
		t.Fatalf("missing connection error=%v", err)
	}
	config.connections = []integrationmodel.IntegrationConnection{connection}
	config.connections[0].ConnectorKey = "other"
	if _, err := service.VerifyIntegrationWebhookSignature(t.Context(), request, principal); apperror.CodeOf(err) != "backend.integration.webhook_signature.connector_mismatch" {
		t.Fatalf("connector mismatch error=%v", err)
	}
	config.connections[0] = connection
	config.connections[0].Status = "disabled"
	if _, err := service.VerifyIntegrationWebhookSignature(t.Context(), request, principal); apperror.CodeOf(err) != "backend.integration.connection.disabled" {
		t.Fatalf("disabled connection error=%v", err)
	}
	config.connections[0] = connection
	missingSecret := integrationmodel.IntegrationWebhookSignatureVerificationRequest{ConnectorKey: "webhook", Algorithm: "sha256"}
	if _, err := service.VerifyIntegrationWebhookSignature(t.Context(), missingSecret, principal); apperror.CodeOf(err) != "backend.integration.webhook_signature.missing_secret_ref" {
		t.Fatalf("missing secret error=%v", err)
	}
	invalidRef := missingSecret
	invalidRef.SecretRef = "literal"
	if _, err := service.VerifyIntegrationWebhookSignature(t.Context(), invalidRef, principal); apperror.CodeOf(err) != "backend.integration.secret_ref_must_be_env" {
		t.Fatalf("invalid ref error=%v", err)
	}

	for failure, code := range map[string]string{
		"invalid_algorithm":      "backend.integration.webhook_signature.invalid_algorithm",
		"invalid_timestamp":      "backend.integration.webhook_signature.invalid_timestamp",
		"timestamp_out_of_range": "backend.integration.webhook_signature.timestamp_out_of_range",
		"missing_nonce":          "backend.integration.webhook_signature.missing_nonce",
		"invalid_signature":      "backend.integration.webhook_signature.invalid",
	} {
		checkResult = integrationmodel.IntegrationWebhookSignatureCheckResult{Algorithm: "normalized", Failure: failure}
		if _, err := service.VerifyIntegrationWebhookSignature(t.Context(), request, principal); apperror.CodeOf(err) != code {
			t.Fatalf("failure %q error=%v", failure, err)
		}
	}
	checkResult = integrationmodel.IntegrationWebhookSignatureCheckResult{Algorithm: "hmac_sha256"}
	events.err = errIntegrationManagementTest
	if _, err := service.VerifyIntegrationWebhookSignature(t.Context(), request, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("nonce repository error=%v", err)
	}
	events.err, events.duplicate = nil, true
	if _, err := service.VerifyIntegrationWebhookSignature(t.Context(), request, principal); apperror.CodeOf(err) != "backend.integration.webhook_signature.replay_detected" {
		t.Fatalf("replay error=%v", err)
	}
	events.duplicate = false
	result, err := service.VerifyIntegrationWebhookSignature(t.Context(), request, principal)
	if err != nil || !result.Valid || result.SecretRefName != "custom" || events.nonce != "nonce" || result.Strategy["replay_protection"] != true {
		t.Fatalf("result=%#v nonce=%q err=%v", result, events.nonce, err)
	}
	checkResult = integrationmodel.IntegrationWebhookSignatureCheckResult{}
	explicit := request
	explicit.Algorithm, explicit.SecretRefName, explicit.MaxSkewSeconds = "sha256", "custom", 15
	if result, err := service.VerifyIntegrationWebhookSignature(t.Context(), explicit, principal); err != nil || !result.Valid || result.Algorithm != "hmac_sha256" {
		t.Fatalf("explicit strategy result=%#v err=%v", result, err)
	}
}
