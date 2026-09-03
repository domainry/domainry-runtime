package record

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRecordStoreWorkspaceIsolationContract(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "record-workspace.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB().Exec(`CREATE TABLE workspace_record (
		workspace_id TEXT NOT NULL,
		id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		email TEXT,
		UNIQUE (workspace_id, id),
		UNIQUE (workspace_id, email)
	)`); err != nil {
		t.Fatal(err)
	}

	repository := NewRecordStore(store)
	object := definitionmodel.ObjectSchema{Key: "workspace_record", Fields: []definitionmodel.FieldSchema{{Key: "email", Type: "text", Unique: true}}}
	workspaceA, workspaceB := "workspace-a", "workspace-b"
	recordA := recordmodel.Record{ID: "shared-id", CreatedAt: "v1", UpdatedAt: "v1", Data: map[string]any{"email": "shared@example.com"}}
	recordB := recordmodel.Record{ID: "shared-id", CreatedAt: "v1", UpdatedAt: "v1", Data: map[string]any{"email": "shared@example.com"}}
	if err := repository.InsertRecord(t.Context(), workspaceA, object, recordA); err != nil {
		t.Fatal(err)
	}
	if err := repository.InsertRecord(t.Context(), workspaceB, object, recordB); err != nil {
		t.Fatalf("same id and unique value must be reusable in another workspace: %v", err)
	}

	for _, workspaceID := range []string{workspaceA, workspaceB} {
		page, err := repository.ListRecords(t.Context(), workspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: 10, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted})
		if err != nil || page.Total != 1 || len(page.Items) != 1 {
			t.Fatalf("workspace %s list leaked or lost records: page=%+v err=%v", workspaceID, page, err)
		}
		if _, leaked := page.Items[0].Data["workspace_id"]; leaked {
			t.Fatalf("workspace discriminator leaked into business data: %+v", page.Items[0].Data)
		}
		exists, err := repository.UniqueExists(t.Context(), workspaceID, object.Key, "email", "", "shared@example.com")
		if err != nil || !exists {
			t.Fatalf("workspace %s unique lookup: exists=%v err=%v", workspaceID, exists, err)
		}
	}

	recordA.Data["email"] = "workspace-a@example.com"
	recordA.UpdatedAt = "v2"
	if err := repository.CommitRecordMutation(t.Context(), workspaceA, transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: recordA, ExpectedUpdatedAt: "v1"}); err != nil {
		t.Fatal(err)
	}
	unchangedB, found, err := repository.GetRecord(t.Context(), workspaceB, object, recordB.ID)
	if err != nil || !found || unchangedB.Data["email"] != "shared@example.com" {
		t.Fatalf("workspace A update affected workspace B: record=%+v found=%v err=%v", unchangedB, found, err)
	}

	if err := repository.DeleteRecord(t.Context(), workspaceA, object, recordA.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := repository.GetRecord(t.Context(), workspaceA, object, recordA.ID); err != nil || found {
		t.Fatalf("workspace A delete failed: found=%v err=%v", found, err)
	}
	if _, found, err := repository.GetRecord(t.Context(), workspaceB, object, recordB.ID); err != nil || !found {
		t.Fatalf("workspace A delete affected workspace B: found=%v err=%v", found, err)
	}

	if _, err := repository.ListRecords(t.Context(), "", object, recordmodel.RecordListQuery{}); err == nil {
		t.Fatal("missing workspace scope must be rejected")
	}
}

func TestTwoRuntimeStoreInstancesProcessDifferentWorkspacesConcurrently(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "two-runtime-workspaces.db")
	open := func() *database.RuntimeStore {
		store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: databasePath})
		if err != nil {
			t.Fatal(err)
		}
		return store
	}
	first, second := open(), open()
	defer first.Close()
	defer second.Close()
	if _, err := first.DB().Exec(`CREATE TABLE two_runtime_workspace_record (
		workspace_id TEXT NOT NULL,
		id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		value_text TEXT,
		UNIQUE (workspace_id, id)
	)`); err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "two_runtime_workspace_record", Fields: []definitionmodel.FieldSchema{{Key: "value_text", Type: "text"}}}
	firstRepository, secondRepository := NewRecordStore(first), NewRecordStore(second)
	for _, seed := range []struct {
		repository  RecordStore
		workspaceID string
		value       string
	}{{firstRepository, "workspace-a", "private-a"}, {secondRepository, "workspace-b", "private-b"}} {
		if err := seed.repository.InsertRecord(t.Context(), seed.workspaceID, object, recordmodel.Record{ID: "shared-id", CreatedAt: "v1", UpdatedAt: "v1", Data: map[string]any{"value_text": seed.value}}); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	errors := make(chan error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		errors <- firstRepository.UpdateRecord(t.Context(), "workspace-a", object, recordmodel.Record{ID: "shared-id", CreatedAt: "v1", UpdatedAt: "v2", Data: map[string]any{"value_text": "updated-a"}})
	}()
	go func() {
		defer wait.Done()
		<-start
		value, found, err := secondRepository.GetRecord(t.Context(), "workspace-b", object, "shared-id")
		if err == nil && (!found || value.Data["value_text"] != "private-b") {
			err = fmt.Errorf("workspace-b read returned %+v found=%v", value, found)
		}
		errors <- err
	}()
	close(start)
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent workspace processing: %v", err)
		}
	}
	for _, assertion := range []struct {
		store       *database.RuntimeStore
		workspaceID string
		value       string
	}{{second, "workspace-a", "updated-a"}, {first, "workspace-b", "private-b"}} {
		value, found, err := NewRecordStore(assertion.store).GetRecord(t.Context(), assertion.workspaceID, object, "shared-id")
		if err != nil || !found || value.Data["value_text"] != assertion.value {
			t.Fatalf("cross-instance workspace read for %s: value=%+v found=%v err=%v", assertion.workspaceID, value, found, err)
		}
	}
}
