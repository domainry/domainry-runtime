package lifecyclemodel

import "time"

type RetentionClass string

const (
	RetentionClassProduct    RetentionClass = "product_retention"
	RetentionClassLegalAudit RetentionClass = "legal_audit_retention"
	RetentionClassTechnical  RetentionClass = "technical_ttl"
	RetentionClassUserErase  RetentionClass = "user_requested_erase"
)

type Sensitivity string

const (
	SensitivityInternal  Sensitivity = "internal"
	SensitivityPII       Sensitivity = "pii"
	SensitivitySensitive Sensitivity = "sensitive"
	SensitivityFinancial Sensitivity = "financial"
	SensitivitySecurity  Sensitivity = "security"
	SensitivityAudit     Sensitivity = "audit"
)

type BackupBehavior string

const (
	BackupBehaviorStandard         BackupBehavior = "standard_restore_then_reconcile"
	BackupBehaviorDelayedErase     BackupBehavior = "delayed_erase_after_restore"
	BackupBehaviorComplianceLocked BackupBehavior = "compliance_locked"
)

type EraseBehavior string

const (
	EraseBehaviorDelete      EraseBehavior = "delete"
	EraseBehaviorAnonymize   EraseBehavior = "anonymize"
	EraseBehaviorNotEligible EraseBehavior = "not_eligible"
)

type RetentionPolicy struct {
	Key                     string
	Version                 string
	Owner                   string
	Class                   RetentionClass
	Sensitivity             []Sensitivity
	DefaultRetention        time.Duration
	MinimumRetention        time.Duration
	StatusRetention         map[string]time.Duration
	ReplayWindow            time.Duration
	WorkspaceMayExtend      bool
	WorkspaceMayReduce      bool
	LegalHoldEligible       bool
	BackupBehavior          BackupBehavior
	EraseBehavior           EraseBehavior
	RequiredReferenceChecks []string
}

type WorkspaceRetentionOverride struct {
	WorkspaceID string
	Retention   time.Duration
}
