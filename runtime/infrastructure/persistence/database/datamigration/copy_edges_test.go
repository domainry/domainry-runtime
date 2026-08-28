package datamigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCopierRunValidationLeaseCheckpointAndSequenceBoundaries(t *testing.T) {
	db := openCopySQLite(t, filepath.Join(t.TempDir(), "empty.db"))
	wantErr := errors.New("injected copier failure")
	valid := Copier{Source: db, Target: db, TargetSchema: "runtime", Checkpoints: &memoryCheckpointStore{checkpoint: Checkpoint{Tables: map[string]TableCheckpoint{}}}}
	for _, candidate := range []Copier{{}, {Source: db}, {Source: db, Target: db}} {
		if _, err := candidate.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "requires source") {
			t.Fatalf("invalid dependencies error=%v", err)
		}
	}
	candidate := valid
	candidate.Plan.Blockers = []string{"blocked"}
	if _, err := candidate.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "has blockers") {
		t.Fatalf("blocker error=%v", err)
	}
	candidate = valid
	candidate.TargetSchema = "unsafe.schema"
	if _, err := candidate.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "unsafe target") {
		t.Fatalf("schema error=%v", err)
	}
	candidate = valid
	candidate.Lease = copyLeaseStub{err: wantErr}
	if _, err := candidate.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "acquire data migration") {
		t.Fatalf("lease error=%v", err)
	}
	candidate = valid
	candidate.Checkpoints = &memoryCheckpointStore{loadErr: wantErr}
	if _, err := candidate.Run(t.Context()); !errors.Is(err, wantErr) {
		t.Fatalf("checkpoint load error=%v", err)
	}
	fingerprint := PlanFingerprint(valid.Plan)
	candidate.Checkpoints = &memoryCheckpointStore{checkpoint: Checkpoint{PlanFingerprint: fingerprint + "changed"}}
	if _, err := candidate.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "plan changed") {
		t.Fatalf("fingerprint error=%v", err)
	}

	for _, size := range []int{-1, 10001} {
		store := &memoryCheckpointStore{}
		candidate = valid
		candidate.Options.BatchSize = size
		candidate.Checkpoints = store
		checkpoint, err := candidate.Run(t.Context())
		if err != nil || checkpoint.State != "completed" || checkpoint.Tables == nil || len(store.saved) != 1 {
			t.Fatalf("size=%d checkpoint=%#v saved=%d err=%v", size, checkpoint, len(store.saved), err)
		}
	}

	candidate = valid
	candidate.Plan.Tables = []TablePlan{{Name: "done"}}
	candidate.Checkpoints = &memoryCheckpointStore{checkpoint: Checkpoint{Tables: map[string]TableCheckpoint{"done": {Done: true}}}}
	if checkpoint, err := candidate.Run(t.Context()); err != nil || checkpoint.State != "completed" {
		t.Fatalf("done checkpoint=%#v err=%v", checkpoint, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	candidate.Checkpoints = &memoryCheckpointStore{checkpoint: Checkpoint{Tables: map[string]TableCheckpoint{}}}
	if _, err := candidate.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled run error=%v", err)
	}

	candidate = valid
	candidate.Lease = copyLeaseStub{handle: &copyLeaseHandleStub{checkErr: wantErr}}
	candidate.Plan.Tables = []TablePlan{{Name: "done"}}
	candidate.Checkpoints = &memoryCheckpointStore{checkpoint: Checkpoint{Tables: map[string]TableCheckpoint{"done": {Done: true}}}}
	if _, err := candidate.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "lease lost") {
		t.Fatalf("lease check error=%v", err)
	}

	for _, sequence := range []SequencePlan{{SourceName: "source", Blocked: true}, {SourceName: "source", TargetName: "unsafe.target"}} {
		candidate = valid
		candidate.Plan.Sequences = []SequencePlan{sequence}
		candidate.Checkpoints = &memoryCheckpointStore{}
		if _, err := candidate.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "safe target mapping") {
			t.Fatalf("sequence=%#v error=%v", sequence, err)
		}
	}
	candidate = valid
	candidate.Plan.Sequences = []SequencePlan{{SourceName: "source", TargetName: "sequence", CurrentValue: 4}}
	candidate.Checkpoints = &memoryCheckpointStore{}
	if _, err := candidate.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "synchronize target sequence") {
		t.Fatalf("sequence execution error=%v", err)
	}

	candidate = valid
	candidate.Checkpoints = &memoryCheckpointStore{saveErrAt: 1, saveErr: wantErr}
	if _, err := candidate.Run(t.Context()); !errors.Is(err, wantErr) {
		t.Fatalf("completion save error=%v", err)
	}
}

