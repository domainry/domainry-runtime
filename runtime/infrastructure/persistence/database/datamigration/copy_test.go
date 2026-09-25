package datamigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/timevalue"

	_ "modernc.org/sqlite"
)

func TestFileCheckpointIsAtomicPrivateAndResumable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "checkpoint.json")
	store := FileCheckpointStore{Path: path}
	want := Checkpoint{Version: 1, PlanFingerprint: strings.Repeat("a", 64), Tables: map[string]TableCheckpoint{"records": {LastKey: json.RawMessage(`"record-2"`), Processed: 2, Digest: strings.Repeat("b", 64)}}}
	if err := store.Save(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("checkpoint mode = %o", info.Mode().Perm())
	}
	got, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.PlanFingerprint != want.PlanFingerprint || got.Tables["records"].Processed != 2 || string(got.Tables["records"].LastKey) != `"record-2"` {
		t.Fatalf("checkpoint mismatch: %+v", got)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.Save(cancelled, got); err == nil {
		t.Fatal("cancelled checkpoint save succeeded")
	}
}

func TestCheckpointCarriesPauseResumeProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	store := FileCheckpointStore{Path: path}
	want := Checkpoint{Version: 1, State: "paused", Batches: 3, Tables: map[string]TableCheckpoint{"records": {Processed: 1500, LastKey: json.RawMessage(`"record-1500"`)}}}
	if err := store.Save(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "paused" || got.Batches != 3 || got.Tables["records"].Processed != 1500 {
		t.Fatalf("checkpoint=%+v", got)
	}
	got.State = "running"
	if err := store.Save(t.Context(), got); err != nil {
		t.Fatal(err)
	}
}

func TestCutoverEvidenceRequiresStopWriteDrainAndFreshIdentity(t *testing.T) {
	now := time.Now().UTC()
	path := filepath.Join(t.TempDir(), "cutover.json")
	evidence := CutoverEvidence{Owner: "database-platform", RecordedAt: now, SourceStopWrite: true, WorkersDrained: true, SourceSnapshotID: "snapshot-1", FinalDeltaID: "delta-1", RollbackTarget: "source-runtime", SourceInventorySHA: strings.Repeat("c", 64)}
	writeCutoverEvidence(t, path, evidence)
	if _, err := ValidateCutoverEvidence(path, now); err != nil {
		t.Fatalf("valid cutover evidence rejected: %v", err)
	}
	evidence.WorkersDrained = false
	writeCutoverEvidence(t, path, evidence)
	if _, err := ValidateCutoverEvidence(path, now); err == nil || !strings.Contains(err.Error(), "worker drain") {
		t.Fatalf("undrained cutover accepted: %v", err)
	}
	evidence.WorkersDrained = true
	evidence.RecordedAt = now.Add(-2 * time.Hour)
	writeCutoverEvidence(t, path, evidence)
	if _, err := ValidateCutoverEvidence(path, now); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale cutover accepted: %v", err)
	}
}

func TestFileCopyLeaseIsExclusiveAndReacquirable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copy.lease")
	lease := FileCopyLease{Path: path, Owner: "migration-a"}
	first, err := lease.Acquire(t.Context(), strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lease.Acquire(t.Context(), strings.Repeat("a", 64)); err == nil {
		t.Fatal("concurrent copy lease was accepted")
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := lease.Acquire(t.Context(), strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("released copy lease was not reacquirable: %v", err)
	}
	_ = second.Release()
}

func TestCopierLeaseLossAfterCommitIsSafeToRestartAndRepeat(t *testing.T) {
	source := openCopySQLite(t, filepath.Join(t.TempDir(), "source.db"))
	targetPath := filepath.Join(t.TempDir(), "target.db")
	target := openCopySQLite(t, filepath.Join(t.TempDir(), "target-main.db"))
	if _, err := target.ExecContext(t.Context(), `ATTACH DATABASE ? AS domainry_runtime`, targetPath); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ExecContext(t.Context(), `CREATE TABLE records (id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL); INSERT INTO records VALUES ('1', 'workspace-a'), ('2', 'workspace-a')`); err != nil {
		t.Fatal(err)
	}
	if _, err := target.ExecContext(t.Context(), `CREATE TABLE domainry_runtime.records (id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	sourceInventory, err := Inspect(t.Context(), source, EngineSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	targetInventory := sourceInventory
	targetInventory.Engine, targetInventory.Schema = EnginePostgres, "domainry_runtime"
	checkpointPath := filepath.Join(t.TempDir(), "checkpoint.json")
	copier := Copier{Source: source, Target: target, SourceEngine: EngineSQLite, TargetSchema: "domainry_runtime", Plan: BuildPlan(sourceInventory, targetInventory), Checkpoints: FileCheckpointStore{Path: checkpointPath}, Options: CopyOptions{BatchSize: 1}, Lease: losingCopyLease{failAt: 3}}
	if _, err := copier.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "lease lost") {
		t.Fatalf("injected lease loss not reported: %v", err)
	}
	copier.Lease = nil
	checkpoint, err := copier.Run(t.Context())
	if err != nil || checkpoint.State != "completed" || checkpoint.Tables["records"].Processed != 2 {
		t.Fatalf("restart checkpoint=%+v err=%v", checkpoint, err)
	}
	if _, err := copier.Run(t.Context()); err != nil {
		t.Fatalf("completed copy repeat failed: %v", err)
	}
	var rows int
	if err := target.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domainry_runtime.records`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("lease-loss restart duplicated or lost rows: rows=%d err=%v", rows, err)
	}
}

type losingCopyLease struct{ failAt int }

func (l losingCopyLease) Acquire(context.Context, string) (CopyLeaseHandle, error) {
	return &losingCopyLeaseHandle{failAt: l.failAt}, nil
}

type losingCopyLeaseHandle struct {
	checks int
	failAt int
}

func (h *losingCopyLeaseHandle) Check(context.Context) error {
	h.checks++
	if h.checks >= h.failAt {
		return errors.New("injected lease loss")
	}
	return nil
}

func (*losingCopyLeaseHandle) Release() error { return nil }

func openCopySQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func writeCutoverEvidence(t *testing.T, path string, evidence CutoverEvidence) {
	t.Helper()
	raw, err := timevalue.MarshalJSON(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
