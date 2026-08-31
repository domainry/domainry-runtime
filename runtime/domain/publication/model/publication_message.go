// Package model defines Runtime-owned durable publication handoff facts. It
// contains no Provider configuration, credentials, inbound events, or
// Integration invocation state.
package publicationmodel

type Message struct {
	ID                 string         `json:"id"`
	WorkspaceID        string         `json:"workspace_id,omitempty"`
	ConnectorKey       string         `json:"connector_key"`
	ConnectionKey      string         `json:"connection_key,omitempty"`
	Operation          string         `json:"operation"`
	Status             string         `json:"status"`
	Payload            map[string]any `json:"payload,omitempty"`
	EventID            string         `json:"event_id,omitempty"`
	RequestRef         string         `json:"request_ref,omitempty"`
	DedupKey           string         `json:"dedup_key,omitempty"`
	RequestFingerprint string         `json:"request_fingerprint,omitempty"`
	ResponseRef        string         `json:"response_ref,omitempty"`
	Error              string         `json:"error,omitempty"`
	AttemptCount       int            `json:"attempt_count"`
	NextAttemptAt      string         `json:"next_attempt_at,omitempty"`
	LastAttemptAt      string         `json:"last_attempt_at,omitempty"`
	LeaseOwner         string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt     string         `json:"lease_expires_at,omitempty"`
	FencingToken       int64          `json:"fencing_token,omitempty"`
	CreatedBy          string         `json:"created_by,omitempty"`
	CreatedAt          string         `json:"created_at,omitempty"`
	UpdatedAt          string         `json:"updated_at,omitempty"`
}

type StatusRequest struct {
	Status      string `json:"status"`
	ResponseRef string `json:"response_ref,omitempty"`
	Error       string `json:"error,omitempty"`
}

type RetryRequest struct {
	DelaySeconds int    `json:"delay_seconds,omitempty"`
	Error        string `json:"error,omitempty"`
}

// Handoff is the business-safe projection of one Runtime publication handoff.
// It deliberately exposes no Provider response or Integration invocation body.
type Handoff struct {
	ID            string `json:"id"`
	ConnectorKey  string `json:"connector_key"`
	ConnectionKey string `json:"connection_key,omitempty"`
	Operation     string `json:"operation"`
	Status        string `json:"status"`
	ResponseRef   string `json:"response_ref,omitempty"`
	Error         string `json:"error,omitempty"`
	AttemptCount  int    `json:"attempt_count"`
	CreatedAt     string `json:"created_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}
