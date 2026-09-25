package datamigration

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/timevalue"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

func TestSQLiteToPostgresCopyResumeVerifyAndFinalDelta(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("RUNTIME_DATA_MIGRATION_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("RUNTIME_DATA_MIGRATION_POSTGRES_DSN is not configured")
	}
	target, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	const schema = "domainry_migration_contract"
	if _, err := target.ExecContext(t.Context(), `DROP SCHEMA IF EXISTS domainry_migration_contract CASCADE; CREATE SCHEMA domainry_migration_contract; CREATE TABLE domainry_migration_contract.parent_records (id TEXT PRIMARY KEY); CREATE TABLE domainry_migration_contract.child_records (id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, parent_id TEXT NOT NULL, active BOOLEAN NOT NULL, payload BYTEA)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = target.ExecContext(t.Context(), `DROP SCHEMA IF EXISTS domainry_migration_contract CASCADE`)
	})

	source := openSQLiteInventoryFixture(t)
	if _, err := source.ExecContext(t.Context(), `UPDATE child_records SET workspace_id = 'workspace-a' WHERE workspace_id = ''`); err != nil {
		t.Fatal(err)
	}
	sourceInventory, err := Inspect(t.Context(), source, EngineSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	targetInventory, err := Inspect(t.Context(), target, EnginePostgres, schema)
	if err != nil {
		t.Fatal(err)
	}
	plan := BuildPlan(sourceInventory, targetInventory)
	if len(plan.Blockers) != 0 {
		t.Fatalf("copy plan blocked: %+v", plan.Blockers)
	}
	if len(plan.Tables) != 2 || plan.Tables[0].Name != "parent_records" || plan.Tables[1].Name != "child_records" {
		t.Fatalf("foreign-key copy order is unsafe: %+v", plan.Tables)
	}
	childPlan := plan.Tables[1]
	if len(childPlan.DeferredIndexes) != 1 || len(childPlan.DeferredForeignKeys) != 1 {
		t.Fatalf("high-cost target objects were not deferred explicitly: %+v", childPlan)
	}
	checkpointPath := filepath.Join(t.TempDir(), "checkpoint.json")
	copier := Copier{Source: source, Target: target, SourceEngine: EngineSQLite, TargetSchema: schema, Plan: plan, Checkpoints: FileCheckpointStore{Path: checkpointPath}, Options: CopyOptions{BatchSize: 1}}
	checkpoint, err := copier.Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !checkpoint.Tables["parent_records"].Done || !checkpoint.Tables["child_records"].Done || checkpoint.Tables["child_records"].Processed != 2 {
		t.Fatalf("copy checkpoint incomplete: %+v", checkpoint)
	}
	if _, err := copier.Run(t.Context()); err != nil {
		t.Fatalf("idempotent checkpoint replay failed: %v", err)
	}
	report, err := copier.Verify(t.Context())
	if err != nil || !report.Current {
		t.Fatalf("initial verification failed: report=%+v err=%v", report, err)
	}
	finalized, err := copier.Finalize(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(finalized.IndexesCreated) != 1 || len(finalized.ForeignKeysCreated) != 1 {
		t.Fatalf("deferred objects were not finalized: %+v", finalized)
	}
	finalInventory, err := Inspect(t.Context(), target, EnginePostgres, schema)
	if err != nil {
		t.Fatal(err)
	}
	finalChild, found := findTable(finalInventory.Tables, "child_records")
	if !found || !containsIndex(finalChild.Indexes, childPlan.DeferredIndexes[0]) || !containsForeignKey(finalChild.ForeignKeys, childPlan.DeferredForeignKeys[0]) {
		t.Fatalf("finalized target objects are not introspectable: %+v", finalChild)
	}

	if _, err := source.ExecContext(t.Context(), `UPDATE child_records SET active = 0 WHERE id = 'child-a'; INSERT INTO child_records (id, workspace_id, parent_id, active, payload) VALUES ('child-new', 'workspace-b', 'parent-a', 1, X'05')`); err != nil {
		t.Fatal(err)
	}
	sourceInventory, err = Inspect(t.Context(), source, EngineSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	plan = BuildPlan(sourceInventory, targetInventory)
	copier.Plan = plan
	if stale, err := copier.Verify(t.Context()); err != nil || stale.Current {
		t.Fatalf("source delta was not detected: report=%+v err=%v", stale, err)
	}
	cutoverPath := filepath.Join(t.TempDir(), "cutover.json")
	cutover := CutoverEvidence{Owner: "database-platform", RecordedAt: time.Now().UTC(), SourceStopWrite: true, WorkersDrained: true, SourceSnapshotID: "sqlite-snapshot-1", FinalDeltaID: "final-delta-1", RollbackTarget: "sqlite-source", SourceInventorySHA: strings.Repeat("d", 64)}
	raw, _ := timevalue.MarshalJSON(cutover)
	if err := os.WriteFile(cutoverPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := copier.RunFinalDelta(t.Context(), cutoverPath); err != nil {
		t.Fatal(err)
	}
	report, err = copier.Verify(t.Context())
	if err != nil || !report.Current {
		t.Fatalf("final delta verification failed: report=%+v err=%v", report, err)
	}
}

func TestPostgresPublicToRuntimeSchemaCopyAndVerify(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("RUNTIME_DATA_MIGRATION_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("RUNTIME_DATA_MIGRATION_POSTGRES_DSN is not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(t.Context(), `DROP TABLE IF EXISTS public.runtime_legacy_records; DROP SCHEMA IF EXISTS domainry_public_migration_contract CASCADE; CREATE SCHEMA domainry_public_migration_contract; CREATE TABLE public.runtime_legacy_records (id BIGSERIAL PRIMARY KEY, workspace_id TEXT NOT NULL, payload JSONB NOT NULL); CREATE TABLE domainry_public_migration_contract.runtime_legacy_records (id BIGSERIAL PRIMARY KEY, workspace_id TEXT NOT NULL, payload JSONB NOT NULL); CREATE UNIQUE INDEX uniq_public_migration_workspace ON domainry_public_migration_contract.runtime_legacy_records(workspace_id, id); INSERT INTO public.runtime_legacy_records (id, workspace_id, payload) VALUES (41, 'workspace-a', '{"status":"ready"}'); SELECT setval('public.runtime_legacy_records_id_seq', 41, true)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(t.Context(), `DROP TABLE IF EXISTS public.runtime_legacy_records; DROP SCHEMA IF EXISTS domainry_public_migration_contract CASCADE`)
	})
	source, err := Inspect(t.Context(), db, EnginePostgres, "public")
	if err != nil {
		t.Fatal(err)
	}
	target, err := Inspect(t.Context(), db, EnginePostgres, "domainry_public_migration_contract")
	if err != nil {
		t.Fatal(err)
	}
	plan := BuildPlan(source, target)
	if len(plan.Blockers) != 0 {
		t.Fatalf("public-schema migration plan blocked: %+v", plan.Blockers)
	}
	copier := Copier{Source: db, Target: db, SourceEngine: EnginePostgres, TargetSchema: "domainry_public_migration_contract", Plan: plan, Checkpoints: FileCheckpointStore{Path: filepath.Join(t.TempDir(), "public-checkpoint.json")}, Options: CopyOptions{BatchSize: 1}}
	if _, err := copier.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	var nextSequenceValue int64
	if err := db.QueryRowContext(t.Context(), `SELECT nextval('domainry_public_migration_contract.runtime_legacy_records_id_seq')`).Scan(&nextSequenceValue); err != nil {
		t.Fatal(err)
	}
	if nextSequenceValue != 42 {
		t.Fatalf("target sequence was not synchronized: got %d want 42", nextSequenceValue)
	}
	report, err := copier.Verify(t.Context())
	if err != nil || !report.Current {
		t.Fatalf("public-schema migration verification failed: report=%+v err=%v", report, err)
	}
}
