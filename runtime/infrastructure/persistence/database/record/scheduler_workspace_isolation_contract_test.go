package record

import (
	"path/filepath"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestSchedulerRecordStateWorkspaceIsolationContract(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "scheduler-workspace.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB().Exec(`CREATE TABLE job_run (
		workspace_id TEXT NOT NULL,
		id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		status TEXT,
		lease_owner TEXT,
		fencing_token INTEGER,
		UNIQUE (workspace_id, id)
	)`); err != nil {
		t.Fatal(err)
	}

	repository := NewRecordStore(store)
	object := definitionmodel.ObjectSchema{Key: "job_run", Fields: []definitionmodel.FieldSchema{
		{Key: "status", Type: "text"},
		{Key: "lease_owner", Type: "text"},
		{Key: "fencing_token", Type: "number"},
	}}
	workspaceA, workspaceB := "workspace-a", "workspace-b"
	shared := recordmodel.Record{ID: "shared-run", CreatedAt: "v1", UpdatedAt: "v1", Data: map[string]any{"status": "queued", "fencing_token": 1}}
	if err := repository.InsertRecord(t.Context(), workspaceA, object, shared); err != nil {
		t.Fatal(err)
	}
	if err := repository.InsertRecord(t.Context(), workspaceB, object, shared); err != nil {
		t.Fatalf("scheduler run identity must be workspace-local: %v", err)
	}

	leased := shared
	leased.UpdatedAt = "v2"
	leased.Data = map[string]any{"status": "leased", "lease_owner": "worker-a", "fencing_token": 2}
	updated, err := repository.UpdateRecordWhere(t.Context(), workspaceA, object, leased, map[string]any{"status": "queued", "fencing_token": 1})
	if err != nil || !updated {
		t.Fatalf("workspace A lease claim: updated=%v err=%v", updated, err)
	}
	workspaceBRun, found, err := repository.GetRecord(t.Context(), workspaceB, object, shared.ID)
	if err != nil || !found || workspaceBRun.Data["status"] != "queued" || workspaceBRun.Data["lease_owner"] != nil {
		t.Fatalf("workspace A claim changed workspace B: record=%+v found=%v err=%v", workspaceBRun, found, err)
	}

	if err := repository.CommitRecordMutationBatch(t.Context(), workspaceA, []transactionmodel.RecordMutationCommit{{Operation: "delete", Object: object, Record: leased}}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := repository.GetRecord(t.Context(), workspaceB, object, shared.ID); err != nil || !found {
		t.Fatalf("workspace A completion/delete affected workspace B: found=%v err=%v", found, err)
	}
	if _, err := repository.ListRecords(t.Context(), "", object, recordmodel.RecordListQuery{}); err == nil {
		t.Fatal("scheduler state access without workspace must be rejected")
	}
}
