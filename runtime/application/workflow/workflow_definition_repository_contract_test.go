package workflow

import (
	"context"
	"errors"
	"reflect"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestInitializePublishedWorkflowDefinitionsAllowsManifestOnlyRuntime(t *testing.T) {
	service := NewWorkflowApplicationService(WorkflowDependencies{})
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test manifest workflow initialization")
	if err := service.InitializePublishedWorkflowDefinitions(t.Context(), nil, scope); err != nil {
		t.Fatalf("manifest-only Workflow initialization error=%v", err)
	}
}

func TestWorkflowDefinitionRepositoryMethodBudget(t *testing.T) {
	contract := reflect.TypeOf((*workflowcontract.WorkflowDefinitionStore)(nil)).Elem()
	if contract.NumMethod() != 11 || contract.NumMethod() > 15 {
		t.Fatalf("workflow definition repository has %d methods", contract.NumMethod())
	}
}

type workflowDefinitionProjectionStore struct {
	definitions       []workflowmodel.WorkflowDefinition
	versions          map[string]workflowmodel.WorkflowDefinitionVersion
	getErr            error
	insertErr         error
	publishErr        error
	publishOK         *bool
	listErr           error
	setErr            error
	setOK             *bool
	versionErr        error
	versionCalls      int
	versionFailAt     int
	versionMissingAt  int
	listVersionsErr   error
	bulkVersionsEmpty bool
	insertDraftErr    error
	insertDraftOK     *bool
	publishKeys       []string
}

func (s *workflowDefinitionProjectionStore) InsertDefinition(_ context.Context, definition workflowmodel.WorkflowDefinition, version workflowmodel.WorkflowDefinitionVersion) error {
	if s.insertErr != nil {
		return s.insertErr
	}
	s.definitions = append(s.definitions, definition)
	if s.versions == nil {
		s.versions = map[string]workflowmodel.WorkflowDefinitionVersion{}
	}
	s.versions[version.ID] = version
	return nil
}
func (s *workflowDefinitionProjectionStore) GetDefinitionByKey(_ context.Context, key string) (workflowmodel.WorkflowDefinition, bool, error) {
	if s.getErr != nil {
		return workflowmodel.WorkflowDefinition{}, false, s.getErr
	}
	for _, definition := range s.definitions {
		if definition.Key == key {
			return definition, true, nil
		}
	}
	return workflowmodel.WorkflowDefinition{}, false, nil
}
func (s *workflowDefinitionProjectionStore) ListDefinitions(context.Context) ([]workflowmodel.WorkflowDefinition, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]workflowmodel.WorkflowDefinition(nil), s.definitions...), nil
}
func (s *workflowDefinitionProjectionStore) GetVersion(_ context.Context, id string) (workflowmodel.WorkflowDefinitionVersion, bool, error) {
	s.versionCalls++
	if s.versionErr != nil && (s.versionFailAt == 0 || s.versionFailAt == s.versionCalls) {
		return workflowmodel.WorkflowDefinitionVersion{}, false, s.versionErr
	}
	if s.versionMissingAt == s.versionCalls {
		return workflowmodel.WorkflowDefinitionVersion{}, false, nil
	}
	version, found := s.versions[id]
	return version, found, nil
}
func (s *workflowDefinitionProjectionStore) ListVersions(_ context.Context, definitionID string) ([]workflowmodel.WorkflowDefinitionVersion, error) {
	if s.listVersionsErr != nil {
		return nil, s.listVersionsErr
	}
	if definitionID == "" && s.bulkVersionsEmpty {
		return nil, nil
	}
	out := []workflowmodel.WorkflowDefinitionVersion{}
	for _, version := range s.versions {
		if definitionID == "" || version.DefinitionID == definitionID {
			out = append(out, version)
		}
	}
	return out, nil
}
func (*workflowDefinitionProjectionStore) UpdateDraft(context.Context, workflowmodel.WorkflowDefinitionVersion, int) (bool, error) {
	return false, nil
}
func (s *workflowDefinitionProjectionStore) InsertDraftVersion(_ context.Context, definitionID string, version workflowmodel.WorkflowDefinitionVersion) (bool, error) {
	if s.insertDraftErr != nil {
		return false, s.insertDraftErr
	}
	if s.insertDraftOK != nil && !*s.insertDraftOK {
		return false, nil
	}
	for index := range s.definitions {
		if s.definitions[index].ID != definitionID || s.definitions[index].CurrentDraftVersionID != "" {
			continue
		}
		s.definitions[index].CurrentDraftVersionID = version.ID
		if s.versions == nil {
			s.versions = map[string]workflowmodel.WorkflowDefinitionVersion{}
		}
		s.versions[version.ID] = version
		return true, nil
	}
	return false, nil
}
func (*workflowDefinitionProjectionStore) DeleteDraft(context.Context, string, string) (bool, error) {
	return false, nil
}
func (s *workflowDefinitionProjectionStore) PublishDraft(_ context.Context, definition workflowmodel.WorkflowDefinition, version workflowmodel.WorkflowDefinitionVersion, idempotencyKey string) (bool, error) {
	if s.publishErr != nil {
		return false, s.publishErr
	}
	if s.publishOK != nil && !*s.publishOK {
		return false, nil
	}
	for index := range s.definitions {
		if s.definitions[index].ID == definition.ID {
			s.definitions[index].CurrentDraftVersionID = ""
			s.definitions[index].CurrentPublishedVersionID = version.ID
		}
	}
	version.Status = workflowmodel.WorkflowVersionPublished
	s.versions[version.ID] = version
	s.publishKeys = append(s.publishKeys, idempotencyKey)
	return true, nil
}
func (*workflowDefinitionProjectionStore) ArchiveVersion(context.Context, string, string, string) (bool, error) {
	return false, nil
}
func (s *workflowDefinitionProjectionStore) SetDefinitionEnabled(_ context.Context, id string, enabled bool, updatedAt string) (bool, error) {
	if s.setErr != nil {
		return false, s.setErr
	}
	if s.setOK != nil && !*s.setOK {
		return false, nil
	}
	for index := range s.definitions {
		if s.definitions[index].ID != id {
			continue
		}
		s.definitions[index].Enabled = enabled
		s.definitions[index].UpdatedAt = updatedAt
		return true, nil
	}
	return false, nil
}

