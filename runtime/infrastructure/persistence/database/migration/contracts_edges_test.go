package migration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

func validBackupEvidence(now time.Time) BackupEvidence {
	return BackupEvidence{
		EvidenceVersion: "1", BackupID: "backup", Engine: "postgres", BackupType: "full", Provider: "provider", Region: "region", FaultDomain: "zone", Owner: "owner",
		CreatedAt: now.Add(-time.Minute), VerifiedAt: now, SchemaVersion: "1", Checksum: "checksum", Size: 1, Encrypted: true,
		ImmutableUntil: now.Add(time.Hour), RPOSeconds: 0, RTOSeconds: 1, IntegrityOK: true,
		Artifacts: []Artifact{
			{Kind: "database", Checksum: "database", Size: 0},
			{Kind: "manifest", Checksum: "manifest", Size: 0},
			{Kind: "uploads", Checksum: "uploads", Size: 0},
			{Kind: "key_versions", Checksum: "keys", Size: 0},
			{Kind: "runtime_artifact", Checksum: "runtime", Size: 0},
		},
	}
}

func TestBackupEvidenceValidationRejectsEveryProtectionBoundary(t *testing.T) {
	now := time.Now().UTC()
	valid := validBackupEvidence(now)
	tests := []struct {
		name   string
		mutate func(*BackupEvidence)
		code   string
	}{
		{name: "backup-id", mutate: func(value *BackupEvidence) { value.BackupID = " " }, code: "backup.evidence_identity_required"},
		{name: "engine", mutate: func(value *BackupEvidence) { value.Engine = " " }, code: "backup.evidence_identity_required"},
		{name: "provider", mutate: func(value *BackupEvidence) { value.Provider = " " }, code: "backup.evidence_identity_required"},
		{name: "owner", mutate: func(value *BackupEvidence) { value.Owner = " " }, code: "backup.evidence_identity_required"},
		{name: "created", mutate: func(value *BackupEvidence) { value.CreatedAt = time.Time{} }, code: "backup.evidence_timestamps_invalid"},
		{name: "verified-before", mutate: func(value *BackupEvidence) { value.VerifiedAt = value.CreatedAt.Add(-time.Second) }, code: "backup.evidence_timestamps_invalid"},
		{name: "verified-future", mutate: func(value *BackupEvidence) { value.VerifiedAt = now.Add(6 * time.Minute) }, code: "backup.evidence_timestamps_invalid"},
		{name: "integrity", mutate: func(value *BackupEvidence) { value.IntegrityOK = false }, code: "backup.integrity_evidence_required"},
		{name: "checksum", mutate: func(value *BackupEvidence) { value.Checksum = "" }, code: "backup.integrity_evidence_required"},
		{name: "size", mutate: func(value *BackupEvidence) { value.Size = 0 }, code: "backup.integrity_evidence_required"},
		{name: "encrypted", mutate: func(value *BackupEvidence) { value.Encrypted = false }, code: "backup.protection_evidence_required"},
		{name: "immutable", mutate: func(value *BackupEvidence) { value.ImmutableUntil = now.Add(-time.Second) }, code: "backup.protection_evidence_required"},
		{name: "region", mutate: func(value *BackupEvidence) { value.Region = "" }, code: "backup.protection_evidence_required"},
		{name: "fault-domain", mutate: func(value *BackupEvidence) { value.FaultDomain = "" }, code: "backup.protection_evidence_required"},
		{name: "objectives", mutate: func(value *BackupEvidence) { value.RPOSeconds = -1 }, code: "backup.recovery_objectives_required"},
		{name: "rto", mutate: func(value *BackupEvidence) { value.RTOSeconds = 0 }, code: "backup.recovery_objectives_required"},
		{name: "schema-version", mutate: func(value *BackupEvidence) { value.SchemaVersion = "" }, code: "backup.recovery_objectives_required"},
		{name: "artifact-kind", mutate: func(value *BackupEvidence) { value.Artifacts[0].Kind = "" }, code: "backup.artifact_manifest_invalid"},
		{name: "artifact-checksum", mutate: func(value *BackupEvidence) { value.Artifacts[0].Checksum = "" }, code: "backup.artifact_manifest_invalid"},
		{name: "artifact-size", mutate: func(value *BackupEvidence) { value.Artifacts[0].Size = -1 }, code: "backup.artifact_manifest_invalid"},
		{name: "artifact-missing", mutate: func(value *BackupEvidence) { value.Artifacts = value.Artifacts[1:] }, code: "backup.artifact_manifest_missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			value.Artifacts = append([]Artifact(nil), valid.Artifacts...)
			test.mutate(&value)
			if err := value.Validate(now); err == nil || !strings.Contains(err.Error(), test.code) {
				t.Fatalf("validation error=%v want %s", err, test.code)
			}
		})
	}

	artifactPath := filepath.Join(t.TempDir(), "database.bin")
	if err := os.WriteFile(artifactPath, []byte("database"), 0o600); err != nil {
		t.Fatal(err)
	}
	value := validBackupEvidence(now)
	value.Artifacts[0].Path = artifactPath
	if err := value.Validate(now); err == nil || !strings.Contains(err.Error(), "backup.artifact_integrity_mismatch") {
		t.Fatalf("artifact mismatch=%v", err)
	}
	actual, err := FileArtifact("database", artifactPath, "key-v1")
	if err != nil {
		t.Fatal(err)
	}
	value.Artifacts[0] = actual
	if err := value.Validate(now); err != nil {
		t.Fatalf("actual artifact rejected: %v", err)
	}
	value.Artifacts[0].Path = filepath.Join(t.TempDir(), "missing-artifact")
	if err := value.Validate(now); err != nil {
		t.Fatalf("external artifact path absence should defer to recorded evidence: %v", err)
	}
	value = validBackupEvidence(now)
	value.Artifacts[0].Path = artifactPath
	value.Artifacts[0].Checksum = actual.Checksum
	value.Artifacts[0].Size = actual.Size + 1
	if err := value.Validate(now); err == nil || !strings.Contains(err.Error(), "backup.artifact_integrity_mismatch") {
		t.Fatalf("artifact size mismatch=%v", err)
	}
	originalOpen := openArtifactFile
	openArtifactFile = func(string) (artifactFile, error) {
		return migrationArtifactFile{reader: migrationErrorReadCloser{}, info: mustFileInfo(t, artifactPath)}, nil
	}
	value.Artifacts[0].Size = actual.Size
	if err := value.Validate(now); err == nil || !strings.Contains(err.Error(), "backup.artifact_integrity_mismatch") {
		t.Fatalf("artifact read failure=%v", err)
	}
	openArtifactFile = originalOpen
}

