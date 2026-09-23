package operationsmodel

import "encoding/json"

// OperationsFailureClass determines the recovery path exposed to operators.
type OperationsFailureClass string

const (
	OperationsFailureRetryable          OperationsFailureClass = "retryable"
	OperationsFailureTerminal           OperationsFailureClass = "terminal"
	OperationsFailureManualIntervention OperationsFailureClass = "manual_intervention"
)

// OperationsReceipt is the stable result returned for both a first request
// and an idempotent replay. Result must contain redacted owner-owned evidence.
type OperationsReceipt struct {
	Command      OperationsCommand      `json:"command"`
	StatusURL    string                 `json:"status_url"`
	Result       json.RawMessage        `json:"result,omitempty"`
	Metadata     json.RawMessage        `json:"metadata,omitempty"`
	ErrorCode    string                 `json:"error_code,omitempty"`
	FailureClass OperationsFailureClass `json:"failure_class,omitempty"`
	NextAction   string                 `json:"next_action,omitempty"`
	RelatedIDs   []string               `json:"related_ids,omitempty"`
	Correlation  string                 `json:"correlation,omitempty"`
	Evidence     []string               `json:"evidence,omitempty"`
	LeaseOwner   string                 `json:"lease_owner,omitempty"`
	LeaseExpires string                 `json:"lease_expires_at,omitempty"`
	FencingToken int64                  `json:"fencing_token,omitempty"`
	ExpiresAt    string                 `json:"expires_at,omitempty"`
}

// OperationsSubmissionDecision is the deterministic response for a command
// already registered under the same operation owner and idempotency key.
type OperationsSubmissionDecision string

const (
	OperationsSubmissionAccepted OperationsSubmissionDecision = "accepted"
	OperationsSubmissionReplay   OperationsSubmissionDecision = "replay"
	OperationsSubmissionConflict OperationsSubmissionDecision = "fingerprint_conflict"
)
