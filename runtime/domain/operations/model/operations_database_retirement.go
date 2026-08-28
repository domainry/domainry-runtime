package operationsmodel

import "time"

type DatabaseRetirementState string

const (
	DatabaseRetirementDiscovered          DatabaseRetirementState = "discovered"
	DatabaseRetirementReplacementReady    DatabaseRetirementState = "replacement_ready"
	DatabaseRetirementBackfilled          DatabaseRetirementState = "backfilled"
	DatabaseRetirementReadsSwitched       DatabaseRetirementState = "reads_switched"
	DatabaseRetirementWritesDisabled      DatabaseRetirementState = "writes_disabled"
	DatabaseRetirementObservationComplete DatabaseRetirementState = "observation_complete"
	DatabaseRetirementQuarantined         DatabaseRetirementState = "quarantined"
	DatabaseRetirementDropped             DatabaseRetirementState = "dropped"
	DatabaseRetirementCodeRemoved         DatabaseRetirementState = "code_removed"
	DatabaseRetirementBlocked             DatabaseRetirementState = "blocked"
)

type DatabaseObjectIdentity struct {
	Engine     string `json:"engine"`
	Database   string `json:"database"`
	Schema     string `json:"schema"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	ParentName string `json:"parent_name,omitempty"`
}

type DatabaseAccessObservation struct {
	ReadCount     uint64            `json:"read_count"`
	WriteCount    uint64            `json:"write_count"`
	LastReadAt    *time.Time        `json:"last_read_at,omitempty"`
	LastWriteAt   *time.Time        `json:"last_write_at,omitempty"`
	SourceCounts  map[string]uint64 `json:"source_counts,omitempty"`
	WindowStarted time.Time         `json:"window_started"`
	WindowEnds    time.Time         `json:"window_ends"`
}

type DatabaseDataComparison struct {
	SourceRows        int64     `json:"source_rows"`
	ReplacementRows   int64     `json:"replacement_rows"`
	SourceKeys        int64     `json:"source_keys"`
	ReplacementKeys   int64     `json:"replacement_keys"`
	SourceHash        string    `json:"source_hash"`
	ReplacementHash   string    `json:"replacement_hash"`
	BusinessChecks    []string  `json:"business_checks"`
	OrphanRows        int64     `json:"orphan_rows"`
	DuplicateRows     int64     `json:"duplicate_rows"`
	InvalidWorkspaces int64     `json:"invalid_workspace_rows"`
	InvalidReferences int64     `json:"invalid_reference_rows"`
	ComparedAt        time.Time `json:"compared_at"`
}

type DatabaseRetirementEvidence struct {
	Owner                 string                    `json:"owner"`
	Replacement           string                    `json:"replacement"`
	ExpectedSchemaVersion string                    `json:"expected_schema_version"`
	ExpectedDataVersion   string                    `json:"expected_data_version"`
	BackfillCheckpoint    string                    `json:"backfill_checkpoint,omitempty"`
	BackfillComplete      bool                      `json:"backfill_complete"`
	ReadsSwitchVersion    string                    `json:"reads_switch_version,omitempty"`
	WritesDisableVersion  string                    `json:"writes_disable_version,omitempty"`
	WriteProtection       string                    `json:"write_protection,omitempty"`
	Observation           DatabaseAccessObservation `json:"observation"`
	Comparison            DatabaseDataComparison    `json:"comparison"`
	Disposition           string                    `json:"disposition"`
	DispositionApproval   string                    `json:"disposition_approval,omitempty"`
	BackupID              string                    `json:"backup_id,omitempty"`
	BackupChecksum        string                    `json:"backup_checksum,omitempty"`
	BackupVerifiedAt      *time.Time                `json:"backup_verified_at,omitempty"`
	RestoreDrillAt        *time.Time                `json:"restore_drill_at,omitempty"`
	MaintenanceEvidence   string                    `json:"maintenance_evidence,omitempty"`
	DrainEvidence         string                    `json:"drain_evidence,omitempty"`
	ChangePlanID          string                    `json:"change_plan_id,omitempty"`
	ApprovalID            string                    `json:"approval_id,omitempty"`
	QuarantineObjectName  string                    `json:"quarantine_object_name,omitempty"`
	QuarantineUntil       *time.Time                `json:"quarantine_until,omitempty"`
	Rollback              string                    `json:"rollback"`
	AuditEventID          string                    `json:"audit_event_id"`
}

type DatabaseRetirement struct {
	ID            string                     `json:"id"`
	Object        DatabaseObjectIdentity     `json:"object"`
	State         DatabaseRetirementState    `json:"state"`
	Evidence      DatabaseRetirementEvidence `json:"evidence"`
	BlockedReason string                     `json:"blocked_reason,omitempty"`
	UpdatedAt     time.Time                  `json:"updated_at"`
}

type DatabaseRetirementOperationalStatus struct {
	Retirement                  DatabaseRetirement `json:"retirement"`
	ObservationRemainingSeconds int64              `json:"observation_remaining_seconds"`
	Alerts                      []string           `json:"alerts"`
}

type DatabaseDropPlan struct {
	RetirementID          string                   `json:"retirement_id"`
	Object                DatabaseObjectIdentity   `json:"object"`
	Dependencies          []DatabaseObjectIdentity `json:"dependencies"`
	Statements            []string                 `json:"statements"`
	EstimatedLock         time.Duration            `json:"estimated_lock"`
	EstimatedReclaimBytes int64                    `json:"estimated_reclaim_bytes"`
	LockTimeout           time.Duration            `json:"lock_timeout"`
	Rollback              string                   `json:"rollback"`
	ExternalTarget        bool                     `json:"external_target"`
	DeploymentScope       string                   `json:"deployment_scope,omitempty"`
	ApprovalID            string                   `json:"approval_id"`
}