func boolPointer(value bool) *bool {
	return &value
}

func TestInitializePublishedWorkflowDefinitionsSynchronizesActiveMetadataSet(t *testing.T) {
	const workflowKey = "order.approval"
	store := &workflowDefinitionProjectionStore{
		definitions: []workflowmodel.WorkflowDefinition{{
			ID: "definition-1", Key: workflowKey, Enabled: true,
			CurrentPublishedVersionID: "version-1",
		}},
		versions: map[string]workflowmodel.WorkflowDefinitionVersion{
			"version-1": {
				ID: "version-1", Status: workflowmodel.WorkflowVersionPublished,
				Workflow: definitionmodel.WorkflowSchema{Key: workflowKey, Enabled: true},
			},
		},
	}
	registry := &workflowRegistryStub{items: map[string]definitionmodel.WorkflowSchema{
		workflowKey: {Key: workflowKey, Enabled: true},
	}}
	service := NewWorkflowApplicationService(WorkflowDependencies{
		Definitions: store, WorkflowRegistry: registry,
	})
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "synchronize active workflow metadata")

	if err := service.InitializePublishedWorkflowDefinitions(t.Context(), nil, scope); err != nil {
		t.Fatal(err)
	}
	if store.definitions[0].Enabled || registry.Count() != 0 {
		t.Fatalf("removed metadata workflow remained enabled: definition=%#v registry=%#v", store.definitions[0], registry.List())
	}

	active := []definitionmodel.WorkflowSchema{{Key: workflowKey, Enabled: true}}
	if err := service.InitializePublishedWorkflowDefinitions(t.Context(), active, scope); err != nil {
		t.Fatal(err)
	}
	if !store.definitions[0].Enabled || registry.Count() != 1 {
		t.Fatalf("restored metadata workflow was not re-enabled: definition=%#v registry=%#v", store.definitions[0], registry.List())
	}
}

