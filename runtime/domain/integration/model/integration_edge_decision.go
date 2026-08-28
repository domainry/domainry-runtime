package integrationmodel

// IntegrationEdgeDecisionBundle is a provider- and industry-neutral, signed decision
// snapshot for a temporarily disconnected edge. Business-specific attributes
// remain opaque claims owned by metadata and rule definitions.
type IntegrationEdgeDecisionBundle struct {
	BundleID      string                            `json:"bundle_id"`
	WorkspaceID   string                            `json:"workspace_id"`
	SubjectID     string                            `json:"subject_id"`
	Decision      string                            `json:"decision"`
	ReasonCode    string                            `json:"reason_code"`
	Entitlements  []IntegrationEdgeEntitlementClaim `json:"entitlements,omitempty"`
	Version       int64                             `json:"version"`
	IssuedAt      string                            `json:"issued_at"`
	ExpiresAt     string                            `json:"expires_at"`
	RevokedIDs    []string                          `json:"revoked_ids,omitempty"`
	OfflinePolicy string                            `json:"offline_policy"`
	KeyID         string                            `json:"key_id"`
	Signature     string                            `json:"signature"`
}

type IntegrationEdgeEntitlementClaim struct {
	Key        string `json:"key"`
	State      string `json:"state"`
	Remaining  string `json:"remaining,omitempty"`
	ValidUntil string `json:"valid_until,omitempty"`
}

type IntegrationEdgeDecisionRequest struct {
	WorkspaceID string
	SubjectID   string
	Now         string
	RevokedIDs  []string
}

type IntegrationEdgeDecisionResult struct {
	Allowed    bool   `json:"allowed"`
	ReasonCode string `json:"reason_code"`
	BundleID   string `json:"bundle_id,omitempty"`
	Version    int64  `json:"version,omitempty"`
}