func TestBackupEvidenceFileAndJSONIOEdges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact, err := FileArtifact("database", path, "key")
	if err != nil || artifact.Size != 7 || artifact.Checksum == "" || artifact.KeyVersion != "key" {
		t.Fatalf("artifact=%+v err=%v", artifact, err)
	}
	if _, err := FileArtifact("database", filepath.Join(dir, "missing"), ""); err == nil {
		t.Fatal("missing artifact accepted")
	}
	if _, err := FileArtifact("database", dir, ""); err == nil {
		t.Fatal("directory artifact accepted")
	}

	evidencePath := filepath.Join(dir, "nested", "evidence.json")
	evidence := validBackupEvidence(time.Now().UTC())
	if err := WriteJSONEvidence(evidencePath, evidence); err != nil {
		t.Fatal(err)
	}
	if read, err := ReadBackupEvidence(evidencePath); err != nil || read.BackupID != evidence.BackupID {
		t.Fatalf("read evidence=%+v err=%v", read, err)
	}
	if err := WriteJSONEvidence(filepath.Join(dir, "invalid.json"), func() {}); err == nil {
		t.Fatal("unencodable evidence accepted")
	}
	blockingParent := filepath.Join(dir, "parent-file")
	if err := os.WriteFile(blockingParent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSONEvidence(filepath.Join(blockingParent, "evidence.json"), evidence); err == nil {
		t.Fatal("evidence write through file parent succeeded")
	}
	if _, err := ReadBackupEvidence(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("missing evidence accepted")
	}
	malformed := filepath.Join(dir, "malformed.json")
	if err := os.WriteFile(malformed, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBackupEvidence(malformed); err == nil {
		t.Fatal("malformed evidence accepted")
	}
	invalidEvidence := filepath.Join(dir, "invalid-evidence.json")
	if err := os.WriteFile(invalidEvidence, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBackupEvidence(invalidEvidence); err == nil {
		t.Fatal("structurally invalid evidence accepted")
	}
	renameTarget := filepath.Join(dir, "rename-target")
	if err := os.MkdirAll(filepath.Join(renameTarget, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSONEvidence(renameTarget, evidence); err == nil {
		t.Fatal("evidence rename over non-empty directory succeeded")
	}
}

func TestRestoreDrillPreviewAndCommandEdgeContracts(t *testing.T) {
	now := time.Now().UTC()
	evidence := validBackupEvidence(now)
	request := RestoreRequest{Target: "target", BackupID: evidence.BackupID, Operator: "operator", ChangePlanID: "change", DryRun: true}
	if err := request.Validate(evidence); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*RestoreRequest){
		func(value *RestoreRequest) { value.Target = "" },
		func(value *RestoreRequest) { value.BackupID = "" },
		func(value *RestoreRequest) { value.BackupID = "other" },
		func(value *RestoreRequest) { value.Operator = "" },
		func(value *RestoreRequest) { value.ChangePlanID = "" },
		func(value *RestoreRequest) { value.DryRun = false },
		func(value *RestoreRequest) { value.DryRun = false; value.MaintenanceEvidence = "maintenance" },
		func(value *RestoreRequest) { value.PointInTime = evidence.CreatedAt.Add(time.Second) },
	} {
		value := request
		mutate(&value)
		if err := value.Validate(evidence); err == nil {
			t.Fatalf("invalid restore accepted: %+v", value)
		}
	}
	pitr := now.Add(-30 * time.Second)
	evidence.PITRTarget = &pitr
	request.PointInTime = evidence.CreatedAt.Add(time.Second)
	if err := request.Validate(evidence); err != nil {
		t.Fatalf("valid PITR rejected: %v", err)
	}
	request.PointInTime = now.Add(time.Minute)
	if err := request.Validate(evidence); err == nil {
		t.Fatal("future PITR accepted")
	}
	request.PointInTime = evidence.CreatedAt.Add(-time.Second)
	if err := request.Validate(evidence); err == nil {
		t.Fatal("PITR before backup creation accepted")
	}
	request = RestoreRequest{Target: "target", BackupID: evidence.BackupID, Operator: "operator", ChangePlanID: "change", MaintenanceEvidence: "maintenance", DrainEvidence: "drain"}
	if err := request.Validate(evidence); err != nil {
		t.Fatalf("fully evidenced non-dry-run rejected: %v", err)
	}

	checks := map[string]bool{"checksum": true, "schema": true, "critical_tables": true, "workspace_counts": true, "outbox": true, "leases": true, "idempotency_receipts": true, "no_duplicate_terminal_effect": true}
	drill := DrillEvidence{DrillID: "drill", BackupID: "backup", Scenario: "operator_error", StartedAt: now.Add(-time.Minute), CompletedAt: now, ActualRPO: 0, ActualRTO: 1, Checks: checks}
	if err := drill.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*DrillEvidence){
		func(value *DrillEvidence) { value.DrillID = "" },
		func(value *DrillEvidence) { value.BackupID = "" },
		func(value *DrillEvidence) { value.CompletedAt = value.StartedAt.Add(-time.Second) },
		func(value *DrillEvidence) { value.ActualRPO = -1 },
		func(value *DrillEvidence) { value.ActualRTO = 0 },
		func(value *DrillEvidence) { value.CompletedAt = now.Add(6 * time.Minute) },
		func(value *DrillEvidence) {
			value.CompletedAt = now.Add(-31 * 24 * time.Hour)
			value.StartedAt = value.CompletedAt.Add(-time.Minute)
		},
		func(value *DrillEvidence) { value.Scenario = "unknown" },
		func(value *DrillEvidence) { value.Checks = map[string]bool{} },
	} {
		value := drill
		mutate(&value)
		if err := value.Validate(); err == nil {
			t.Fatalf("invalid drill accepted: %+v", value)
		}
	}

	for _, preview := range []MigrationPreview{
		{},
		{ReleaseID: "release", Phase: "unknown", RollbackStrategy: "rollback"},
		{ReleaseID: "release", Phase: "expand"},
		{ReleaseID: "release", Phase: "expand", RollbackStrategy: "rollback", Removals: []string{"column"}},
		{ReleaseID: "release", Phase: "expand", RollbackStrategy: "rollback", Removals: []string{"column"}, BackupID: "backup"},
		{ReleaseID: "release", Phase: "contract", RollbackStrategy: "rollback", OldReplicaCount: 1},
	} {
		if err := preview.ValidateForApply(); err == nil {
			t.Fatalf("invalid preview accepted: %+v", preview)
		}
	}
	if err := (MigrationPreview{ReleaseID: "release", Phase: "expand", RollbackStrategy: "rollback"}).ValidateForApply(); err != nil {
		t.Fatal(err)
	}
	if got := SortedArtifactKinds([]Artifact{{Kind: "z"}, {Kind: "a"}}); len(got) != 2 || got[0] != "a" || got[1] != "z" {
		t.Fatalf("artifact kinds=%v", got)
	}

	if spec, err := DatabaseBackupCommand(" postgres ", "dsn", "target"); err != nil || spec.Executable != "pg_dump" {
		t.Fatalf("postgres backup=%+v err=%v", spec, err)
	}
	for _, build := range []func(string, string, string) (CommandSpec, error){DatabaseBackupCommand, DatabaseRestoreCommand} {
		if _, err := build("unknown", "dsn", "path"); err == nil {
			t.Fatal("unknown database command accepted")
		}
		if _, err := build("mysql", "invalid", "path"); err == nil {
			t.Fatal("invalid MySQL DSN accepted")
		}
	}
	args := mysqlConnectionArguments(&mysqldriver.Config{Addr: "db.example:invalid", User: "user", TLSConfig: "custom"})
	if strings.Contains(strings.Join(args, " "), "--port") || !strings.Contains(strings.Join(args, " "), "VERIFY_IDENTITY") {
		t.Fatalf("mysql args=%v", args)
	}
	args = mysqlConnectionArguments(&mysqldriver.Config{Addr: "db.example", User: "user"})
	if len(args) != 4 || args[1] != "db.example" {
		t.Fatalf("mysql host-only args=%v", args)
	}
	if CompareVersions(" 1.2_release ", "1.2.0") != 0 || CompareVersions("1.x", "1.1") >= 0 || CompareVersions("1", "1.0.1") >= 0 || CompareVersions("1.0.1", "1") <= 0 {
		t.Fatal("exported version comparison normalization mismatch")
	}
	if policy := RollbackPolicy("unknown"); policy.Mode != "unsupported" || !policy.RequiresVerifiedBackup {
		t.Fatalf("unknown rollback policy=%+v", policy)
	}
}

func mustFileInfo(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
