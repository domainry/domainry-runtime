package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

const (
	IntegrationEdgeOfflinePolicyDeny        = "deny"
	IntegrationEdgeOfflinePolicyAllowSigned = "allow_if_signed"
)

// IntegrationSignEdgeDecisionBundle signs the complete canonical decision snapshot. The
// caller owns key lookup and rotation; raw key material is never persisted in
// the bundle.
func IntegrationSignEdgeDecisionBundle(bundle integrationmodel.IntegrationEdgeDecisionBundle, key []byte) (integrationmodel.IntegrationEdgeDecisionBundle, error) {
	bundle.Signature = ""
	payload := canonicalEdgeDecisionPayload(bundle)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	bundle.Signature = base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return bundle, nil
}

// IntegrationEvaluateEdgeDecision fails closed unless an unexpired, unrevoked and validly
// signed bundle explicitly permits offline execution.
func IntegrationEvaluateEdgeDecision(bundle integrationmodel.IntegrationEdgeDecisionBundle, request integrationmodel.IntegrationEdgeDecisionRequest, key []byte) integrationmodel.IntegrationEdgeDecisionResult {
	result := integrationmodel.IntegrationEdgeDecisionResult{ReasonCode: "edge.bundle.invalid"}
	if strings.TrimSpace(bundle.BundleID) == "" || strings.TrimSpace(bundle.WorkspaceID) == "" || strings.TrimSpace(bundle.SubjectID) == "" || bundle.Version <= 0 || len(key) == 0 {
		return result
	}
	result.BundleID, result.Version = bundle.BundleID, bundle.Version
	if bundle.WorkspaceID != strings.TrimSpace(request.WorkspaceID) || bundle.SubjectID != strings.TrimSpace(request.SubjectID) {
		result.ReasonCode = "edge.bundle.subject_mismatch"
		return result
	}
	if edgeDecisionRevoked(bundle.BundleID, append(append([]string{}, bundle.RevokedIDs...), request.RevokedIDs...)) {
		result.ReasonCode = "edge.bundle.revoked"
		return result
	}
	now, nowErr := time.Parse(time.RFC3339, strings.TrimSpace(request.Now))
	issuedAt, issuedErr := time.Parse(time.RFC3339, strings.TrimSpace(bundle.IssuedAt))
	expiresAt, expiresErr := time.Parse(time.RFC3339, strings.TrimSpace(bundle.ExpiresAt))
	if nowErr != nil || issuedErr != nil || expiresErr != nil || now.Before(issuedAt) || !now.Before(expiresAt) {
		result.ReasonCode = "edge.bundle.expired"
		return result
	}
	signature, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(bundle.Signature))
	if err != nil {
		result.ReasonCode = "edge.bundle.signature_invalid"
		return result
	}
	unsigned := bundle
	unsigned.Signature = ""
	payload := canonicalEdgeDecisionPayload(unsigned)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		result.ReasonCode = "edge.bundle.signature_invalid"
		return result
	}
	if bundle.Decision != "allow" {
		result.ReasonCode = nonEmptyEdgeReason(bundle.ReasonCode, "edge.bundle.denied")
		return result
	}
	if bundle.OfflinePolicy != IntegrationEdgeOfflinePolicyAllowSigned {
		result.ReasonCode = "edge.bundle.offline_denied"
		return result
	}
	for _, entitlement := range bundle.Entitlements {
		if strings.TrimSpace(entitlement.Key) == "" || strings.TrimSpace(entitlement.State) == "" {
			return result
		}
		if entitlement.ValidUntil != "" {
			validUntil, parseErr := time.Parse(time.RFC3339, entitlement.ValidUntil)
			if parseErr != nil || !now.Before(validUntil) {
				result.ReasonCode = "edge.entitlement.expired"
				return result
			}
		}
	}
	result.Allowed, result.ReasonCode = true, nonEmptyEdgeReason(bundle.ReasonCode, "edge.bundle.allowed")
	return result
}

func canonicalEdgeDecisionPayload(bundle integrationmodel.IntegrationEdgeDecisionBundle) []byte {
	sort.Strings(bundle.RevokedIDs)
	sort.Slice(bundle.Entitlements, func(i, j int) bool { return bundle.Entitlements[i].Key < bundle.Entitlements[j].Key })
	// The bundle contains only JSON-safe scalar and slice fields.
	payload, _ := json.Marshal(bundle)
	return payload
}

func edgeDecisionRevoked(bundleID string, revoked []string) bool {
	for _, id := range revoked {
		if strings.TrimSpace(id) == bundleID {
			return true
		}
	}
	return false
}

func nonEmptyEdgeReason(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}