func TestCopierPauseThrottleAndFinalDelta(t *testing.T) {
	source := openCopySQLite(t, filepath.Join(t.TempDir(), "source.db"))
	target := openCopySQLite(t, filepath.Join(t.TempDir(), "target.db"))
	targetPath := filepath.Join(t.TempDir(), "attached.db")
	if _, err := target.ExecContext(t.Context(), `ATTACH DATABASE ? AS runtime`, targetPath); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ExecContext(t.Context(), `CREATE TABLE records (id TEXT PRIMARY KEY); INSERT INTO records VALUES ('1'), ('2')`); err != nil {
		t.Fatal(err)
	}
	if _, err := target.ExecContext(t.Context(), `CREATE TABLE runtime.records (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	plan := Plan{Source: Inventory{Engine: EngineSQLite, Tables: []TableInventory{{Name: "records", Columns: []ColumnInventory{{Name: "id", Type: "text"}}, PrimaryKey: []string{"id"}}}}, Target: Inventory{Engine: EnginePostgres, Schema: "runtime"}, Tables: []TablePlan{{Name: "records", CheckpointKey: []string{"id"}, Conversions: []ConversionPlan{{Column: "id", Strategy: "identity"}}}}}
	store := &memoryCheckpointStore{}
	copier := Copier{Source: source, Target: target, SourceEngine: EngineSQLite, TargetSchema: "runtime", Plan: plan, Checkpoints: store, Options: CopyOptions{BatchSize: 1, MaxBatches: 1}}
	paused, err := copier.Run(t.Context())
	if err != nil || paused.State != "paused" || paused.Tables["records"].Done || len(store.saved) != 2 {
		t.Fatalf("paused=%#v saves=%d err=%v", paused, len(store.saved), err)
	}

	store.checkpoint = paused
	copier.Options = CopyOptions{BatchSize: 1, Throttle: time.Nanosecond}
	completed, err := copier.Run(t.Context())
	if err != nil || completed.State != "completed" || completed.Tables["records"].Processed != 2 {
		t.Fatalf("completed=%#v err=%v", completed, err)
	}

	store.checkpoint = Checkpoint{Tables: map[string]TableCheckpoint{}}
	store.saved = nil
	ctx, cancel := context.WithCancel(t.Context())
	copier.Options = CopyOptions{BatchSize: 1, Throttle: time.Hour}
	copier.Lease = copyLeaseStub{handle: &copyLeaseHandleStub{onCheck: func(check int) {
		if check == 3 {
			cancel()
		}
	}}}
	if _, err := copier.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("throttle cancellation error=%v", err)
	}

	evidencePath := filepath.Join(t.TempDir(), "cutover.json")
	writeCutoverEvidence(t, evidencePath, validCutoverEvidence(time.Now().UTC()))
	empty := Copier{Source: source, Target: target, SourceEngine: EngineSQLite, TargetSchema: "runtime", Plan: Plan{}, Checkpoints: &memoryCheckpointStore{checkpoint: completed}}
	final, err := empty.RunFinalDelta(t.Context(), evidencePath)
	if err != nil || final.State != "completed" || final.PlanFingerprint == "" {
		t.Fatalf("final=%#v err=%v", final, err)
	}
	if _, err := empty.RunFinalDelta(t.Context(), filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("invalid final delta evidence accepted")
	}
	empty.Checkpoints = &memoryCheckpointStore{loadErr: errors.New("load")}
	if _, err := empty.RunFinalDelta(t.Context(), evidencePath); err == nil || err.Error() != "load" {
		t.Fatalf("final load error=%v", err)
	}
	empty.Checkpoints = &memoryCheckpointStore{saveErrAt: 1, saveErr: errors.New("save")}
	if _, err := empty.RunFinalDelta(t.Context(), evidencePath); err == nil || err.Error() != "save" {
		t.Fatalf("final save error=%v", err)
	}

	for _, failure := range []struct {
		name   string
		failAt int
	}{
		{name: "inner lease", failAt: 2},
		{name: "post batch lease", failAt: 3},
	} {
		t.Run(failure.name, func(t *testing.T) {
			candidate := copier
			candidate.Options = CopyOptions{BatchSize: 1}
			candidate.Checkpoints = &memoryCheckpointStore{}
			candidate.Lease = copyLeaseStub{handle: &copyLeaseHandleStub{checkErr: errors.New("lost"), failAt: failure.failAt}}
			if _, err := candidate.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "lease lost") {
				t.Fatalf("lease failure=%v", err)
			}
		})
	}
	for _, failure := range []struct {
		name      string
		saveErrAt int
		maxBatch  int
	}{
		{name: "batch checkpoint save", saveErrAt: 1},
		{name: "pause checkpoint save", saveErrAt: 2, maxBatch: 1},
	} {
		t.Run(failure.name, func(t *testing.T) {
			candidate := copier
			candidate.Options = CopyOptions{BatchSize: 1, MaxBatches: failure.maxBatch}
			candidate.Checkpoints = &memoryCheckpointStore{saveErrAt: failure.saveErrAt, saveErr: errors.New("save stage")}
			candidate.Lease = nil
			if _, err := candidate.Run(t.Context()); err == nil || err.Error() != "save stage" {
				t.Fatalf("save failure=%v", err)
			}
		})
	}
	candidate := copier
	candidate.Plan.Tables = append([]TablePlan(nil), copier.Plan.Tables...)
	candidate.Plan.Tables[0].CheckpointKey = nil
	candidate.Checkpoints = &memoryCheckpointStore{}
	candidate.Lease = nil
	if _, err := candidate.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "checkpoint key") {
		t.Fatalf("copy error=%v", err)
	}

	oneRowSource := openCopySQLite(t, filepath.Join(t.TempDir(), "one-row.db"))
	if _, err := oneRowSource.ExecContext(t.Context(), `CREATE TABLE records (id TEXT PRIMARY KEY); INSERT INTO records VALUES ('1')`); err != nil {
		t.Fatal(err)
	}
	candidate = copier
	candidate.Source = oneRowSource
	candidate.Options = CopyOptions{BatchSize: 2, MaxBatches: 1}
	candidate.Checkpoints = &memoryCheckpointStore{}
	candidate.Lease = nil
	if checkpoint, err := candidate.Run(t.Context()); err != nil || checkpoint.State != "completed" {
		t.Fatalf("short final batch checkpoint=%#v err=%v", checkpoint, err)
	}

	candidate = copier
	candidate.Options = CopyOptions{BatchSize: 1, MaxBatches: 2}
	candidate.Checkpoints = &memoryCheckpointStore{}
	candidate.Lease = nil
	if checkpoint, err := candidate.Run(t.Context()); err != nil || checkpoint.State != "paused" || checkpoint.Batches != 2 {
		t.Fatalf("second-batch pause checkpoint=%#v err=%v", checkpoint, err)
	}

	scriptedTarget := sql.OpenDB(migrationSQLConnector{state: &migrationSQLState{}})
	defer scriptedTarget.Close()
	candidate = Copier{Source: source, Target: scriptedTarget, TargetSchema: "runtime", Plan: Plan{Sequences: []SequencePlan{{SourceName: "source", TargetName: "sequence"}}}, Checkpoints: &memoryCheckpointStore{}}
	if checkpoint, err := candidate.Run(t.Context()); err != nil || checkpoint.State != "completed" {
		t.Fatalf("sequence success checkpoint=%#v err=%v", checkpoint, err)
	}
}

func TestCopyBatchAndConversionInputBoundaries(t *testing.T) {
	source := openCopySQLite(t, filepath.Join(t.TempDir(), "source.db"))
	target := openCopySQLite(t, filepath.Join(t.TempDir(), "target.db"))
	if _, err := target.ExecContext(t.Context(), `ATTACH DATABASE ? AS runtime`, filepath.Join(t.TempDir(), "attached.db")); err != nil {
		t.Fatal(err)
	}
	baseTable := TableInventory{Name: "records", Columns: []ColumnInventory{{Name: "id", Type: "text"}, {Name: "workspace_id", Type: "text"}}, PrimaryKey: []string{"id"}}
	basePlan := TablePlan{Name: "records", CheckpointKey: []string{"id"}, Conversions: []ConversionPlan{{Column: "id", Strategy: "identity"}, {Column: "workspace_id", Strategy: "identity"}}}
	copier := Copier{Source: source, Target: target, SourceEngine: EngineSQLite, TargetSchema: "runtime", Plan: Plan{Source: Inventory{Tables: []TableInventory{baseTable}}}}
	if _, _, err := copier.copyBatch(t.Context(), TablePlan{Name: "records"}, TableCheckpoint{}, 1); err == nil || !strings.Contains(err.Error(), "one checkpoint") {
		t.Fatalf("checkpoint key error=%v", err)
	}
	if _, _, err := copier.copyBatch(t.Context(), TablePlan{Name: "missing", CheckpointKey: []string{"id"}}, TableCheckpoint{}, 1); err == nil || !strings.Contains(err.Error(), "missing from plan") {
		t.Fatalf("source table error=%v", err)
	}
	copier.Plan.Source.Tables[0].Columns[0].Name = "unsafe.name"
	if _, _, err := copier.copyBatch(t.Context(), basePlan, TableCheckpoint{}, 1); err == nil || !strings.Contains(err.Error(), "unsafe source column") {
		t.Fatalf("unsafe column error=%v", err)
	}
	baseTable.Columns = []ColumnInventory{{Name: "id", Type: "text"}, {Name: "workspace_id", Type: "text"}}
	copier.Plan.Source.Tables[0] = baseTable
	if _, _, err := copier.copyBatch(t.Context(), basePlan, TableCheckpoint{LastKey: json.RawMessage("{")}, 1); err == nil || !strings.Contains(err.Error(), "decode checkpoint") {
		t.Fatalf("checkpoint decode error=%v", err)
	}
	if _, err := source.ExecContext(t.Context(), `CREATE TABLE records (id TEXT PRIMARY KEY, workspace_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := target.ExecContext(t.Context(), `CREATE TABLE runtime.records (id TEXT PRIMARY KEY, workspace_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ExecContext(t.Context(), `INSERT INTO records VALUES ('1', '')`); err != nil {
		t.Fatal(err)
	}
	workspacePlan := basePlan
	workspacePlan.WorkspaceScoped = true
	if _, _, err := copier.copyBatch(t.Context(), workspacePlan, TableCheckpoint{}, 1); err == nil || !strings.Contains(err.Error(), "without workspace_id") {
		t.Fatalf("empty workspace error=%v", err)
	}
	workspacePlan.CheckpointKey = []string{"id"}
	copier.Plan.Source.Tables[0].Columns = []ColumnInventory{{Name: "id", Type: "text"}}
	if _, _, err := copier.copyBatch(t.Context(), workspacePlan, TableCheckpoint{}, 1); err == nil {
		t.Fatal("workspace plan without workspace column accepted")
	}

	columns := []string{"flag", "created_at", "payload", "blob", "nil", "unsupported"}
	plan := TablePlan{Conversions: []ConversionPlan{
		{Column: "flag", Strategy: "sqlite_zero_one_to_boolean"},
		{Column: "created_at", Strategy: "rfc3339_text_to_timestamptz"},
		{Column: "payload", Strategy: "validate_json_text"},
		{Column: "blob", Strategy: "blob_to_bytea"},
		{Column: "nil", Strategy: "identity"},
		{Column: "unsupported", Strategy: "unsupported"},
	}}
	values := []any{int64(1), "2026-07-20T12:00:00Z", `{"ok":true}`, []byte{1}, nil, "value"}
	if _, err := convertRow(TableInventory{}, plan, columns, values); err == nil || !strings.Contains(err.Error(), "unsupported conversion") {
		t.Fatalf("unsupported conversion error=%v", err)
	}
	plan.Conversions[len(plan.Conversions)-1].Strategy = "identity"
	converted, err := convertRow(TableInventory{}, plan, columns, values)
	if err != nil || converted[0] != true {
		t.Fatalf("converted=%#v err=%v", converted, err)
	}
	for _, invalid := range []struct {
		column, strategy string
		value            any
		message          string
	}{{"flag", "sqlite_zero_one_to_boolean", 2, "reversible"}, {"created_at", "rfc3339_text_to_timestamptz", "bad", "RFC3339"}, {"payload", "validate_json_text", "{", "invalid JSON"}} {
		if _, err := convertRow(TableInventory{}, TablePlan{Conversions: []ConversionPlan{{Column: invalid.column, Strategy: invalid.strategy}}}, []string{invalid.column}, []any{invalid.value}); err == nil || !strings.Contains(err.Error(), invalid.message) {
			t.Fatalf("invalid %#v error=%v", invalid, err)
		}
	}

	if _, err := target.ExecContext(t.Context(), `CREATE TABLE runtime.only_id (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	tx, err := target.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := upsertPostgresRow(t.Context(), tx, "runtime", "only_id", []string{"id"}, "id", []any{"1"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func validCutoverEvidence(now time.Time) CutoverEvidence {
	return CutoverEvidence{Owner: "owner", RecordedAt: now, SourceStopWrite: true, WorkersDrained: true, SourceSnapshotID: "snapshot", FinalDeltaID: "delta", RollbackTarget: "source", SourceInventorySHA: strings.Repeat("a", 64)}
}

type memoryCheckpointStore struct {
	checkpoint Checkpoint
	loadErr    error
	saveErrAt  int
	saveErr    error
	saved      []Checkpoint
}

func (s *memoryCheckpointStore) Load(context.Context) (Checkpoint, error) {
	return s.checkpoint, s.loadErr
}
func (s *memoryCheckpointStore) Save(_ context.Context, checkpoint Checkpoint) error {
	s.saved = append(s.saved, checkpoint)
	if s.saveErrAt > 0 && len(s.saved) == s.saveErrAt {
		return s.saveErr
	}
	s.checkpoint = checkpoint
	return nil
}

type copyLeaseStub struct {
	handle CopyLeaseHandle
	err    error
}

func (s copyLeaseStub) Acquire(context.Context, string) (CopyLeaseHandle, error) {
	if s.handle == nil && s.err == nil {
		s.handle = &copyLeaseHandleStub{}
	}
	return s.handle, s.err
}

type copyLeaseHandleStub struct {
	checks   int
	checkErr error
	failAt   int
	onCheck  func(int)
}

func (h *copyLeaseHandleStub) Check(context.Context) error {
	h.checks++
	if h.onCheck != nil {
		h.onCheck(h.checks)
	}
	if h.checkErr != nil && (h.failAt == 0 || h.checks >= h.failAt) {
		return h.checkErr
	}
	return nil
}
func (*copyLeaseHandleStub) Release() error { return nil }

var _ CheckpointStore = (*memoryCheckpointStore)(nil)
var _ CopyLease = copyLeaseStub{}