func TestInitializePublishedWorkflowDefinitionsPublishesChangedMetadata(t *testing.T) {
	const workflowKey = "refund.approval"
	oldWorkflow := definitionmodel.WorkflowSchema{
		Key: workflowKey, Name: "Refund approval", Enabled: true,
		TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"},
		Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{
			{ID: "start", Type: "trigger"},
			{ID: "execute", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{
				ActionKey: "refund.execute", RecordID: "$record.id", OnError: "fail",
			}}},
		}, Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start-execute", Source: "start", Target: "execute"}}},
	}
	updatedWorkflow := cloneWorkflowSchema(oldWorkflow)
	updatedWorkflow.Graph.Nodes[1].Contract.Action.Input = map[string]any{
		"decision_reason": "Approved through refund approval workflow",
	}
	store := &workflowDefinitionProjectionStore{
		definitions: []workflowmodel.WorkflowDefinition{{
			ID: "definition-1", Key: workflowKey, Enabled: true, CurrentPublishedVersionID: "version-1",
		}},
		versions: map[string]workflowmodel.WorkflowDefinitionVersion{
			"version-1": {
				ID: "version-1", DefinitionID: "definition-1", Version: 1,
				Status: workflowmodel.WorkflowVersionPublished, ContentHash: WorkflowDefinitionHash(oldWorkflow),
				Workflow: oldWorkflow,
			},
		},
	}
	registry := &workflowRegistryStub{items: map[string]definitionmodel.WorkflowSchema{}}
	service := NewWorkflowApplicationService(WorkflowDependencies{
		Definitions: store, WorkflowRegistry: registry,
		Schema: workflowReferenceSchemaEdgeStub{snapshot: WorkflowSchemaSnapshot{
			Actions: []definitionmodel.ActionSchema{{Key: "refund.execute", ObjectKey: "refund"}},
		}},
		ObjectMap: func(context.Context) map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"refund": {Key: "refund"}}
		},
	})
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "synchronize changed workflow metadata")

	if err := service.InitializePublishedWorkflowDefinitions(t.Context(), []definitionmodel.WorkflowSchema{updatedWorkflow}, scope); err != nil {
		t.Fatal(err)
	}
	if len(store.versions) != 2 {
		t.Fatalf("changed workflow did not create a new published version: %#v", store.versions)
	}
	current := store.definitions[0].CurrentPublishedVersionID
	published := store.versions[current]
	if published.Version != 2 || published.Status != workflowmodel.WorkflowVersionPublished {
		t.Fatalf("unexpected synchronized version: %#v", published)
	}
	got := published.Workflow.Graph.Nodes[1].Contract.Action.Input["decision_reason"]
	if got != "Approved through refund approval workflow" {
		t.Fatalf("workflow Action input was not preserved: %#v", published.Workflow.Graph.Nodes[1].Contract.Action.Input)
	}
	registered, found := registry.Get(workflowKey)
	if !found || registered.Graph.Nodes[1].Contract.Action.Input["decision_reason"] != got {
		t.Fatalf("registry did not receive synchronized Workflow: %#v found=%t", registered, found)
	}
}

