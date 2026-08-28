package workflow

import (
	"context"
	"errors"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func workflowSynchronizationSchema() definitionmodel.WorkflowSchema {
	return definitionmodel.WorkflowSchema{
		Key: "sync.flow", Name: "Sync flow", Enabled: true,
		TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"},
		Graph:           &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}}},
	}
}

func workflowSynchronizationService(store *workflowDefinitionProjectionStore) *WorkflowApplicationService {
	return NewWorkflowApplicationService(WorkflowDependencies{
		Definitions: store, WorkflowRegistry: &workflowRegistryStub{items: map[string]definitionmodel.WorkflowSchema{}}, Schema: validationSchemaStub{},
		ObjectMap: func(context.Context) map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{}
		},
	})
}

func TestSynchronizePublishedWorkflowDefinitionFailureBoundaries(t *testing.T) {
	workflow := workflowSynchronizationSchema()
	definition := workflowmodel.WorkflowDefinition{ID: "definition", Key: workflow.Key, Enabled: true}
	publishedDefinition := definition
	publishedDefinition.CurrentPublishedVersionID = "published"
	for name, store := range map[string]*workflowDefinitionProjectionStore{
		"published load":    {versionErr: errors.New("version")},
		"published missing": {versions: map[string]workflowmodel.WorkflowDefinitionVersion{}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := workflowSynchronizationService(store).synchronizePublishedWorkflowDefinition(t.Context(), publishedDefinition, workflow); err == nil {
				t.Fatal("expected published version error")
			}
		})
	}
	draftDefinition := definition
	draftDefinition.CurrentDraftVersionID = "draft"
	if err := workflowSynchronizationService(&workflowDefinitionProjectionStore{}).synchronizePublishedWorkflowDefinition(t.Context(), draftDefinition, workflow); err == nil {
		t.Fatal("existing draft accepted")
	}
	if err := workflowSynchronizationService(&workflowDefinitionProjectionStore{versionErr: errors.New("draft read")}).synchronizePublishedWorkflowDefinition(t.Context(), draftDefinition, workflow); err == nil {
		t.Fatal("draft read error ignored")
	}
	for name, draft := range map[string]workflowmodel.WorkflowDefinitionVersion{
		"user draft":         {ID: "draft", DefinitionID: definition.ID, CreatedBy: "user", Workflow: workflow},
		"stale system draft": {ID: "draft", DefinitionID: definition.ID, CreatedBy: "system", Workflow: definitionmodel.WorkflowSchema{Key: workflow.Key}},
	} {
		t.Run(name, func(t *testing.T) {
			store := &workflowDefinitionProjectionStore{versions: map[string]workflowmodel.WorkflowDefinitionVersion{"draft": draft}}
			if err := workflowSynchronizationService(store).synchronizePublishedWorkflowDefinition(t.Context(), draftDefinition, workflow); err == nil {
				t.Fatal("blocking draft accepted")
			}
		})
	}
	invalid := workflow
	invalid.Graph = &definitionmodel.WorkflowGraphSchema{Version: 1}
	if err := workflowSynchronizationService(&workflowDefinitionProjectionStore{}).synchronizePublishedWorkflowDefinition(t.Context(), definition, invalid); err == nil {
		t.Fatal("invalid workflow accepted")
	}
	for name, store := range map[string]*workflowDefinitionProjectionStore{
		"list versions": {listVersionsErr: errors.New("versions")},
		"insert error":  {versions: map[string]workflowmodel.WorkflowDefinitionVersion{}, insertDraftErr: errors.New("insert")},
		"insert false":  {versions: map[string]workflowmodel.WorkflowDefinitionVersion{}, insertDraftOK: boolPointer(false)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := workflowSynchronizationService(store).synchronizePublishedWorkflowDefinition(t.Context(), definition, workflow); err == nil {
				t.Fatal("expected draft creation error")
			}
		})
	}
	for name, store := range map[string]*workflowDefinitionProjectionStore{
		"publish error": {
			definitions: []workflowmodel.WorkflowDefinition{definition}, versions: map[string]workflowmodel.WorkflowDefinitionVersion{
				"zero": {ID: "zero", DefinitionID: definition.ID, Version: 0}, "five": {ID: "five", DefinitionID: definition.ID, Version: 5},
			}, publishErr: errors.New("publish"),
		},
		"publish false": {definitions: []workflowmodel.WorkflowDefinition{definition}, versions: map[string]workflowmodel.WorkflowDefinitionVersion{}, publishOK: boolPointer(false)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := workflowSynchronizationService(store).synchronizePublishedWorkflowDefinition(t.Context(), definition, workflow); err == nil {
				t.Fatal("expected publication error")
			}
		})
	}
}

