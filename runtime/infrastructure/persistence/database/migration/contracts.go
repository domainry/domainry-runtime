package migration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type artifactFile interface {
	io.Reader
	Stat() (os.FileInfo, error)
	Close() error
}

var (
	openArtifactFile   = func(path string) (artifactFile, error) { return os.Open(path) }
	makeEvidenceDir    = os.MkdirAll
	writeEvidenceFile  = os.WriteFile
	renameEvidenceFile = os.Rename
)

const (
	StateCurrent = "current"
	StatePending = "migration.pending"
	StateFailed  = "migration.failed"
	StateDirty   = "migration.dirty"
	StateUnknown = "migration.unknown"
	StateNewer   = "migration.schema_newer"
	StateDrift   = "migration.checksum_drift"
)

type Compatibility struct {
	RuntimeVersion   string `json:"runtime_version"`
	MinSchemaVersion string `json:"min_schema_version"`
	MaxSchemaVersion string `json:"max_schema_version"`
}

func (c Compatibility) Evaluate(current string, known bool) (string, error) {
	if !known {
		return StateUnknown, fmt.Errorf("%s: schema version %q is not in the release manifest", StateUnknown, current)
	}
	if compareVersions(current, c.MinSchemaVersion) < 0 {
		return StatePending, fmt.Errorf("%s: schema %s is older than minimum %s", StatePending, current, c.MinSchemaVersion)
	}
	if compareVersions(current, c.MaxSchemaVersion) > 0 {
		return StateNewer, fmt.Errorf("%s: schema %s is newer than maximum %s", StateNewer, current, c.MaxSchemaVersion)
	}
	return StateCurrent, nil
}

func compareVersions(left, right string) int {
	parse := func(value string) []int {
		value = strings.TrimSpace(strings.SplitN(value, "_", 2)[0])
		parts := strings.FieldsFunc(value, func(r rune) bool { return r == '.' || r == '-' })
		result := make([]int, 0, len(parts))
		for _, part := range parts {
			n, _ := strconv.Atoi(part)
			result = append(result, n)
		}
		return result
	}
	l, r := parse(left), parse(right)
	for len(l) < len(r) {
		l = append(l, 0)
	}
	for len(r) < len(l) {
		r = append(r, 0)
	}
	for index := range l {
		if l[index] < r[index] {
			return -1
		}
		if l[index] > r[index] {
			return 1
		}
	}
	return 0
}

func CompareVersions(left, right string) int { return compareVersions(left, right) }

type Artifact struct {
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	Checksum   string `json:"checksum"`
	Size       int64  `json:"size"`
	KeyVersion string `json:"key_version,omitempty"`
}

type BackupEvidence struct {
	EvidenceVersion      string     `json:"evidence_version"`
	BackupID             string     `json:"backup_id"`
	Engine               string     `json:"engine"`
	BackupType           string     `json:"backup_type"`
	Provider             string     `json:"provider"`
	ProjectReferenceHash string     `json:"project_reference_hash,omitempty"`
	Region               string     `json:"region"`
	FaultDomain          string     `json:"fault_domain"`
	Owner                string     `json:"owner"`
	CreatedAt            time.Time  `json:"created_at"`
	VerifiedAt           time.Time  `json:"verified_at"`
	SchemaVersion        string     `json:"schema_version"`
	Checksum             string     `json:"checksum"`
	Size                 int64      `json:"size"`
	Encrypted            bool       `json:"encrypted"`
	ImmutableUntil       time.Time  `json:"immutable_until"`
	RPOSeconds           int64      `json:"rpo_seconds"`
	RTOSeconds           int64      `json:"rto_seconds"`
	PITRTarget           *time.Time `json:"pitr_target,omitempty"`
	Artifacts            []Artifact `json:"artifacts"`
	IntegrityOK          bool       `json:"integrity_verified"`
}

