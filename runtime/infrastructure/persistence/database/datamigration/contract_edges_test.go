package datamigration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/timevalue"
)

func TestFileCheckpointStoreInputAndDecodeBoundaries(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	store := FileCheckpointStore{Path: filepath.Join(t.TempDir(), "checkpoint.json")}
	if _, err := store.Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled load error=%v", err)
	}
	missing, err := store.Load(t.Context())
	if err != nil || missing.Version != 1 || missing.Tables == nil {
		t.Fatalf("missing checkpoint=%#v err=%v", missing, err)
	}
	if err := (FileCheckpointStore{}).Save(t.Context(), Checkpoint{}); err == nil {
		t.Fatal("empty checkpoint path accepted")
	}

	directory := t.TempDir()
	if _, err := (FileCheckpointStore{Path: directory}).Load(t.Context()); err == nil || !strings.Contains(err.Error(), "read data migration checkpoint") {
		t.Fatalf("directory load error=%v", err)
	}
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (FileCheckpointStore{Path: path}).Load(t.Context()); err == nil || !strings.Contains(err.Error(), "parse data migration checkpoint") {
		t.Fatalf("parse error=%v", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (FileCheckpointStore{Path: path}).Load(t.Context()); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("version error=%v", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"tables":null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := (FileCheckpointStore{Path: path}).Load(t.Context())
	if err != nil || loaded.Tables == nil {
		t.Fatalf("nil tables checkpoint=%#v err=%v", loaded, err)
	}

	parentFile := filepath.Join(t.TempDir(), "parent")
	if err := os.WriteFile(parentFile, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (FileCheckpointStore{Path: filepath.Join(parentFile, "checkpoint.json")}).Save(t.Context(), Checkpoint{}); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("mkdir failure=%v", err)
	}
}

func TestFileCheckpointStoreAtomicWriteStages(t *testing.T) {
	wantErr := errors.New("injected checkpoint write failure")
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	invalidJSON := Checkpoint{Tables: map[string]TableCheckpoint{"records": {LastKey: json.RawMessage("{")}}}
	if err := (FileCheckpointStore{Path: path}).Save(t.Context(), invalidJSON); err == nil {
		t.Fatal("invalid checkpoint raw JSON encoded")
	}
	if err := (FileCheckpointStore{Path: path, createTemp: func(string, string) (checkpointTemporaryFile, error) { return nil, wantErr }}).Save(t.Context(), Checkpoint{}); !errors.Is(err, wantErr) {
		t.Fatalf("create temp error=%v", err)
	}
	for _, stage := range []string{"chmod", "write", "sync", "close"} {
		file := &checkpointFileStub{name: filepath.Join(t.TempDir(), "temporary"), failStage: stage, err: wantErr}
		store := FileCheckpointStore{Path: path, createTemp: func(string, string) (checkpointTemporaryFile, error) { return file, nil }}
		if err := store.Save(t.Context(), Checkpoint{}); !errors.Is(err, wantErr) {
			t.Fatalf("stage=%s error=%v", stage, err)
		}
	}
	file := &checkpointFileStub{name: filepath.Join(t.TempDir(), "temporary")}
	store := FileCheckpointStore{
		Path:       path,
		createTemp: func(string, string) (checkpointTemporaryFile, error) { return file, nil },
		rename:     func(string, string) error { return wantErr },
	}
	if err := store.Save(t.Context(), Checkpoint{}); !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "publish") {
		t.Fatalf("rename error=%v", err)
	}
}

func TestFileCopyLeaseInputCheckAndPersistenceBoundaries(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	validPath := filepath.Join(t.TempDir(), "copy.lease")
	if _, err := (FileCopyLease{Path: validPath}).Acquire(ctx, "fingerprint"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquire error=%v", err)
	}
	for _, input := range []FileCopyLease{{}, {Path: "relative"}, {Path: validPath}} {
		fingerprint := "fingerprint"
		if input.Path == validPath {
			fingerprint = ""
		}
		if _, err := input.Acquire(t.Context(), fingerprint); err == nil {
			t.Fatalf("invalid lease accepted: %#v fingerprint=%q", input, fingerprint)
		}
	}
	parentFile := filepath.Join(t.TempDir(), "parent")
	if err := os.WriteFile(parentFile, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (FileCopyLease{Path: filepath.Join(parentFile, "copy.lease")}).Acquire(t.Context(), "fingerprint"); err == nil {
		t.Fatal("lease mkdir failure accepted")
	}
	if _, err := (FileCopyLease{Path: t.TempDir()}).Acquire(t.Context(), "fingerprint"); err == nil {
		t.Fatal("directory lease path accepted")
	}
	wantErr := errors.New("injected lease I/O failure")
	if _, err := (FileCopyLease{Path: filepath.Join(t.TempDir(), "open.lease"), openFile: func(string, int, os.FileMode) (*os.File, error) { return nil, wantErr }}).Acquire(t.Context(), "fingerprint"); !errors.Is(err, wantErr) {
		t.Fatalf("open error=%v", err)
	}
	closedFile, err := os.CreateTemp(t.TempDir(), "closed-lease")
	if err != nil {
		t.Fatal(err)
	}
	if err := closedFile.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := (FileCopyLease{Path: filepath.Join(t.TempDir(), "chmod.lease"), openFile: func(string, int, os.FileMode) (*os.File, error) { return closedFile, nil }}).Acquire(t.Context(), "fingerprint"); err == nil {
		t.Fatal("closed lease file chmod succeeded")
	}
	if _, err := (FileCopyLease{Path: filepath.Join(t.TempDir(), "random.lease"), random: failingReader{err: wantErr}}).Acquire(t.Context(), "fingerprint"); !errors.Is(err, wantErr) {
		t.Fatalf("random error=%v", err)
	}
	if _, err := (FileCopyLease{Path: filepath.Join(t.TempDir(), "persist.lease"), persist: func(*fileCopyLeaseHandle) error { return wantErr }}).Acquire(t.Context(), "fingerprint"); !errors.Is(err, wantErr) {
		t.Fatalf("initial persist error=%v", err)
	}

	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	handle, err := (FileCopyLease{Path: validPath, Owner: " owner ", Now: func() time.Time { return now }}).Acquire(t.Context(), "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	typed := handle.(*fileCopyLeaseHandle)
	if typed.evidence.Owner != "owner" || !typed.evidence.HeartbeatAt.Equal(now) {
		t.Fatalf("lease evidence=%#v", typed.evidence)
	}
	if err := typed.Check(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled check error=%v", err)
	}
	now = now.Add(time.Minute)
	if err := typed.Check(t.Context()); err != nil || !typed.evidence.HeartbeatAt.Equal(now) {
		t.Fatalf("heartbeat=%v error=%v", typed.evidence.HeartbeatAt, err)
	}
	if err := typed.Release(); err != nil {
		t.Fatal(err)
	}
	if err := typed.Check(t.Context()); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed check error=%v", err)
	}
	if err := typed.Release(); err != nil {
		t.Fatalf("second release=%v", err)
	}

	brokenFile, err := os.CreateTemp(t.TempDir(), "broken-lease")
	if err != nil {
		t.Fatal(err)
	}
	broken := &fileCopyLeaseHandle{file: brokenFile, now: time.Now, evidence: fileCopyLeaseEvidence{}}
	if err := brokenFile.Close(); err != nil {
		t.Fatal(err)
	}
	if err := broken.persist(); err == nil {
		t.Fatal("closed lease persistence succeeded")
	}
	for _, stage := range []string{"truncate", "seek", "write", "sync"} {
		file := &leaseEvidenceFileStub{failStage: stage, err: wantErr}
		if err := persistLeaseEvidence(file, []byte("evidence")); !errors.Is(err, wantErr) {
			t.Fatalf("persist stage=%s error=%v", stage, err)
		}
	}
	if err := releaseLeaseFile(nil, func(*os.File) error { return wantErr }, func(*os.File) error { return errors.New("close") }); !errors.Is(err, wantErr) {
		t.Fatalf("unlock precedence error=%v", err)
	}
	if err := releaseLeaseFile(nil, func(*os.File) error { return nil }, func(*os.File) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("close error=%v", err)
	}
}

func TestPlanConversionOrderingAndDigestHelpers(t *testing.T) {
	columns := []ColumnInventory{
		{Name: "missing", Type: "text"},
		{Name: "flag", Type: "integer"},
		{Name: "created_at", Type: "text"},
		{Name: "payload", Type: "text"},
		{Name: "blob", Type: "blob"},
		{Name: "explicit", Type: "decimal"},
	}
	targetColumns := []ColumnInventory{
		{Name: "flag", Type: "boolean"},
		{Name: "created_at", Type: "timestamp with time zone"},
		{Name: "payload", Type: "jsonb"},
		{Name: "blob", Type: "bytea"},
		{Name: "explicit", Type: "uuid"},
	}
	source := Inventory{Engine: EngineSQLite, DatabaseBytes: 1000, Tables: []TableInventory{{Name: "child", Rows: 0, Columns: columns, PrimaryKey: []string{"a", "b"}, InvalidWorkspaceRows: 1, ForeignKeys: []ForeignInventory{{Columns: []string{"parent_id"}, ReferencedTable: "parent", ReferencedColumns: []string{"id"}}}}, {Name: "parent", Rows: 0, Columns: []ColumnInventory{{Name: "id", Type: "integer"}}}}}
	target := Inventory{Engine: EnginePostgres, Tables: []TableInventory{{Name: "child", Columns: targetColumns, Indexes: []IndexInventory{{Name: "same", Columns: []string{"flag"}}}, ForeignKeys: []ForeignInventory{{Columns: []string{"other"}, ReferencedTable: "parent", ReferencedColumns: []string{"id"}}}}}}
	plan := BuildPlan(source, target)
	if len(plan.Blockers) == 0 || len(plan.Warnings) < 5 || len(plan.Tables) != 2 || plan.Tables[0].Name != "parent" {
		t.Fatalf("plan=%#v", plan)
	}

	for _, test := range []struct {
		source, target string
		found          bool
		strategy       string
	}{
		{"varchar(20)", "text", true, "identity"},
		{"integer", "boolean", true, "sqlite_zero_one_to_boolean"},
		{"text", "timestamp", true, "rfc3339_text_to_timestamptz"},
		{"text", "json", true, "validate_json_text"},
		{"blob", "bytea", true, "blob_to_bytea"},
		{"decimal", "uuid", true, "explicit_cast_required"},
		{"real", "", false, "explicit_cast_required"},
		{"blob", "", false, "blob_to_bytea"},
	} {
		got := conversionFor(ColumnInventory{Name: "value", Type: test.source}, ColumnInventory{Type: test.target}, test.found)
		if got.Strategy != test.strategy {
			t.Fatalf("conversion %#v => %#v", test, got)
		}
	}
	if recommendedPostgresType("double", "value") != "double precision" || recommendedPostgresType("float", "value") != "double precision" || recommendedPostgresType("unknown", "created_at") != "text" || recommendedPostgresType("unknown", "value") != "text" {
		t.Fatal("recommended type variants failed")
	}
	if estimateTableBytes(Inventory{}, TableInventory{Rows: 2}) != 1024 || estimateTableBytes(Inventory{DatabaseBytes: 100}, TableInventory{Rows: 0}) != 0 || estimateTableBytes(Inventory{DatabaseBytes: 100, Tables: []TableInventory{{Rows: 0}}}, TableInventory{Rows: 1}) != 0 {
		t.Fatal("estimate variants failed")
	}
	if max64(2, 1) != 2 || max64(1, 2) != 2 || equalStrings([]string{"a"}, []string{"a", "b"}) || equalStrings([]string{"a"}, []string{"b"}) {
		t.Fatal("comparison variants failed")
	}
	if containsIndex([]IndexInventory{{Unique: false, Columns: []string{"id"}}}, IndexInventory{Unique: true, Columns: []string{"id"}}) {
		t.Fatal("index uniqueness ignored")
	}
	if containsForeignKey([]ForeignInventory{{ReferencedTable: "parent", Columns: []string{"id"}, ReferencedColumns: []string{"id"}}}, ForeignInventory{ReferencedTable: "other", Columns: []string{"id"}, ReferencedColumns: []string{"id"}}) {
		t.Fatal("foreign table ignored")
	}
	if containsForeignKey([]ForeignInventory{{ReferencedTable: "parent", Columns: []string{"id"}, ReferencedColumns: []string{"other"}}}, ForeignInventory{ReferencedTable: "parent", Columns: []string{"id"}, ReferencedColumns: []string{"id"}}) {
		t.Fatal("foreign referenced columns ignored")
	}
	if target, ok := matchTargetSequence(SequenceInventory{Name: "same"}, []SequenceInventory{{Name: "same"}}); !ok || target.Name != "same" {
		t.Fatal("same-name sequence not matched")
	}
	if _, ok := matchTargetSequence(SequenceInventory{Name: "source", OwnedTable: "table", OwnedColumn: "id"}, []SequenceInventory{{Name: "target", OwnedTable: "table", OwnedColumn: "other"}}); ok {
		t.Fatal("mismatched owned sequence accepted")
	}
	if _, ok := matchTargetSequence(SequenceInventory{Name: "source", OwnedTable: "table", OwnedColumn: "id"}, []SequenceInventory{{Name: "target", OwnedTable: "other", OwnedColumn: "id"}}); ok {
		t.Fatal("mismatched owned table sequence accepted")
	}
	if _, ok := matchTargetSequence(SequenceInventory{Name: "source"}, []SequenceInventory{{Name: "target", OwnedTable: "table", OwnedColumn: "id"}}); ok {
		t.Fatal("unowned sequence matched by ownership")
	}
	for _, conversion := range []ConversionPlan{
		conversionFor(ColumnInventory{Name: "value", Type: "text"}, ColumnInventory{Type: "boolean"}, true),
		conversionFor(ColumnInventory{Name: "value", Type: "integer"}, ColumnInventory{Type: "timestamp"}, true),
		conversionFor(ColumnInventory{Name: "value", Type: "text"}, ColumnInventory{Type: "bytea"}, true),
	} {
		if conversion.Strategy != "explicit_cast_required" {
			t.Fatalf("expected explicit conversion: %#v", conversion)
		}
	}

	cycleInventory := Inventory{Tables: []TableInventory{{Name: "a", ForeignKeys: []ForeignInventory{{ReferencedTable: "b"}}}, {Name: "b", ForeignKeys: []ForeignInventory{{ReferencedTable: "a"}}}}}
	plans := []TablePlan{{Name: "a"}, {Name: "b"}}
	ordered, blockers := orderPlansByForeignKeys(cycleInventory, plans)
	if len(blockers) != 1 || len(ordered) != 2 {
		t.Fatalf("cycle ordered=%#v blockers=%#v", ordered, blockers)
	}
	ordered, blockers = orderPlansByForeignKeys(Inventory{Tables: []TableInventory{{Name: "a", ForeignKeys: []ForeignInventory{{ReferencedTable: "a"}, {ReferencedTable: "outside"}}}}}, []TablePlan{{Name: "a"}})
	if len(blockers) != 0 || len(ordered) != 1 {
		t.Fatalf("self/outside dependency ordered=%#v blockers=%#v", ordered, blockers)
	}
	if sourcePlaceholder(EnginePostgres, 2) != "$2" || sourcePlaceholder(EngineMySQL, 2) != "?" || (Copier{SourceEngine: EnginePostgres, Plan: Plan{Source: Inventory{Schema: "public"}}}).sourceRelation("records") != `"public"."records"` {
		t.Fatal("source SQL variants failed")
	}
	if got := canonicalRow([]any{nil, []byte{1}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("offset", 3600)), true, 42}); !json.Valid(got) {
		t.Fatalf("canonical row=%s", got)
	}
	if rollDigest("not-hex", []byte("row")) == "" {
		t.Fatal("digest fallback failed")
	}
}

func TestRetirementInputAndCutoverEvidenceDecodeBoundaries(t *testing.T) {
	if _, err := LoadRetirementEvidenceInput(""); err == nil {
		t.Fatal("empty retirement evidence accepted")
	}
	if _, err := LoadRetirementEvidenceInput(filepath.Join(t.TempDir(), "missing.json")); err == nil || !strings.Contains(err.Error(), "read retirement") {
		t.Fatalf("missing retirement error=%v", err)
	}
	invalid := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(invalid, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRetirementEvidenceInput(invalid); err == nil || !strings.Contains(err.Error(), "parse retirement") {
		t.Fatalf("parse retirement error=%v", err)
	}
	valid := filepath.Join(t.TempDir(), "valid.json")
	if err := os.WriteFile(valid, []byte(`{"owner":"owner"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if input, err := LoadRetirementEvidenceInput(valid); err != nil || input.Owner != "owner" {
		t.Fatalf("input=%#v err=%v", input, err)
	}

	if _, err := ValidateCutoverEvidence("", time.Now()); err == nil {
		t.Fatal("empty cutover evidence accepted")
	}
	if _, err := ValidateCutoverEvidence(filepath.Join(t.TempDir(), "missing.json"), time.Now()); err == nil || !strings.Contains(err.Error(), "read cutover") {
		t.Fatalf("missing cutover error=%v", err)
	}
	if _, err := ValidateCutoverEvidence(invalid, time.Now()); err == nil || !strings.Contains(err.Error(), "parse cutover") {
		t.Fatalf("parse cutover error=%v", err)
	}
}

func TestRetirementAndCutoverEveryValidationOutcome(t *testing.T) {
	now := time.Now().UTC()
	validCheck := RetirementBusinessCheck{Name: "count", SourceValue: "1", ReplacementValue: "1", EvidenceRef: "query"}
	validVerification := VerificationReport{Current: true, Tables: []TableVerification{{SourceDuplicateRows: 1, TargetDuplicateRows: 2, SourceInvalidWorkspaces: 3, TargetInvalidWorkspaces: 4, SourceInvalidReferences: 5, TargetInvalidReferences: 6}}}
	validInput := RetirementEvidenceInput{Owner: "owner", Replacement: "replacement", Disposition: "migrate", RecordedAt: now, BusinessChecks: []RetirementBusinessCheck{validCheck}}
	if report := BuildRetirementEvidence(validVerification, validInput, time.Time{}); !report.Ready || report.DuplicateRows != 3 || report.InvalidWorkspaceRows != 7 || report.InvalidReferenceRows != 11 || report.OrphanRows != 11 {
		t.Fatalf("valid report=%#v", report)
	}
	variants := []RetirementEvidenceInput{
		{Replacement: "replacement", Disposition: "migrate", RecordedAt: now, BusinessChecks: []RetirementBusinessCheck{validCheck}},
		{Owner: "owner", Disposition: "migrate", RecordedAt: now, BusinessChecks: []RetirementBusinessCheck{validCheck}},
		{Owner: "owner", Replacement: "replacement", Disposition: "invalid", RecordedAt: now, BusinessChecks: []RetirementBusinessCheck{validCheck}},
		{Owner: "owner", Replacement: "replacement", Disposition: "anonymize", RecordedAt: now, BusinessChecks: []RetirementBusinessCheck{validCheck}},
		{Owner: "owner", Replacement: "replacement", Disposition: "anonymize", DispositionApproval: "approved", RecordedAt: now, BusinessChecks: []RetirementBusinessCheck{validCheck}},
		{Owner: "owner", Replacement: "replacement", Disposition: "discard", RecordedAt: now, BusinessChecks: []RetirementBusinessCheck{validCheck}},
		{Owner: "owner", Replacement: "replacement", Disposition: "retain", BusinessChecks: []RetirementBusinessCheck{validCheck}},
		{Owner: "owner", Replacement: "replacement", Disposition: "archive", RecordedAt: now.Add(6 * time.Minute), BusinessChecks: []RetirementBusinessCheck{validCheck}},
		{Owner: "owner", Replacement: "replacement", Disposition: "archive", RecordedAt: now},
	}
	for index, input := range variants {
		report := BuildRetirementEvidence(validVerification, input, now)
		if index != 4 && len(report.Blockers) == 0 {
			t.Fatalf("variant %d unexpectedly ready: %#v", index, report)
		}
	}
	for index := 0; index < 4; index++ {
		check := validCheck
		switch index {
		case 0:
			check.Name = ""
		case 1:
			check.SourceValue = ""
		case 2:
			check.ReplacementValue = ""
		case 3:
			check.EvidenceRef = ""
		}
		input := validInput
		input.BusinessChecks = []RetirementBusinessCheck{check}
		if report := BuildRetirementEvidence(validVerification, input, now); len(report.Blockers) == 0 {
			t.Fatalf("incomplete check %d accepted", index)
		}
	}
	mismatch := validInput
	mismatch.BusinessChecks = []RetirementBusinessCheck{{Name: "count", SourceValue: "1", ReplacementValue: "2", EvidenceRef: "query"}}
	if report := BuildRetirementEvidence(VerificationReport{}, mismatch, now); len(report.Blockers) != 2 {
		t.Fatalf("mismatch report=%#v", report)
	}

	path := filepath.Join(t.TempDir(), "cutover.json")
	write := func(evidence CutoverEvidence) error {
		raw, err := timevalue.MarshalJSON(evidence)
		if err != nil {
			return err
		}
		return os.WriteFile(path, raw, 0o600)
	}
	valid := CutoverEvidence{Owner: "owner", RecordedAt: now, SourceStopWrite: true, WorkersDrained: true, SourceSnapshotID: "snapshot", FinalDeltaID: "delta", RollbackTarget: "source", SourceInventorySHA: strings.Repeat("a", 64)}
	if err := write(valid); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateCutoverEvidence(path, time.Time{}); err != nil {
		t.Fatalf("zero now rejected: %v", err)
	}
	for index := 0; index < 5; index++ {
		candidate := valid
		switch index {
		case 0:
			candidate.Owner = ""
		case 1:
			candidate.SourceSnapshotID = ""
		case 2:
			candidate.FinalDeltaID = ""
		case 3:
			candidate.RollbackTarget = ""
		case 4:
			candidate.SourceInventorySHA = "short"
		}
		if err := write(candidate); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateCutoverEvidence(path, now); err == nil || !strings.Contains(err.Error(), "identity") {
			t.Fatalf("identity variant %d error=%v", index, err)
		}
	}
	for _, candidate := range []CutoverEvidence{
		func() CutoverEvidence { value := valid; value.SourceStopWrite = false; return value }(),
		func() CutoverEvidence { value := valid; value.WorkersDrained = false; return value }(),
		func() CutoverEvidence { value := valid; value.RecordedAt = time.Time{}; return value }(),
		func() CutoverEvidence { value := valid; value.RecordedAt = now.Add(6 * time.Minute); return value }(),
		func() CutoverEvidence { value := valid; value.RecordedAt = now.Add(-2 * time.Hour); return value }(),
	} {
		if err := write(candidate); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateCutoverEvidence(path, now); err == nil {
			t.Fatalf("invalid cutover accepted: %#v", candidate)
		}
	}
}

type checkpointFileStub struct {
	name      string
	failStage string
	err       error
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

type leaseEvidenceFileStub struct {
	failStage string
	err       error
}

func (f *leaseEvidenceFileStub) Truncate(int64) error {
	if f.failStage == "truncate" {
		return f.err
	}
	return nil
}
func (f *leaseEvidenceFileStub) Seek(int64, int) (int64, error) {
	if f.failStage == "seek" {
		return 0, f.err
	}
	return 0, nil
}
func (f *leaseEvidenceFileStub) Write(payload []byte) (int, error) {
	if f.failStage == "write" {
		return 0, f.err
	}
	return len(payload), nil
}
func (f *leaseEvidenceFileStub) Sync() error {
	if f.failStage == "sync" {
		return f.err
	}
	return nil
}

func (f *checkpointFileStub) Name() string { return f.name }
func (f *checkpointFileStub) Chmod(os.FileMode) error {
	if f.failStage == "chmod" {
		return f.err
	}
	return nil
}
func (f *checkpointFileStub) Write([]byte) (int, error) {
	if f.failStage == "write" {
		return 0, f.err
	}
	return 1, nil
}
func (f *checkpointFileStub) Sync() error {
	if f.failStage == "sync" {
		return f.err
	}
	return nil
}
func (f *checkpointFileStub) Close() error {
	if f.failStage == "close" {
		return f.err
	}
	return nil
}
