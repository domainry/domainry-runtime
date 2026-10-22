package record

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	ormschema "github.com/domainry/domainry-orm/schema"
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
	createTestRecordTable(t, store, "workspace_record", ormschema.Column("email", ormschema.Text()))
	if _, err := store.DB().Exec(`CREATE UNIQUE INDEX workspace_record_email_unique ON workspace_record(workspace_id, email)`); err != nil {
		t.Fatal(err)
	}

	if err := store.EnsureEvidenceSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewRecordStore(store)
	object := definitionmodel.ObjectSchema{Key: "workspace_record", Fields: []definitionmodel.FieldSchema{{Key: "email", Type: "text", Unique: true}}}
	workspaceA, workspaceB := "workspace-a", "workspace-b"
	recordA := recordmodel.Record{RecordID: "shared-id", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z", Data: map[string]any{"email": "shared@example.com"}}
	recordB := recordmodel.Record{RecordID: "record-b", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z", Data: map[string]any{"email": "shared@example.com"}}
	if err := repository.InsertRecord(t.Context(), workspaceA, object, recordA); err != nil {
		t.Fatal(err)
	}
	if err := repository.InsertRecord(t.Context(), workspaceB, object, recordB); err != nil {
		t.Fatalf("workspace-scoped unique value must be reusable in another workspace: %v", err)
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
	recordA.UpdatedAt = "2026-01-02T00:00:00Z"
	if err := repository.CommitRecordMutation(t.Context(), workspaceA, transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: recordA, ExpectedUpdatedAt: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	unchangedB, found, err := repository.GetRecord(t.Context(), workspaceB, object, recordB.RecordID)
	if err != nil || !found || unchangedB.Data["email"] != "shared@example.com" {
		t.Fatalf("workspace A update affected workspace B: record=%+v found=%v err=%v", unchangedB, found, err)
	}

	if err := repository.DeleteRecord(t.Context(), workspaceA, object, recordA.RecordID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := repository.GetRecord(t.Context(), workspaceA, object, recordA.RecordID); err != nil || found {
		t.Fatalf("workspace A delete failed: found=%v err=%v", found, err)
	}
	if _, found, err := repository.GetRecord(t.Context(), workspaceB, object, recordB.RecordID); err != nil || !found {
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
	if err := first.EnsureApplicationSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	createTestRecordTable(t, first, "two_runtime_workspace_record", ormschema.Column("value_text", ormschema.Text()))
	object := definitionmodel.ObjectSchema{Key: "two_runtime_workspace_record", Fields: []definitionmodel.FieldSchema{{Key: "value_text", Type: "text"}}}
	firstRepository, secondRepository := NewRecordStore(first), NewRecordStore(second)
	for _, seed := range []struct {
		repository  RecordStore
		workspaceID string
		id          string
		value       string
	}{{firstRepository, "workspace-a", "record-a", "private-a"}, {secondRepository, "workspace-b", "record-b", "private-b"}} {
		if err := seed.repository.InsertRecord(t.Context(), seed.workspaceID, object, recordmodel.Record{RecordID: seed.id, CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z", Data: map[string]any{"value_text": seed.value}}); err != nil {
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
		errors <- firstRepository.UpdateRecord(t.Context(), "workspace-a", object, recordmodel.Record{RecordID: "record-a", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z", Data: map[string]any{"value_text": "updated-a"}})
	}()
	go func() {
		defer wait.Done()
		<-start
		value, found, err := secondRepository.GetRecord(t.Context(), "workspace-b", object, "record-b")
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
		id          string
		value       string
	}{{second, "workspace-a", "record-a", "updated-a"}, {first, "workspace-b", "record-b", "private-b"}} {
		value, found, err := NewRecordStore(assertion.store).GetRecord(t.Context(), assertion.workspaceID, object, assertion.id)
		if err != nil || !found || value.Data["value_text"] != assertion.value {
			t.Fatalf("cross-instance workspace read for %s: value=%+v found=%v err=%v", assertion.workspaceID, value, found, err)
		}
	}
}
