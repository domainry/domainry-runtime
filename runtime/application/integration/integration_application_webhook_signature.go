package integration

import (
	"context"
	"encoding/json"
	"fmt"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strconv"
	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (s *IntegrationApplicationService) VerifyIntegrationWebhookSignature(ctx context.Context, req integrationmodel.IntegrationWebhookSignatureVerificationRequest, principal principalmodel.Principal) (integrationmodel.IntegrationWebhookSignatureVerificationResult, error) {
	if err := integrationAuthorizeQuery(principal); err != nil {
		return integrationmodel.IntegrationWebhookSignatureVerificationResult{}, err
	}
	if !HasPermission(principal, PermissionInvoke) {
		return integrationmodel.IntegrationWebhookSignatureVerificationResult{}, forbidden("auth.permission_denied")
	}
	connectorKey := strings.TrimSpace(req.ConnectorKey)
	if connectorKey == "" {
		return integrationmodel.IntegrationWebhookSignatureVerificationResult{}, badRequest("backend.integration.webhook_signature.missing_connector")
	}
	if !s.connectorExists(connectorKey) {
		return integrationmodel.IntegrationWebhookSignatureVerificationResult{}, notFound("backend.integration.connector.not_found")
	}
	secretRefName, secretRef := strings.TrimSpace(req.SecretRefName), strings.TrimSpace(req.SecretRef)
	connectionKey := strings.TrimSpace(req.ConnectionKey)
	var strategy map[string]any
	if secretRef == "" && connectionKey != "" {
		connection, ok, err := s.findConnection(ctx, connectionKey, principalWorkspaceID(principal))
		if err != nil {
			return integrationmodel.IntegrationWebhookSignatureVerificationResult{}, err
		}
		if !ok {
			return integrationmodel.IntegrationWebhookSignatureVerificationResult{}, notFound("backend.integration.connection.not_found")
		}
		if connection.ConnectorKey != connectorKey {
			return integrationmodel.IntegrationWebhookSignatureVerificationResult{}, badRequest("backend.integration.webhook_signature.connector_mismatch")
		}
		if connection.Status == "disabled" {
			return integrationmodel.IntegrationWebhookSignatureVerificationResult{}, badRequest("backend.integration.connection.disabled")
		}
		strategy = webhookSignatureStrategyFromConnection(connection)
		if strings.TrimSpace(req.Algorithm) == "" {
			req.Algorithm = strings.TrimSpace(fmt.Sprint(strategy["algorithm"]))
		}
		if secretRefName == "" {
			secretRefName = strings.TrimSpace(fmt.Sprint(strategy["secret_ref_name"]))
		}
		if req.MaxSkewSeconds <= 0 {
			req.MaxSkewSeconds = integrationConfigInt64(connection.Config, 0, "webhook_max_skew_seconds", "signature_max_skew_seconds", "max_skew_seconds")
		}
		secretRef = strings.TrimSpace(connection.SecretRefs[secretRefName])
	}
	if secretRefName == "" {
		secretRefName = "webhook_secret"
	}
	algorithm := strings.TrimSpace(s.normalizeWebhookAlgorithm(req.Algorithm))
	if algorithm == "" {
		return integrationmodel.IntegrationWebhookSignatureVerificationResult{}, badRequest("backend.integration.webhook_signature.invalid_algorithm")
	}
	if secretRef == "" {
		return integrationmodel.IntegrationWebhookSignatureVerificationResult{}, badRequest("backend.integration.webhook_signature.missing_secret_ref")
	}
	secretValue, err := ResolveSecretRef(secretRef)
	if err != nil {
		return integrationmodel.IntegrationWebhookSignatureVerificationResult{}, err
	}
	now := time.Now().UTC()
	check := s.verifyWebhookSignature(integrationmodel.IntegrationWebhookSignatureCheck{
		Algorithm: algorithm, Secret: secretValue, Timestamp: req.Timestamp, Nonce: req.Nonce,
		Signature: req.Signature, Body: req.Body, MaxSkewSeconds: req.MaxSkewSeconds, Now: now,
	})
	if normalized := strings.TrimSpace(check.Algorithm); normalized != "" {
		algorithm = normalized
	}
	if check.Failure == "invalid_algorithm" {
		return integrationmodel.IntegrationWebhookSignatureVerificationResult{}, badRequest("backend.integration.webhook_signature.invalid_algorithm")
	}
	if strategy == nil {
		strategy = webhookSignatureStrategy(algorithm, secretRefName, req.MaxSkewSeconds, "", "", "")
	} else {
		strategy["algorithm"], strategy["secret_ref_name"] = algorithm, secretRefName
		strategy["max_skew_seconds"], strategy["replay_protection"] = req.MaxSkewSeconds, req.MaxSkewSeconds > 0
	}
	invalid := integrationmodel.IntegrationWebhookSignatureVerificationResult{Valid: false, ConnectorKey: connectorKey, ConnectionKey: connectionKey, SecretRefName: secretRefName, Algorithm: algorithm, Strategy: strategy}
	switch check.Failure {
	case "":
	case "invalid_timestamp":
		return integrationmodel.IntegrationWebhookSignatureVerificationResult{}, badRequest("backend.integration.webhook_signature.invalid_timestamp")
	case "timestamp_out_of_range":
		return integrationmodel.IntegrationWebhookSignatureVerificationResult{}, forbidden("backend.integration.webhook_signature.timestamp_out_of_range")
	case "missing_nonce":
		return integrationmodel.IntegrationWebhookSignatureVerificationResult{}, badRequest("backend.integration.webhook_signature.missing_nonce")
	default:
		return invalid, forbidden("backend.integration.webhook_signature.invalid")
	}
	if req.MaxSkewSeconds > 0 {
		expiresAt := now.Add(time.Duration(req.MaxSkewSeconds) * time.Second).Format(time.RFC3339)
		duplicate, err := s.eventRepo.RecordWebhookNonce(ctx, principalWorkspaceID(principal), connectorKey, strings.TrimSpace(req.Nonce), strings.TrimSpace(req.Timestamp), expiresAt)
		if err != nil {
			return integrationmodel.IntegrationWebhookSignatureVerificationResult{}, err
		}
		if duplicate {
			return invalid, forbidden("backend.integration.webhook_signature.replay_detected")
		}
	}
	invalid.Valid = true
	return invalid, nil
}

func webhookSignatureStrategyFromConnection(connection integrationmodel.IntegrationConnection) map[string]any {
	return webhookSignatureStrategy(
		integrationConfigStringValue(connection.Config, "webhook_signature_algorithm", "signature_algorithm"),
		integrationConfigStringValue(connection.Config, "webhook_signature_secret_ref_name", "signature_secret_ref_name"),
		integrationConfigInt64(connection.Config, 0, "webhook_max_skew_seconds", "signature_max_skew_seconds", "max_skew_seconds"),
		integrationConfigStringValue(connection.Config, "webhook_signature_header", "signature_header"),
		integrationConfigStringValue(connection.Config, "webhook_timestamp_header", "signature_timestamp_header"),
		integrationConfigStringValue(connection.Config, "webhook_nonce_header", "signature_nonce_header"),
	)
}

func webhookSignatureStrategy(algorithm, secretRefName string, maxSkewSeconds int64, signatureHeader, timestampHeader, nonceHeader string) map[string]any {
	secretRefName = valueOrDefault(strings.TrimSpace(secretRefName), "webhook_secret")
	if signatureHeader = strings.TrimSpace(signatureHeader); signatureHeader == "" {
		signatureHeader = "X-Integration-Signature"
	}
	if timestampHeader = strings.TrimSpace(timestampHeader); timestampHeader == "" {
		timestampHeader = "X-Integration-Timestamp"
	}
	if nonceHeader = strings.TrimSpace(nonceHeader); nonceHeader == "" {
		nonceHeader = "X-Integration-Nonce"
	}
	return map[string]any{"algorithm": strings.TrimSpace(algorithm), "secret_ref_name": secretRefName, "max_skew_seconds": maxSkewSeconds, "signature_header": signatureHeader, "timestamp_header": timestampHeader, "nonce_header": nonceHeader, "replay_protection": maxSkewSeconds > 0}
}

func integrationConfigStringValue(config map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := config[key]; ok && value != nil {
			if text := strings.TrimSpace(fmt.Sprint(value)); text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}

func integrationConfigInt64(config map[string]any, fallback int64, keys ...string) int64 {
	for _, key := range keys {
		value, ok := config[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case int:
			return int64(typed)
		case int64:
			return typed
		case float64:
			return int64(typed)
		case json.Number:
			if parsed, err := typed.Int64(); err == nil {
				return parsed
			}
		}
		if parsed, err := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(value)), 10, 64); err == nil {
			return parsed
		}
	}
	return fallback
}
