package deploymentmodel

type MigrationStatus struct {
	Current          bool                    `json:"current"`
	Expected         int                     `json:"expected"`
	Applied          int                     `json:"applied"`
	Pending          int                     `json:"pending"`
	Drift            int                     `json:"drift"`
	Dirty            int                     `json:"dirty"`
	Unknown          int                     `json:"unknown"`
	Newer            int                     `json:"newer"`
	State            string                  `json:"state"`
	ErrorCode        string                  `json:"error_code,omitempty"`
	RuntimeVersion   string                  `json:"runtime_version,omitempty"`
	MinSchemaVersion string                  `json:"min_schema_version,omitempty"`
	MaxSchemaVersion string                  `json:"max_schema_version,omitempty"`
	ExpectedPaths    []string                `json:"expected_paths,omitempty"`
	AppliedPaths     []string                `json:"applied_paths,omitempty"`
	PendingPaths     []string                `json:"pending_paths,omitempty"`
	DriftPaths       []string                `json:"drift_paths,omitempty"`
	DirtyPaths       []string                `json:"dirty_paths,omitempty"`
	UnknownPaths     []string                `json:"unknown_paths,omitempty"`
	NewerPaths       []string                `json:"newer_paths,omitempty"`
	LastAppliedAt    string                  `json:"last_applied_at,omitempty"`
	Error            string                  `json:"error,omitempty"`
	Rollback         MigrationRollbackPolicy `json:"rollback"`
}

type MigrationRollbackPolicy struct {
	Mode                   string   `json:"mode"`
	RequiresVerifiedBackup bool     `json:"requires_verified_backup"`
	Procedure              []string `json:"procedure,omitempty"`
}