func (e BackupEvidence) Validate(now time.Time) error {
	if strings.TrimSpace(e.BackupID) == "" || strings.TrimSpace(e.Engine) == "" || strings.TrimSpace(e.Provider) == "" || strings.TrimSpace(e.Owner) == "" {
		return fmt.Errorf("backup.evidence_identity_required")
	}
	if e.CreatedAt.IsZero() || e.VerifiedAt.Before(e.CreatedAt) || e.VerifiedAt.After(now.Add(5*time.Minute)) {
		return fmt.Errorf("backup.evidence_timestamps_invalid")
	}
	if !e.IntegrityOK || strings.TrimSpace(e.Checksum) == "" || e.Size <= 0 {
		return fmt.Errorf("backup.integrity_evidence_required")
	}
	if !e.Encrypted || e.ImmutableUntil.Before(now) || strings.TrimSpace(e.Region) == "" || strings.TrimSpace(e.FaultDomain) == "" {
		return fmt.Errorf("backup.protection_evidence_required")
	}
	if e.RPOSeconds < 0 || e.RTOSeconds <= 0 || strings.TrimSpace(e.SchemaVersion) == "" {
		return fmt.Errorf("backup.recovery_objectives_required")
	}
	kinds := map[string]bool{}
	for _, artifact := range e.Artifacts {
		if strings.TrimSpace(artifact.Kind) == "" || strings.TrimSpace(artifact.Checksum) == "" || artifact.Size < 0 {
			return fmt.Errorf("backup.artifact_manifest_invalid")
		}
		if strings.TrimSpace(artifact.Path) != "" {
			if _, err := os.Stat(artifact.Path); err == nil {
				actual, readErr := FileArtifact(artifact.Kind, artifact.Path, artifact.KeyVersion)
				if readErr != nil || actual.Checksum != artifact.Checksum || actual.Size != artifact.Size {
					return fmt.Errorf("backup.artifact_integrity_mismatch: %s", artifact.Kind)
				}
			}
		}
		kinds[artifact.Kind] = true
	}
	for _, required := range []string{"database", "manifest", "uploads", "key_versions", "runtime_artifact"} {
		if !kinds[required] {
			return fmt.Errorf("backup.artifact_manifest_missing: %s", required)
		}
	}
	return nil
}

func ReadBackupEvidence(path string) (BackupEvidence, error) {
	raw, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return BackupEvidence{}, fmt.Errorf("read backup evidence: %w", err)
	}
	var evidence BackupEvidence
	if err := json.Unmarshal(raw, &evidence); err != nil {
		return BackupEvidence{}, fmt.Errorf("parse backup evidence: %w", err)
	}
	if err := evidence.Validate(time.Now().UTC()); err != nil {
		return BackupEvidence{}, err
	}
	return evidence, nil
}

