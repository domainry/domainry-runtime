package workflow

import (
	"context"
	"errors"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"testing"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowDefinitionStoreLifecycleAndCancellation(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewWorkflowDefinitionStore(store)
	definition := workflowmodel.WorkflowDefinition{ID: "definition-1", Key: "expense", Name: "Expense", Enabled: true, CurrentDraftVersionID: "version-1", CreatedAt: "v1", UpdatedAt: "v1"}
	draft := workflowmodel.WorkflowDefinitionVersion{ID: "version-1", DefinitionID: definition.ID, Version: 1, Status: workflowmodel.WorkflowVersionDraft, Revision: 1, Workflow: definitionmodel.WorkflowSchema{Key: definition.Key, Name: definition.Name, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2}}, ValidationReport: workflowmodel.WorkflowValidation{Valid: true}, CreatedBy: "admin", CreatedAt: "v1", UpdatedAt: "v1"}
	if err := repository.InsertDefinition(t.Context(), definition, draft); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if value, found, err := repository.GetDefinitionByKey(t.Context(), definition.Key); err != nil || !found || value.ID != definition.ID {
		t.Fatalf("get definition=%#v found=%v err=%v", value, found, err)
	}
	if values, err := repository.ListDefinitions(t.Context()); err != nil || len(values) != 1 {
		t.Fatalf("list definitions=%#v err=%v", values, err)
	}
	draft.Workflow.Name, draft.UpdatedAt = "Expense updated", "v2"
	if updated, err := repository.UpdateDraft(t.Context(), draft, 1); err != nil || !updated {
		t.Fatalf("update draft=%v err=%v", updated, err)
	}
	draft.Revision, draft.ContentHash, draft.PublishedBy, draft.PublishedAt = 2, "hash-1", "admin", "v3"
	definition.UpdatedAt = "v3"
	if published, err := repository.PublishDraft(t.Context(), definition, draft, "publish-1"); err != nil || !published {
		t.Fatalf("publish=%v err=%v", published, err)
	}
	if versions, err := repository.ListVersions(t.Context(), definition.ID); err != nil || len(versions) != 1 || versions[0].Status != workflowmodel.WorkflowVersionPublished {
		t.Fatalf("versions=%#v err=%v", versions, err)
	}
	if version, found, err := repository.GetVersion(t.Context(), draft.ID); err != nil || !found || version.PublishIdempotencyKey != "publish-1" {
		t.Fatalf("get version=%#v found=%v err=%v", version, found, err)
	}
	if updated, err := repository.SetDefinitionEnabled(t.Context(), definition.ID, false, "v4"); err != nil || !updated {
		t.Fatalf("disable=%v err=%v", updated, err)
	}
	second := draft
	second.ID, second.Version, second.Status, second.Revision, second.PublishedAt, second.PublishedBy = "version-2", 2, workflowmodel.WorkflowVersionDraft, 1, "", ""
	second.CreatedAt, second.UpdatedAt = "v5", "v5"
	if created, err := repository.InsertDraftVersion(t.Context(), definition.ID, second); err != nil || !created {
		t.Fatalf("insert second draft=%v err=%v", created, err)
	}
	if deleted, err := repository.DeleteDraft(t.Context(), definition.ID, second.ID); err != nil || !deleted {
		t.Fatalf("delete draft=%v err=%v", deleted, err)
	}
	if archived, err := repository.ArchiveVersion(t.Context(), definition.ID, draft.ID, "v6"); err != nil || archived {
		t.Fatalf("current published version must not archive: archived=%v err=%v", archived, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.ListDefinitions(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled list error=%v", err)
	}
	if err := repository.InsertDefinition(cancelled, workflowmodel.WorkflowDefinition{}, workflowmodel.WorkflowDefinitionVersion{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled insert error=%v", err)
	}
}
