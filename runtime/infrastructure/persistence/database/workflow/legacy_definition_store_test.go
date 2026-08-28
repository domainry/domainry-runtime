package workflow

import (
	"context"
	"errors"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"testing"
	"time"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowDefinitionCompatibilityAPIsRejectCancelledContext(t *testing.T) {
	base := openStoreForGeneratedListTest(t)
	defer base.Close()
	if err := base.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := NewWorkflowDefinitionStore(base)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := store.GetDefinitionByKey(ctx, "never"); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetWorkflowDefinitionByKey error = %v", err)
	}
	if _, err := store.ListDefinitions(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListWorkflowDefinitions error = %v", err)
	}
}

func TestWorkflowDefinitionDraftLifecycle(t *testing.T) {
	base := openStoreForGeneratedListTest(t)
	defer base.Close()
	if err := base.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := NewWorkflowDefinitionStore(base)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	definition := workflowmodel.WorkflowDefinition{ID: "wf_leave", Key: "leave_approval", Name: "Leave approval", Enabled: true, CurrentDraftVersionID: "wfv_leave_1", CreatedAt: now, UpdatedAt: now}
	draft := workflowmodel.WorkflowDefinitionVersion{ID: "wfv_leave_1", DefinitionID: definition.ID, Version: 1, Status: workflowmodel.WorkflowVersionDraft, Revision: 1, Workflow: workflowDefinitionTestSchema(), ValidationReport: workflowmodel.WorkflowValidation{Issues: []workflowmodel.WorkflowValidationIssue{}}, CreatedBy: "admin", CreatedAt: now, UpdatedAt: now}
	if err := store.InsertDefinition(t.Context(), definition, draft); err != nil {
		t.Fatal(err)
	}
	stored, found, err := store.GetDefinitionByKey(t.Context(), definition.Key)
	if err != nil || !found || stored.CurrentDraftVersionID != draft.ID {
		t.Fatalf("unexpected definition: %#v, found=%v, err=%v", stored, found, err)
	}
	draft.Workflow.Name = "Updated leave approval"
	draft.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	updated, err := store.UpdateDraft(t.Context(), draft, 1)
	if err != nil || !updated {
		t.Fatalf("expected draft update: updated=%v err=%v", updated, err)
	}
	updated, err = store.UpdateDraft(t.Context(), draft, 1)
	if err != nil || updated {
		t.Fatalf("expected optimistic lock conflict: updated=%v err=%v", updated, err)
	}
	draft.ContentHash = "hash"
	draft.Revision = 2
	draft.ValidationReport.Valid = true
	draft.PublishedBy = "publisher"
	draft.PublishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	draft.UpdatedAt = draft.PublishedAt
	definition.UpdatedAt = draft.PublishedAt
	published, err := store.PublishDraft(t.Context(), definition, draft, "publish-1")
	if err != nil || !published {
		t.Fatalf("expected publish: published=%v err=%v", published, err)
	}
	publishedVersion, found, err := store.GetVersion(t.Context(), draft.ID)
	if err != nil || !found || publishedVersion.Status != workflowmodel.WorkflowVersionPublished || publishedVersion.ContentHash != "hash" {
		t.Fatalf("unexpected published version: %#v found=%v err=%v", publishedVersion, found, err)
	}
	deleted, err := store.DeleteDraft(t.Context(), definition.ID, draft.ID)
	if err != nil || deleted {
		t.Fatalf("published version must not be deleted: deleted=%v err=%v", deleted, err)
	}
}

func workflowDefinitionTestSchema() definitionmodel.WorkflowSchema {
	return definitionmodel.WorkflowSchema{Key: "leave_approval", Name: "Leave approval", Enabled: true, Trigger: map[string]any{"type": "manual"}, Condition: map[string]any{}, Action: map[string]any{"type": "workflow_graph"}, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger", Name: "Start"}}, Edges: []definitionmodel.WorkflowGraphEdge{}}}
}