func FileArtifact(kind, path, keyVersion string) (Artifact, error) {
	file, err := openArtifactFile(filepath.Clean(path))
	if err != nil {
		return Artifact{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Artifact{}, err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return Artifact{}, err
	}
	return Artifact{Kind: kind, Path: path, Checksum: hex.EncodeToString(hash.Sum(nil)), Size: info.Size(), KeyVersion: keyVersion}, nil
}

type RestoreRequest struct {
	Engine              string    `json:"engine"`
	Target              string    `json:"target"`
	BackupID            string    `json:"backup_id"`
	PointInTime         time.Time `json:"point_in_time,omitempty"`
	DryRun              bool      `json:"dry_run"`
	MaintenanceEvidence string    `json:"maintenance_evidence"`
	DrainEvidence       string    `json:"drain_evidence"`
	Operator            string    `json:"operator"`
	ChangePlanID        string    `json:"change_plan_id"`
}

func (r RestoreRequest) Validate(evidence BackupEvidence) error {
	if strings.TrimSpace(r.Target) == "" || strings.TrimSpace(r.BackupID) == "" || r.BackupID != evidence.BackupID {
		return fmt.Errorf("restore.target_and_backup_required")
	}
	if strings.TrimSpace(r.Operator) == "" || strings.TrimSpace(r.ChangePlanID) == "" {
		return fmt.Errorf("restore.authorization_and_change_plan_required")
	}
	if !r.DryRun && (strings.TrimSpace(r.MaintenanceEvidence) == "" || strings.TrimSpace(r.DrainEvidence) == "") {
		return fmt.Errorf("restore.maintenance_and_drain_evidence_required")
	}
	if !r.PointInTime.IsZero() {
		if evidence.PITRTarget == nil || r.PointInTime.Before(evidence.CreatedAt) || r.PointInTime.After(time.Now().UTC()) {
			return fmt.Errorf("restore.pitr_target_invalid")
		}
	}
	return nil
}

type ReconciliationAction struct {
	Table  string `json:"table"`
	Action string `json:"action"`
	Guard  string `json:"guard"`
}

func RestoreReconciliationPlan() []ReconciliationAction {
	return []ReconciliationAction{
		{Table: "_automation_instruction_executions", Action: "release_expired_processing_lease", Guard: "lease_expires_at <= restored_at"},
		{Table: "_workflow_execution_receipts", Action: "release_expired_processing_lease", Guard: "lease_expires_at <= restored_at"},
		{Table: "_action_executions", Action: "release_expired_processing_lease", Guard: "lease_expires_at <= restored_at"},
		{Table: "_record_mutation_executions", Action: "release_expired_processing_lease", Guard: "lease_expires_at <= restored_at"},
		{Table: "_publication_outbox", Action: "requeue_expired_delivery", Guard: "lease_expires_at <= restored_at AND terminal_at IS NULL"},
		{Table: "_transaction_boundary_intents", Action: "requeue_expired_intent", Guard: "lease_expires_at <= restored_at"},
	}
}

type DrillEvidence struct {
	EvidenceVersion string                 `json:"evidence_version"`
	DrillID         string                 `json:"drill_id"`
	Scenario        string                 `json:"scenario"`
	Engine          string                 `json:"engine"`
	BackupID        string                 `json:"backup_id"`
	StartedAt       time.Time              `json:"started_at"`
	CompletedAt     time.Time              `json:"completed_at"`
	TargetTime      time.Time              `json:"target_time,omitempty"`
	ActualRPO       int64                  `json:"actual_rpo_seconds"`
	ActualRTO       int64                  `json:"actual_rto_seconds"`
	Checks          map[string]bool        `json:"checks"`
	Counts          map[string]int64       `json:"counts"`
	Reconciliation  []ReconciliationAction `json:"reconciliation"`
}

func (e DrillEvidence) Validate() error {
	if strings.TrimSpace(e.DrillID) == "" || strings.TrimSpace(e.BackupID) == "" || e.CompletedAt.Before(e.StartedAt) || e.ActualRPO < 0 || e.ActualRTO <= 0 {
		return fmt.Errorf("drill.evidence_invalid")
	}
	now := time.Now().UTC()
	if e.CompletedAt.After(now.Add(5*time.Minute)) || now.Sub(e.CompletedAt) > 30*24*time.Hour {
		return fmt.Errorf("drill.evidence_expired")
	}
	allowed := map[string]bool{"region_database_loss": true, "operator_error": true, "bad_migration": true}
	if !allowed[e.Scenario] {
		return fmt.Errorf("drill.scenario_invalid")
	}
	for _, required := range []string{"checksum", "schema", "critical_tables", "workspace_counts", "outbox", "leases", "idempotency_receipts", "no_duplicate_terminal_effect"} {
		if !e.Checks[required] {
			return fmt.Errorf("drill.check_failed: %s", required)
		}
	}
	return nil
}

func WriteJSONEvidence(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := makeEvidenceDir(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := writeEvidenceFile(temp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return renameEvidenceFile(temp, path)
}

type MigrationPreview struct {
	ReleaseID        string   `json:"release_id"`
	Phase            string   `json:"phase"`
	Additions        []string `json:"additions"`
	Removals         []string `json:"removals"`
	Backfills        []string `json:"backfills"`
	EstimatedRows    int64    `json:"estimated_rows"`
	LockRisk         string   `json:"lock_risk"`
	RollbackStrategy string   `json:"rollback_strategy"`
	BackupID         string   `json:"backup_id,omitempty"`
	ChangePlanID     string   `json:"change_plan_id,omitempty"`
	OldReplicaCount  int      `json:"old_replica_count"`
}

func (p MigrationPreview) ValidateForApply() error {
	phases := map[string]bool{"expand": true, "backfill": true, "switch": true, "contract": true}
	if strings.TrimSpace(p.ReleaseID) == "" || !phases[p.Phase] || strings.TrimSpace(p.RollbackStrategy) == "" {
		return fmt.Errorf("migration.preview_invalid")
	}
	if len(p.Removals) > 0 && (strings.TrimSpace(p.BackupID) == "" || strings.TrimSpace(p.ChangePlanID) == "") {
		return fmt.Errorf("migration.destructive_evidence_required")
	}
	if p.Phase == "contract" && p.OldReplicaCount != 0 {
		return fmt.Errorf("migration.old_replicas_present")
	}
	return nil
}

func SortedArtifactKinds(artifacts []Artifact) []string {
	result := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		result = append(result, artifact.Kind)
	}
	sort.Strings(result)
	return result
}
