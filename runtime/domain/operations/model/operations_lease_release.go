package operationsmodel

import "time"

type OperationsLeaseReleaseRequest struct {
	Owner                string    `json:"owner"`
	WorkspaceID          string    `json:"workspace_id,omitempty"`
	ResourceID           string    `json:"resource_id"`
	ExpectedLeaseOwner   string    `json:"expected_lease_owner"`
	ExpectedFencingToken int64     `json:"expected_fencing_token"`
	VerifiedStuck        bool      `json:"verified_stuck"`
	VerificationEvidence string    `json:"verification_evidence,omitempty"`
	Now                  time.Time `json:"now"`
}

type OperationsLeaseReleaseResult struct {
	Owner                string    `json:"owner"`
	WorkspaceID          string    `json:"workspace_id,omitempty"`
	ResourceID           string    `json:"resource_id"`
	PreviousLeaseOwner   string    `json:"previous_lease_owner"`
	PreviousFencingToken int64     `json:"previous_fencing_token"`
	NextFencingToken     int64     `json:"next_fencing_token"`
	PreviousExpiresAt    time.Time `json:"previous_expires_at"`
	ReleasedAt           time.Time `json:"released_at"`
	Eligibility          string    `json:"eligibility"`
}
