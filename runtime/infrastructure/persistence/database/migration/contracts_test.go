package migration

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompatibilityRejectsUnknownOldAndNewerSchemas(t *testing.T) {
	contract := Compatibility{RuntimeVersion: "2.4.0", MinSchemaVersion: "002", MaxSchemaVersion: "004"}
	tests := []struct {
		current string
		known   bool
		state   string
	}{
		{"001", true, StatePending}, {"003", false, StateUnknown}, {"005", true, StateNewer}, {"004", true, StateCurrent},
	}
	for _, test := range tests {
		state, _ := contract.Evaluate(test.current, test.known)
		if state != test.state {
			t.Fatalf("current=%s known=%v state=%s want=%s", test.current, test.known, state, test.state)
		}
	}
}

func TestBackupEnvelopeAuthenticatesEveryChunk(t *testing.T) {
	dir := t.TempDir()
	source, encrypted, restored := filepath.Join(dir, "source"), filepath.Join(dir, "backup.enc"), filepath.Join(dir, "restored")
	payload := bytes.Repeat([]byte("runtime-backup-evidence"), (backupChunkSize/23)+100)
	if err := os.WriteFile(source, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, 32)
	if err := EncryptBackupFile(source, encrypted, key); err != nil {
		t.Fatal(err)
	}
	if err := DecryptBackupFile(encrypted, restored, key); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(restored)
	if err != nil || !bytes.Equal(actual, payload) {
		t.Fatalf("restored bytes mismatch: err=%v", err)
	}
	raw, _ := os.ReadFile(encrypted)
	raw[len(raw)-1] ^= 1
	if err := os.WriteFile(encrypted, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := DecryptBackupFile(encrypted, filepath.Join(dir, "tampered"), key); err == nil {
		t.Fatal("tampered backup was accepted")
	}
}

func TestBackupEvidenceRequiresConsistentRecoveryBoundary(t *testing.T) {
	now := time.Now().UTC()
	evidence := BackupEvidence{
		EvidenceVersion: "1", BackupID: "backup-1", Engine: "postgres", BackupType: "base_backup", Provider: "provider", Region: "ap-east-1", FaultDomain: "az-2", Owner: "database-platform",
		CreatedAt: now.Add(-time.Minute), VerifiedAt: now, SchemaVersion: "004", Checksum: "sha256:db", Size: 10, Encrypted: true, ImmutableUntil: now.Add(30 * 24 * time.Hour), RPOSeconds: 60, RTOSeconds: 300, IntegrityOK: true,
		Artifacts: []Artifact{{Kind: "database", Checksum: "db", Size: 1}, {Kind: "manifest", Checksum: "manifest", Size: 1}, {Kind: "uploads", Checksum: "uploads", Size: 1}, {Kind: "key_versions", Checksum: "keys", Size: 1}, {Kind: "runtime_artifact", Checksum: "runtime", Size: 1}},
	}
	if err := evidence.Validate(now); err != nil {
		t.Fatalf("valid evidence rejected: %v", err)
	}
	evidence.Artifacts = evidence.Artifacts[:4]
	if err := evidence.Validate(now); err == nil {
		t.Fatal("incomplete recovery boundary accepted")
	}
}

func TestRestoreAndContractPhaseRequireSafetyEvidence(t *testing.T) {
	now := time.Now().UTC()
	evidence := BackupEvidence{BackupID: "backup-1", CreatedAt: now.Add(-time.Hour)}
	request := RestoreRequest{Engine: "postgres", Target: "production", BackupID: "backup-1", Operator: "oncall", ChangePlanID: "change-1"}
	if err := request.Validate(evidence); err == nil {
		t.Fatal("restore without maintenance and drain evidence accepted")
	}
	request.DryRun = true
	if err := request.Validate(evidence); err != nil {
		t.Fatalf("dry-run rejected: %v", err)
	}
	preview := MigrationPreview{ReleaseID: "release-1", Phase: "contract", Removals: []string{"legacy_column"}, RollbackStrategy: "backup_restore", BackupID: "backup-1", ChangePlanID: "change-1", OldReplicaCount: 1}
	if err := preview.ValidateForApply(); err == nil {
		t.Fatal("contract phase accepted while old replicas remain")
	}
	preview.OldReplicaCount = 0
	if err := preview.ValidateForApply(); err != nil {
		t.Fatalf("safe contract preview rejected: %v", err)
	}
}

func TestDrillEvidenceRequiresNoDuplicateTerminalSideEffect(t *testing.T) {
	now := time.Now().UTC()
	checks := map[string]bool{"checksum": true, "schema": true, "critical_tables": true, "workspace_counts": true, "outbox": true, "leases": true, "idempotency_receipts": true, "no_duplicate_terminal_effect": true}
	evidence := DrillEvidence{DrillID: "drill-1", Scenario: "bad_migration", Engine: "sqlite", BackupID: "backup-1", StartedAt: now.Add(-time.Minute), CompletedAt: now, ActualRPO: 0, ActualRTO: 60, Checks: checks, Reconciliation: RestoreReconciliationPlan()}
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	delete(checks, "no_duplicate_terminal_effect")
	if err := evidence.Validate(); err == nil {
		t.Fatal("drill without terminal side-effect check accepted")
	}
}