func TestInitializePublishedWorkflowDefinitionsSkipsInactiveUnpublishedProjection(t *testing.T) {
	store := &workflowDefinitionProjectionStore{definitions: []workflowmodel.WorkflowDefinition{{ID: "inactive", Key: "removed", Enabled: true}}}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "inactive unpublished workflow edge")
	if err := workflowSynchronizationService(store).InitializePublishedWorkflowDefinitions(t.Context(), nil, scope); err != nil {
		t.Fatal(err)
	}
}

func TestInitializePublishedWorkflowDefinitionsFinalPublishedReadBoundaries(t *testing.T) {
	workflow := workflowSynchronizationSchema()
	definition := workflowmodel.WorkflowDefinition{ID: "definition", Key: workflow.Key, Enabled: true, CurrentPublishedVersionID: "published"}
	version := workflowmodel.WorkflowDefinitionVersion{ID: "published", DefinitionID: definition.ID, Version: 1, ContentHash: WorkflowDefinitionHash(workflow), Workflow: workflow}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "final published workflow read edges")
	for _, store := range []*workflowDefinitionProjectionStore{
		{definitions: []workflowmodel.WorkflowDefinition{definition}, versions: map[string]workflowmodel.WorkflowDefinitionVersion{"published": version}, listVersionsErr: errors.New("bulk read")},
		{definitions: []workflowmodel.WorkflowDefinition{definition}, versions: map[string]workflowmodel.WorkflowDefinitionVersion{"published": version}, bulkVersionsEmpty: true},
	} {
		if err := workflowSynchronizationService(store).InitializePublishedWorkflowDefinitions(t.Context(), []definitionmodel.WorkflowSchema{workflow}, scope); err == nil {
			t.Fatal("final published read failure ignored")
		}
	}
}

func TestSynchronizePublishedWorkflowDefinitionResumesMatchingSystemDraftWithVersionedPublishKey(t *testing.T) {
	workflow := workflowSynchronizationSchema()
	old := workflow
	old.Name = "Old sync flow"
	definition := workflowmodel.WorkflowDefinition{ID: "definition", Key: workflow.Key, Enabled: true, CurrentPublishedVersionID: "published", CurrentDraftVersionID: "draft"}
	store := &workflowDefinitionProjectionStore{
		definitions: []workflowmodel.WorkflowDefinition{definition},
		versions: map[string]workflowmodel.WorkflowDefinitionVersion{
			"published": {ID: "published", DefinitionID: definition.ID, Version: 1, Status: workflowmodel.WorkflowVersionPublished, ContentHash: WorkflowDefinitionHash(old), Workflow: old, CreatedBy: "system"},
			"draft":     {ID: "draft", DefinitionID: definition.ID, Version: 2, Status: workflowmodel.WorkflowVersionDraft, Revision: 1, Workflow: workflow, CreatedBy: "system"},
		},
	}
	if err := workflowSynchronizationService(store).synchronizePublishedWorkflowDefinition(t.Context(), definition, workflow); err != nil {
		t.Fatal(err)
	}
	if got := store.definitions[0].CurrentPublishedVersionID; got != "draft" {
		t.Fatalf("current published version = %q", got)
	}
	wantKey := "metadata:" + WorkflowDefinitionHash(workflow) + ":version:2"
	if len(store.publishKeys) != 1 || store.publishKeys[0] != wantKey {
		t.Fatalf("publish keys = %#v, want %q", store.publishKeys, wantKey)
	}
}
