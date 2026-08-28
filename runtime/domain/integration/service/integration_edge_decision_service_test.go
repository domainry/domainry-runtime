package service

import (
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestEdgeDecisionBundleAllowsOnlyValidSignedExplicitOfflineDecision(t *testing.T) {
	key := []byte("test-only-signing-key")
	bundle, err := IntegrationSignEdgeDecisionBundle(integrationmodel.IntegrationEdgeDecisionBundle{
		BundleID: "bundle-1", WorkspaceID: "workspace", SubjectID: "subject-1", Decision: "allow", ReasonCode: "policy.allowed",
		Entitlements: []integrationmodel.IntegrationEdgeEntitlementClaim{{Key: "access", State: "eligible", Remaining: "3", ValidUntil: "2026-07-20T12:10:00Z"}},
		Version:      7, IssuedAt: "2026-07-20T12:00:00Z", ExpiresAt: "2026-07-20T12:05:00Z", OfflinePolicy: IntegrationEdgeOfflinePolicyAllowSigned, KeyID: "edge-key-1",
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	request := integrationmodel.IntegrationEdgeDecisionRequest{WorkspaceID: "workspace", SubjectID: "subject-1", Now: "2026-07-20T12:01:00Z"}
	if result := IntegrationEvaluateEdgeDecision(bundle, request, key); !result.Allowed || result.Version != 7 {
		t.Fatalf("valid result=%#v", result)
	}

	cases := []struct {
		name   string
		mutate func(*integrationmodel.IntegrationEdgeDecisionBundle, *integrationmodel.IntegrationEdgeDecisionRequest)
		reason string
	}{
		{name: "expired bundle", mutate: func(_ *integrationmodel.IntegrationEdgeDecisionBundle, r *integrationmodel.IntegrationEdgeDecisionRequest) {
			r.Now = "2026-07-20T12:05:00Z"
		}, reason: "edge.bundle.expired"},
		{name: "revoked bundle", mutate: func(_ *integrationmodel.IntegrationEdgeDecisionBundle, r *integrationmodel.IntegrationEdgeDecisionRequest) {
			r.RevokedIDs = []string{"bundle-1"}
		}, reason: "edge.bundle.revoked"},
		{name: "subject mismatch", mutate: func(_ *integrationmodel.IntegrationEdgeDecisionBundle, r *integrationmodel.IntegrationEdgeDecisionRequest) {
			r.SubjectID = "other"
		}, reason: "edge.bundle.subject_mismatch"},
		{name: "tampered decision", mutate: func(b *integrationmodel.IntegrationEdgeDecisionBundle, _ *integrationmodel.IntegrationEdgeDecisionRequest) {
			b.ReasonCode = "tampered"
		}, reason: "edge.bundle.signature_invalid"},
		{name: "fail closed policy", mutate: func(b *integrationmodel.IntegrationEdgeDecisionBundle, _ *integrationmodel.IntegrationEdgeDecisionRequest) {
			b.OfflinePolicy = IntegrationEdgeOfflinePolicyDeny
			*b, _ = IntegrationSignEdgeDecisionBundle(*b, key)
		}, reason: "edge.bundle.offline_denied"},
		{name: "server denial", mutate: func(b *integrationmodel.IntegrationEdgeDecisionBundle, _ *integrationmodel.IntegrationEdgeDecisionRequest) {
			b.Decision = "deny"
			b.ReasonCode = "policy.denied"
			*b, _ = IntegrationSignEdgeDecisionBundle(*b, key)
		}, reason: "policy.denied"},
		{name: "expired entitlement", mutate: func(b *integrationmodel.IntegrationEdgeDecisionBundle, _ *integrationmodel.IntegrationEdgeDecisionRequest) {
			b.Entitlements[0].ValidUntil = "2026-07-20T12:00:30Z"
			*b, _ = IntegrationSignEdgeDecisionBundle(*b, key)
		}, reason: "edge.entitlement.expired"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			candidate, candidateRequest := bundle, request
			candidate.Entitlements = append([]integrationmodel.IntegrationEdgeEntitlementClaim(nil), bundle.Entitlements...)
			test.mutate(&candidate, &candidateRequest)
			result := IntegrationEvaluateEdgeDecision(candidate, candidateRequest, key)
			if result.Allowed || result.ReasonCode != test.reason {
				t.Fatalf("result=%#v", result)
			}
		})
	}
}

func TestEdgeDecisionBundleCoversGenericFailClosedInputs(t *testing.T) {
	key := []byte("test-only-signing-key")
	base := integrationmodel.IntegrationEdgeDecisionBundle{
		BundleID: "bundle-1", WorkspaceID: "workspace", SubjectID: "subject-1", Decision: "allow",
		Version: 1, IssuedAt: "2026-07-20T12:00:00Z", ExpiresAt: "2026-07-20T12:05:00Z",
		OfflinePolicy: IntegrationEdgeOfflinePolicyAllowSigned,
	}
	request := integrationmodel.IntegrationEdgeDecisionRequest{WorkspaceID: "workspace", SubjectID: "subject-1", Now: "2026-07-20T12:01:00Z"}
	signed := func(bundle integrationmodel.IntegrationEdgeDecisionBundle) integrationmodel.IntegrationEdgeDecisionBundle {
		result, err := IntegrationSignEdgeDecisionBundle(bundle, key)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	base = signed(base)

	invalidBundles := []integrationmodel.IntegrationEdgeDecisionBundle{
		func() integrationmodel.IntegrationEdgeDecisionBundle {
			value := base
			value.BundleID = ""
			return value
		}(),
		func() integrationmodel.IntegrationEdgeDecisionBundle {
			value := base
			value.WorkspaceID = ""
			return value
		}(),
		func() integrationmodel.IntegrationEdgeDecisionBundle {
			value := base
			value.SubjectID = ""
			return value
		}(),
		func() integrationmodel.IntegrationEdgeDecisionBundle { value := base; value.Version = 0; return value }(),
	}
	for _, bundle := range invalidBundles {
		if result := IntegrationEvaluateEdgeDecision(bundle, request, key); result.Allowed || result.ReasonCode != "edge.bundle.invalid" {
			t.Fatalf("invalid bundle result=%#v", result)
		}
	}
	if result := IntegrationEvaluateEdgeDecision(base, request, nil); result.Allowed || result.ReasonCode != "edge.bundle.invalid" {
		t.Fatalf("empty key result=%#v", result)
	}

	cases := []struct {
		name    string
		bundle  integrationmodel.IntegrationEdgeDecisionBundle
		request integrationmodel.IntegrationEdgeDecisionRequest
		resign  bool
		reason  string
	}{
		{name: "workspace mismatch", bundle: base, request: func() integrationmodel.IntegrationEdgeDecisionRequest {
			value := request
			value.WorkspaceID = "other"
			return value
		}(), reason: "edge.bundle.subject_mismatch"},
		{name: "bundle revocation", bundle: func() integrationmodel.IntegrationEdgeDecisionBundle {
			value := base
			value.RevokedIDs = []string{" ", "bundle-1"}
			return value
		}(), request: request, resign: true, reason: "edge.bundle.revoked"},
		{name: "invalid now", bundle: base, request: func() integrationmodel.IntegrationEdgeDecisionRequest {
			value := request
			value.Now = "invalid"
			return value
		}(), reason: "edge.bundle.expired"},
		{name: "invalid issued", bundle: func() integrationmodel.IntegrationEdgeDecisionBundle {
			value := base
			value.IssuedAt = "invalid"
			return value
		}(), request: request, resign: true, reason: "edge.bundle.expired"},
		{name: "invalid expiry", bundle: func() integrationmodel.IntegrationEdgeDecisionBundle {
			value := base
			value.ExpiresAt = "invalid"
			return value
		}(), request: request, resign: true, reason: "edge.bundle.expired"},
		{name: "not yet issued", bundle: func() integrationmodel.IntegrationEdgeDecisionBundle {
			value := base
			value.IssuedAt = "2026-07-20T12:02:00Z"
			return value
		}(), request: request, resign: true, reason: "edge.bundle.expired"},
		{name: "invalid signature encoding", bundle: func() integrationmodel.IntegrationEdgeDecisionBundle {
			value := base
			value.Signature = "***"
			return value
		}(), request: request, reason: "edge.bundle.signature_invalid"},
		{name: "blank entitlement key", bundle: func() integrationmodel.IntegrationEdgeDecisionBundle {
			value := base
			value.Entitlements = []integrationmodel.IntegrationEdgeEntitlementClaim{{Key: "", State: "active"}}
			return value
		}(), request: request, resign: true, reason: "edge.bundle.invalid"},
		{name: "blank entitlement state", bundle: func() integrationmodel.IntegrationEdgeDecisionBundle {
			value := base
			value.Entitlements = []integrationmodel.IntegrationEdgeEntitlementClaim{{Key: "access", State: ""}}
			return value
		}(), request: request, resign: true, reason: "edge.bundle.invalid"},
		{name: "invalid entitlement expiry", bundle: func() integrationmodel.IntegrationEdgeDecisionBundle {
			value := base
			value.Entitlements = []integrationmodel.IntegrationEdgeEntitlementClaim{{Key: "access", State: "active", ValidUntil: "invalid"}}
			return value
		}(), request: request, resign: true, reason: "edge.entitlement.expired"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			bundle := test.bundle
			if test.resign {
				bundle = signed(bundle)
			}
			result := IntegrationEvaluateEdgeDecision(bundle, test.request, key)
			if result.Allowed || result.ReasonCode != test.reason {
				t.Fatalf("result=%#v", result)
			}
		})
	}

	withoutReason := base
	withoutReason.ReasonCode = ""
	withoutReason = signed(withoutReason)
	if result := IntegrationEvaluateEdgeDecision(withoutReason, request, key); !result.Allowed || result.ReasonCode != "edge.bundle.allowed" {
		t.Fatalf("fallback allow reason result=%#v", result)
	}
	withoutEntitlementExpiry := base
	withoutEntitlementExpiry.Entitlements = []integrationmodel.IntegrationEdgeEntitlementClaim{{Key: "second", State: "active"}, {Key: "access", State: "active"}}
	withoutEntitlementExpiry = signed(withoutEntitlementExpiry)
	if result := IntegrationEvaluateEdgeDecision(withoutEntitlementExpiry, request, key); !result.Allowed {
		t.Fatalf("non-expiring entitlement result=%#v", result)
	}
	denied := base
	denied.Decision, denied.ReasonCode = "deny", ""
	denied = signed(denied)
	if result := IntegrationEvaluateEdgeDecision(denied, request, key); result.Allowed || result.ReasonCode != "edge.bundle.denied" {
		t.Fatalf("fallback deny reason result=%#v", result)
	}
}
