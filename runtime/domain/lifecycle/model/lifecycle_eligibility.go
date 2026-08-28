package lifecyclemodel

import "time"

type Operation string

const (
	OperationArchive Operation = "archive"
	OperationPurge   Operation = "purge"
	OperationErase   Operation = "erase"
)

type ResourceTarget struct {
	WorkspaceID  string
	Owner        string
	ResourceType string
	ResourceID   string
}

type LegalHold struct {
	ID            string
	WorkspaceID   string
	Owner         string
	ResourceType  string
	ResourceID    string
	Reason        string
	Authority     string
	StartsAt      time.Time
	EndsAt        *time.Time
	ReviewAt      time.Time
	AuditEvidence string
}

type EligibilityInput struct {
	Operation           Operation
	Target              ResourceTarget
	OwnerEligible       bool
	ActiveProcess       bool
	PendingOutbox       bool
	Referenced          bool
	BackupPolicyBlocked bool
	LegalHolds          []LegalHold
	Now                 time.Time
}

type EligibilityDecision struct {
	Eligible bool
	Blockers []string
}
