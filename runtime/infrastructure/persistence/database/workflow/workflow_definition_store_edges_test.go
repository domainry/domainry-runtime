package workflow

import (
	"context"
	"errors"
	"testing"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func workflowDefinitionEdgeFixture(t *testing.T) (*database.RuntimeStore, WorkflowDefinitionStore, workflowmodel.WorkflowDefinition, workflowmodel.WorkflowDefinitionVersion) {
	t.Helper()
	store := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewWorkflowDefinitionStore(store)
	definition := workflowmodel.WorkflowDefinition{ID: "definition-edge", Key: "edge", Name: "Edge", Enabled: true, CurrentDraftVersionID: "version-edge", CreatedAt: "v1", UpdatedAt: "v1"}
	draft := workflowmodel.WorkflowDefinitionVersion{
		ID: "version-edge", DefinitionID: definition.ID, Version: 1, Status: workflowmodel.WorkflowVersionDraft, Revision: 1,
		Workflow: workflowDefinitionTestSchema(), ValidationReport: workflowmodel.WorkflowValidation{Valid: true}, CreatedBy: "admin", CreatedAt: "v1", UpdatedAt: "v1",
	}
	if err := repository.InsertDefinition(t.Context(), definition, draft); err != nil {
		t.Fatal(err)
	}
	return store, repository, definition, draft
}

func TestWorkflowDefinitionMissingRowsCorruptScansAndWriteFailures(t *testing.T) {
	store, repository, definition, draft := workflowDefinitionEdgeFixture(t)
	if _, found, err := repository.GetVersion(t.Context(), "missing"); err != nil || found {
		t.Fatalf("missing version found=%v error=%v", found, err)
	}
	if created, err := repository.InsertDraftVersion(t.Context(), "missing-definition", workflowmodel.WorkflowDefinitionVersion{ID: "missing-draft"}); err != nil || created {
		t.Fatalf("missing definition draft created=%v error=%v", created, err)
	}
	if enabled, err := repository.SetDefinitionEnabled(t.Context(), "missing", false, "v2"); err != nil || enabled {
		t.Fatalf("missing definition enabled=%v error=%v", enabled, err)
	}
	if archived, err := repository.ArchiveVersion(t.Context(), definition.ID, "missing", "v2"); err != nil || archived {
		t.Fatalf("missing version archived=%v error=%v", archived, err)
	}
	if err := repository.InsertDefinition(t.Context(), definition, draft); err == nil {
		t.Fatal("duplicate definition inserted")
	}
	duplicateVersionDefinition := definition
	duplicateVersionDefinition.ID, duplicateVersionDefinition.Key = "definition-duplicate-version", "duplicate-version"
	if err := repository.InsertDefinition(t.Context(), duplicateVersionDefinition, draft); err == nil {
		t.Fatal("duplicate version inserted")
	}

	if _, err := store.DB().ExecContext(t.Context(), `UPDATE workflow_definition_identities SET enabled = 'invalid' WHERE id = ?`, definition.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.GetDefinitionByKey(t.Context(), definition.Key); err == nil {
		t.Fatal("corrupt definition scanned")
	}
	if _, err := repository.ListDefinitions(t.Context()); err == nil {
		t.Fatal("corrupt definition listed")
	}
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE workflow_definition_versions SET version_no = 'invalid' WHERE id = ?`, draft.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.GetVersion(t.Context(), draft.ID); err == nil {
		t.Fatal("corrupt version scanned")
	}
	if _, err := repository.ListVersions(t.Context(), definition.ID); err == nil {
		t.Fatal("corrupt version listed")
	}
}

func TestWorkflowDefinitionCancelledOperationsPropagate(t *testing.T) {
	_, repository, definition, draft := workflowDefinitionEdgeFixture(t)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	checks := map[string]func() error{
		"get version":   func() error { _, _, err := repository.GetVersion(cancelled, draft.ID); return err },
		"list versions": func() error { _, err := repository.ListVersions(cancelled, definition.ID); return err },
		"update draft":  func() error { _, err := repository.UpdateDraft(cancelled, draft, 1); return err },
		"insert draft": func() error {
			_, err := repository.InsertDraftVersion(cancelled, definition.ID, workflowmodel.WorkflowDefinitionVersion{ID: "new"})
			return err
		},
		"delete draft":  func() error { _, err := repository.DeleteDraft(cancelled, definition.ID, draft.ID); return err },
		"publish draft": func() error { _, err := repository.PublishDraft(cancelled, definition, draft, "key"); return err },
		"archive version": func() error {
			_, err := repository.ArchiveVersion(cancelled, definition.ID, draft.ID, "v2")
			return err
		},
		"set enabled": func() error {
			_, err := repository.SetDefinitionEnabled(cancelled, definition.ID, false, "v2")
			return err
		},
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(); !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestWorkflowDefinitionDraftAndPublishSecondWriteFailuresRollback(t *testing.T) {
	t.Run("insert draft version", func(t *testing.T) {
		_, repository, definition, draft := workflowDefinitionEdgeFixture(t)
		draft.ID = "duplicate-draft"
		definition.ID, definition.Key, definition.CurrentDraftVersionID = "definition-two", "two", ""
		seed := draft
		seed.DefinitionID = definition.ID
		seed.ID = "seed-draft"
		if err := repository.InsertDefinition(t.Context(), definition, seed); err != nil {
			t.Fatal(err)
		}
		if deleted, err := repository.DeleteDraft(t.Context(), definition.ID, seed.ID); err != nil || !deleted {
			t.Fatalf("seed draft delete=%v error=%v", deleted, err)
		}
		draft.DefinitionID = definition.ID
		draft.ID = "version-edge"
		if created, err := repository.InsertDraftVersion(t.Context(), definition.ID, draft); err == nil || created {
			t.Fatalf("duplicate version created=%v error=%v", created, err)
		}
	})
	t.Run("delete draft identity update", func(t *testing.T) {
		store, repository, definition, draft := workflowDefinitionEdgeFixture(t)
		if _, err := store.DB().ExecContext(t.Context(), `DROP TABLE workflow_definition_identities`); err != nil {
			t.Fatal(err)
		}
		if deleted, err := repository.DeleteDraft(t.Context(), definition.ID, draft.ID); err == nil || deleted {
			t.Fatalf("deleted=%v error=%v", deleted, err)
		}
	})
	t.Run("publish identity update", func(t *testing.T) {
		store, repository, definition, draft := workflowDefinitionEdgeFixture(t)
		draft.ContentHash, draft.PublishedBy, draft.PublishedAt = "hash", "admin", "v2"
		if _, err := store.DB().ExecContext(t.Context(), `DROP TABLE workflow_definition_identities`); err != nil {
			t.Fatal(err)
		}
		if published, err := repository.PublishDraft(t.Context(), definition, draft, "publish"); err == nil || published {
			t.Fatalf("published=%v error=%v", published, err)
		}
	})
	t.Run("stale publish", func(t *testing.T) {
		_, repository, definition, draft := workflowDefinitionEdgeFixture(t)
		draft.Revision = 99
		if published, err := repository.PublishDraft(t.Context(), definition, draft, "publish"); err != nil || published {
			t.Fatalf("published=%v error=%v", published, err)
		}
	})
}
