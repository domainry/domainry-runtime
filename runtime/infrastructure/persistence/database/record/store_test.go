package record

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"errors"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	"os"
	"path/filepath"
	"testing"
	"time"

	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRecordStoreImplementsContractAndCancelsSQL(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	repository := NewRecordStore(store)
	var _ recordrepository.RecordRepository = repository
	object := definitionmodel.ObjectSchema{Key: "context_record", Name: "Context Record", Fields: []definitionmodel.FieldSchema{{Key: "status", Name: "Status", Type: "text"}}}
	if _, err := store.DB().Exec(`CREATE TABLE context_record (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, status TEXT, UNIQUE (workspace_id, id))`); err != nil {
		t.Fatal(err)
	}
	if err := repository.InsertRecord(t.Context(), "workspace-primary", object, recordmodel.Record{ID: "record_1", CreatedAt: "v1", UpdatedAt: "v1", Data: map[string]any{"status": "pending"}}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := repository.GetRecord(ctx, "workspace-primary", object, "record_1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancelled query, got %v", err)
	}
	expired, expiredCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expiredCancel()
	if _, err := repository.ListRecords(expired, "workspace-primary", object, recordmodel.RecordListQuery{Page: 1, PageSize: 10, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected SQL deadline error, got %v", err)
	}
	commit := transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: recordmodel.Record{ID: "record_1", CreatedAt: "v1", UpdatedAt: "v2", Data: map[string]any{"status": "approved"}}}
	if err := repository.CommitRecordMutation(ctx, "workspace-primary", commit); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancelled transaction, got %v", err)
	}
	record, found, err := repository.GetRecord(t.Context(), "workspace-primary", object, "record_1")
	if err != nil || !found || record.Data["status"] != "pending" {
		t.Fatalf("cancelled mutation changed record: record=%#v found=%v err=%v", record, found, err)
	}
}

func openRuntimeStore(t *testing.T) *persistence.RuntimeStore {
	t.Helper()
	migration := filepath.Join(t.TempDir(), "001_empty.sql")
	if err := os.WriteFile(migration, []byte("-- context record test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "context.db"), MigrationSQL: migration})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
