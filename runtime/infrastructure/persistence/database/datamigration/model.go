package datamigration

import "time"

type Engine string

const (
	EngineSQLite   Engine = "sqlite"
	EnginePostgres Engine = "postgres"
	EngineMySQL    Engine = "mysql"
)

type Inventory struct {
	Engine        Engine              `json:"engine"`
	Schema        string              `json:"schema,omitempty"`
	CapturedAt    time.Time           `json:"captured_at"`
	DatabaseBytes int64               `json:"database_bytes,omitempty"`
	Tables        []TableInventory    `json:"tables"`
	Sequences     []SequenceInventory `json:"sequences,omitempty"`
	Views         []ViewInventory     `json:"views,omitempty"`
	Triggers      []TriggerInventory  `json:"triggers,omitempty"`
}

type ViewInventory struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
}

type TriggerInventory struct {
	Name       string `json:"name"`
	Table      string `json:"table"`
	Timing     string `json:"timing,omitempty"`
	Event      string `json:"event,omitempty"`
	Definition string `json:"definition"`
}

type SequenceInventory struct {
	Name         string `json:"name"`
	CurrentValue int64  `json:"current_value"`
	OwnedTable   string `json:"owned_table,omitempty"`
	OwnedColumn  string `json:"owned_column,omitempty"`
}

type TableInventory struct {
	Name                   string                `json:"name"`
	Rows                   int64                 `json:"rows"`
	EstimatedBytes         int64                 `json:"estimated_bytes,omitempty"`
	Columns                []ColumnInventory     `json:"columns"`
	PrimaryKey             []string              `json:"primary_key,omitempty"`
	Indexes                []IndexInventory      `json:"indexes,omitempty"`
	ForeignKeys            []ForeignInventory    `json:"foreign_keys,omitempty"`
	Constraints            []ConstraintInventory `json:"constraints,omitempty"`
	WorkspaceScoped        bool                  `json:"workspace_scoped"`
	InvalidWorkspaceRows   int64                 `json:"invalid_workspace_rows,omitempty"`
	LargeObjectColumns     []string              `json:"large_object_columns,omitempty"`
	MaximumLargeObjectSize int64                 `json:"maximum_large_object_size,omitempty"`
}

type ConstraintInventory struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Columns    []string `json:"columns,omitempty"`
	Definition string   `json:"definition,omitempty"`
}

type ColumnInventory struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   bool   `json:"nullable"`
	Default    string `json:"default,omitempty"`
	PrimaryKey int    `json:"primary_key_position,omitempty"`
}

type IndexInventory struct {
	Name    string   `json:"name"`
	Unique  bool     `json:"unique"`
	Columns []string `json:"columns"`
}

type ForeignInventory struct {
	Columns           []string `json:"columns"`
	ReferencedTable   string   `json:"referenced_table"`
	ReferencedColumns []string `json:"referenced_columns"`
}

type Plan struct {
	Source                   Inventory      `json:"source"`
	Target                   Inventory      `json:"target"`
	GeneratedAt              time.Time      `json:"generated_at"`
	EstimatedTargetBytes     int64          `json:"estimated_target_bytes"`
	Tables                   []TablePlan    `json:"tables"`
	Sequences                []SequencePlan `json:"sequences,omitempty"`
	Blockers                 []string       `json:"blockers,omitempty"`
	Warnings                 []string       `json:"warnings,omitempty"`
	RequiresStopWriteCutover bool           `json:"requires_stop_write_cutover"`
	RequiresIsolatedRollback bool           `json:"requires_isolated_target_rollback"`
	ForbidsWorkspaceFallback bool           `json:"forbids_workspace_fallback"`
	HighCostIndexesAfterCopy bool           `json:"high_cost_indexes_after_copy"`
	ConstraintsAfterCopy     bool           `json:"constraints_after_copy"`
	PreviewGroups            []PreviewGroup `json:"preview_groups"`
}

// PreviewGroup makes every migration phase and its destructive nature visible
// without exposing executable arbitrary SQL.
type PreviewGroup struct {
	Phase       string   `json:"phase"`
	Operations  []string `json:"operations"`
	Destructive bool     `json:"destructive"`
}

type SequencePlan struct {
	SourceName   string `json:"source_name"`
	TargetName   string `json:"target_name,omitempty"`
	CurrentValue int64  `json:"current_value"`
	Strategy     string `json:"strategy"`
	Blocked      bool   `json:"blocked"`
}

type TablePlan struct {
	Name                string             `json:"name"`
	Rows                int64              `json:"rows"`
	CheckpointKey       []string           `json:"checkpoint_key,omitempty"`
	Conversions         []ConversionPlan   `json:"conversions"`
	MissingTarget       bool               `json:"missing_target"`
	MissingColumns      []string           `json:"missing_columns,omitempty"`
	ConflictingTypes    []string           `json:"conflicting_types,omitempty"`
	WorkspaceScoped     bool               `json:"workspace_scoped"`
	BatchCopyEligible   bool               `json:"batch_copy_eligible"`
	DeferredIndexes     []IndexInventory   `json:"deferred_indexes,omitempty"`
	DeferredForeignKeys []ForeignInventory `json:"deferred_foreign_keys,omitempty"`
}

type ConversionPlan struct {
	Column         string `json:"column"`
	SourceType     string `json:"source_type"`
	TargetType     string `json:"target_type"`
	Strategy       string `json:"strategy"`
	Reversible     bool   `json:"reversible"`
	RequiresReview bool   `json:"requires_review"`
}