func TestInitializePublishedWorkflowDefinitionsRepositoryFailureEdges(t *testing.T) {
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "exercise workflow projection failure edges")
	valid := definitionmodel.WorkflowSchema{
		Key: "order.approval", Name: "Order approval", Enabled: true,
		TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"},
		Graph: &definitionmodel.WorkflowGraphSchema{
			Version: 2,
			Nodes:   []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}},
		},
	}
	publishedDefinition := workflowmodel.WorkflowDefinition{
		ID: "definition-1", Key: valid.Key, Enabled: true, CurrentPublishedVersionID: "version-1",
	}
	publishedVersion := workflowmodel.WorkflowDefinitionVersion{
		ID: "version-1", Workflow: valid, Status: workflowmodel.WorkflowVersionPublished,
	}
	run := func(t *testing.T, store *workflowDefinitionProjectionStore, workflows []definitionmodel.WorkflowSchema) error {
		t.Helper()
		service := NewWorkflowApplicationService(WorkflowDependencies{
			Definitions:      store,
			WorkflowRegistry: &workflowRegistryStub{items: map[string]definitionmodel.WorkflowSchema{}},
			Schema:           validationSchemaStub{},
			ObjectMap: func(context.Context) map[string]definitionmodel.ObjectSchema {
				return map[string]definitionmodel.ObjectSchema{}
			},
		})
		return service.InitializePublishedWorkflowDefinitions(t.Context(), workflows, scope)
	}

	t.Run("validate unauthorized", func(t *testing.T) {
		service := NewWorkflowApplicationService(WorkflowDependencies{
			Schema: validationSchemaStub{},
			ObjectMap: func(context.Context) map[string]definitionmodel.ObjectSchema {
				return map[string]definitionmodel.ObjectSchema{}
			},
		})
		if _, err := service.ValidateWorkflowDefinition(t.Context(), valid, principalmodel.Principal{}); err == nil {
			t.Fatal("expected workflow query authorization error")
		}
	})
	t.Run("invalid new definition", func(t *testing.T) {
		invalid := valid
		invalid.Graph = &definitionmodel.WorkflowGraphSchema{Version: 1}
		if err := run(t, &workflowDefinitionProjectionStore{}, []definitionmodel.WorkflowSchema{invalid}); err == nil {
			t.Fatal("expected validation error")
		}
	})
	t.Run("insert definition", func(t *testing.T) {
		err := run(t, &workflowDefinitionProjectionStore{insertErr: errors.New("insert failed")}, []definitionmodel.WorkflowSchema{valid})
		if err == nil {
			t.Fatal("expected insert definition error")
		}
	})
	t.Run("publish definition error", func(t *testing.T) {
		err := run(t, &workflowDefinitionProjectionStore{publishErr: errors.New("publish failed")}, []definitionmodel.WorkflowSchema{valid})
		if err == nil {
			t.Fatal("expected publish definition error")
		}
	})
	t.Run("publish definition rejected", func(t *testing.T) {
		err := run(t, &workflowDefinitionProjectionStore{publishOK: boolPointer(false)}, []definitionmodel.WorkflowSchema{valid})
		if err == nil {
			t.Fatal("expected unpublished definition error")
		}
	})
	t.Run("seed and publish definition", func(t *testing.T) {
		store := &workflowDefinitionProjectionStore{}
		if err := run(t, store, []definitionmodel.WorkflowSchema{valid}); err != nil {
			t.Fatal(err)
		}
		if len(store.definitions) != 1 || store.definitions[0].CurrentPublishedVersionID == "" {
			t.Fatalf("workflow projection was not published: %#v", store.definitions)
		}
	})
	t.Run("list definitions", func(t *testing.T) {
		if err := run(t, &workflowDefinitionProjectionStore{listErr: errors.New("list failed")}, nil); err == nil {
			t.Fatal("expected list definitions error")
		}
	})
	t.Run("set enabled error", func(t *testing.T) {
		store := &workflowDefinitionProjectionStore{
			definitions: []workflowmodel.WorkflowDefinition{{ID: "definition-1", Key: valid.Key, Enabled: false}},
			setErr:      errors.New("set enabled failed"),
		}
		if err := run(t, store, []definitionmodel.WorkflowSchema{valid}); err == nil {
			t.Fatal("expected set enabled error")
		}
	})
	t.Run("set enabled rejected", func(t *testing.T) {
		store := &workflowDefinitionProjectionStore{
			definitions: []workflowmodel.WorkflowDefinition{{ID: "definition-1", Key: valid.Key, Enabled: false}},
			setOK:       boolPointer(false),
		}
		if err := run(t, store, []definitionmodel.WorkflowSchema{valid}); err == nil {
			t.Fatal("expected missing definition error")
		}
	})
	t.Run("skip definition without published version", func(t *testing.T) {
		store := &workflowDefinitionProjectionStore{
			definitions: []workflowmodel.WorkflowDefinition{{ID: "definition-1", Key: valid.Key, Enabled: true}},
		}
		if err := run(t, store, []definitionmodel.WorkflowSchema{valid}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("get published version error", func(t *testing.T) {
		store := &workflowDefinitionProjectionStore{
			definitions: []workflowmodel.WorkflowDefinition{publishedDefinition},
			versions:    map[string]workflowmodel.WorkflowDefinitionVersion{"version-1": publishedVersion},
			versionErr:  errors.New("version failed"),
		}
		if err := run(t, store, []definitionmodel.WorkflowSchema{valid}); err == nil {
			t.Fatal("expected get version error")
		}
	})
	t.Run("published version missing", func(t *testing.T) {
		store := &workflowDefinitionProjectionStore{
			definitions: []workflowmodel.WorkflowDefinition{publishedDefinition},
			versions:    map[string]workflowmodel.WorkflowDefinitionVersion{},
		}
		if err := run(t, store, []definitionmodel.WorkflowSchema{valid}); err == nil {
			t.Fatal("expected missing version error")
		}
	})
}
