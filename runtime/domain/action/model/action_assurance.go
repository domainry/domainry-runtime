package actionmodel

type ActionAssuranceGrant struct {
	ID              string
	TokenHash       string
	WorkspaceID     string
	UserID          string
	ActionKey       string
	ObjectKey       string
	RecordID        string
	PayloadDigest   string
	Methods         []string
	ApprovalVersion string
	ApprovalHash    string
	IssuedAt        string
	ExpiresAt       string
	ConsumedAt      string
}

type ActionAssuranceIssueRequest struct {
	WorkspaceID     string
	UserID          string
	ActionKey       string
	ObjectKey       string
	RecordID        string
	Payload         map[string]any
	VerifiedMethods []string
	ApprovalVersion string
	ApprovalHash    string
}

type ActionAssuranceEvidence struct {
	GrantID         string            `json:"grant_id"`
	Methods         []string          `json:"methods"`
	ApprovalVersion string            `json:"approval_version,omitempty"`
	ApprovalHash    string            `json:"approval_hash,omitempty"`
	Facts           map[string]string `json:"facts,omitempty"`
}
