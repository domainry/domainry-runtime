package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type workflowHTTPDefinitionStore struct {
	definitions map[string]workflowmodel.WorkflowDefinition
	versions    map[string]workflowmodel.WorkflowDefinitionVersion
	err         error
}

func (s *workflowHTTPDefinitionStore) InsertDefinition(_ context.Context, definition workflowmodel.WorkflowDefinition, version workflowmodel.WorkflowDefinitionVersion) error {
	if s.err != nil {
		return s.err
	}
	s.definitions[definition.Key], s.versions[version.ID] = definition, version
	return nil
}

func (s *workflowHTTPDefinitionStore) GetDefinitionByKey(_ context.Context, key string) (workflowmodel.WorkflowDefinition, bool, error) {
	if s.err != nil {
		return workflowmodel.WorkflowDefinition{}, false, s.err
	}
	definition, found := s.definitions[strings.TrimSpace(key)]
	return definition, found, nil
}

func (s *workflowHTTPDefinitionStore) ListDefinitions(context.Context) ([]workflowmodel.WorkflowDefinition, error) {
	if s.err != nil {
		return nil, s.err
	}
	definitions := make([]workflowmodel.WorkflowDefinition, 0, len(s.definitions))
	for _, definition := range s.definitions {
		definitions = append(definitions, definition)
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Key < definitions[j].Key })
	return definitions, nil
}

func (s *workflowHTTPDefinitionStore) GetVersion(_ context.Context, id string) (workflowmodel.WorkflowDefinitionVersion, bool, error) {
	if s.err != nil {
		return workflowmodel.WorkflowDefinitionVersion{}, false, s.err
	}
	version, found := s.versions[strings.TrimSpace(id)]
	return version, found, nil
}

func (s *workflowHTTPDefinitionStore) ListVersions(_ context.Context, definitionID string) ([]workflowmodel.WorkflowDefinitionVersion, error) {
	if s.err != nil {
		return nil, s.err
	}
	versions := []workflowmodel.WorkflowDefinitionVersion{}
	for _, version := range s.versions {
		if version.DefinitionID == definitionID {
			versions = append(versions, version)
		}
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].Version < versions[j].Version })
	return versions, nil
}

func (s *workflowHTTPDefinitionStore) UpdateDraft(_ context.Context, version workflowmodel.WorkflowDefinitionVersion, expectedRevision int) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	current, found := s.versions[version.ID]
	if !found || current.Status != workflowmodel.WorkflowVersionDraft || current.Revision != expectedRevision {
		return false, nil
	}
	version.Revision = expectedRevision + 1
	s.versions[version.ID] = version
	return true, nil
}

func (s *workflowHTTPDefinitionStore) InsertDraftVersion(_ context.Context, definitionID string, version workflowmodel.WorkflowDefinitionVersion) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	for key, definition := range s.definitions {
		if definition.ID == definitionID && definition.CurrentDraftVersionID == "" {
			definition.CurrentDraftVersionID = version.ID
			s.definitions[key], s.versions[version.ID] = definition, version
			return true, nil
		}
	}
	return false, nil
}

func (s *workflowHTTPDefinitionStore) DeleteDraft(_ context.Context, definitionID, versionID string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	for key, definition := range s.definitions {
		if definition.ID == definitionID && definition.CurrentDraftVersionID == versionID {
			delete(s.versions, versionID)
			definition.CurrentDraftVersionID = ""
			s.definitions[key] = definition
			return true, nil
		}
	}
	return false, nil
}

func (s *workflowHTTPDefinitionStore) PublishDraft(_ context.Context, definition workflowmodel.WorkflowDefinition, version workflowmodel.WorkflowDefinitionVersion, key string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	current, found := s.definitions[definition.Key]
	if !found || current.CurrentDraftVersionID != version.ID {
		return false, nil
	}
	version.Status, version.PublishIdempotencyKey = workflowmodel.WorkflowVersionPublished, key
	definition.CurrentDraftVersionID, definition.CurrentPublishedVersionID = "", version.ID
	s.definitions[definition.Key], s.versions[version.ID] = definition, version
	return true, nil
}

func (s *workflowHTTPDefinitionStore) ArchiveVersion(_ context.Context, definitionID, versionID, archivedAt string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	version, found := s.versions[versionID]
	if !found || version.DefinitionID != definitionID || version.Status != workflowmodel.WorkflowVersionPublished {
		return false, nil
	}
	version.Status, version.ArchivedAt = workflowmodel.WorkflowVersionArchived, archivedAt
	s.versions[versionID] = version
	return true, nil
}

func (s *workflowHTTPDefinitionStore) SetDefinitionEnabled(_ context.Context, definitionID string, enabled bool, updatedAt string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	for key, definition := range s.definitions {
		if definition.ID == definitionID {
			definition.Enabled, definition.UpdatedAt = enabled, updatedAt
			s.definitions[key] = definition
			return true, nil
		}
	}
	return false, nil
}

type workflowHTTPRegistry struct {
	items map[string]definitionmodel.WorkflowSchema
}

func (r *workflowHTTPRegistry) List() []definitionmodel.WorkflowSchema {
	items := make([]definitionmodel.WorkflowSchema, 0, len(r.items))
	for _, item := range r.items {
		items = append(items, item)
	}
	return items
}
func (r *workflowHTTPRegistry) Get(key string) (definitionmodel.WorkflowSchema, bool) {
	item, found := r.items[key]
	return item, found
}
func (r *workflowHTTPRegistry) Set(key string, workflow definitionmodel.WorkflowSchema) {
	r.items[key] = workflow
}
func (r *workflowHTTPRegistry) Delete(key string) { delete(r.items, key) }
func (r *workflowHTTPRegistry) Count() int        { return len(r.items) }

type workflowHTTPSchemaProvider struct{}

func (workflowHTTPSchemaProvider) WorkflowSchemaSnapshot(context.Context, principalmodel.Principal) workflowapplication.WorkflowSchemaSnapshot {
	return workflowapplication.WorkflowSchemaSnapshot{}
}
func (workflowHTTPSchemaProvider) ConnectorAdapterExists(context.Context, string) bool { return false }

type workflowHTTPResponse struct {
	status int
	value  any
	err    error
	code   string
}

func newWorkflowHTTPDefinitionFixture() (*WorkflowsHandler, *workflowHTTPDefinitionStore, *workflowHTTPResponse) {
	store := &workflowHTTPDefinitionStore{definitions: map[string]workflowmodel.WorkflowDefinition{}, versions: map[string]workflowmodel.WorkflowDefinitionVersion{}}
	registry := &workflowHTTPRegistry{items: map[string]definitionmodel.WorkflowSchema{}}
	service := workflowapplication.NewWorkflowApplicationService(workflowapplication.WorkflowDependencies{
		Definitions: store, WorkflowRegistry: registry, Schema: workflowHTTPSchemaProvider{},
		ObjectMap: func(context.Context) map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{}
		},
		Audit: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any) {
		},
		AuditMetadata: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) {
		},
	})
	response := &workflowHTTPResponse{}
	principal := workflowHTTPAdminPrincipal()
	handler := NewWorkflowsHandler(WorkflowsDependencies{
		Definitions: service, Processes: service, Executions: service,
		Principal: func(*http.Request) principalmodel.Principal { return principal },
		WriteJSON: func(_ http.ResponseWriter, status int, value any) { response.status, response.value = status, value },
		WriteError: func(_ http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			response.status, response.code = status, code
		},
		WriteServiceError: func(_ http.ResponseWriter, _ *http.Request, err error) { response.err = err },
		DecodeJSON: func(_ http.ResponseWriter, request *http.Request, value any) bool {
			if err := json.NewDecoder(request.Body).Decode(value); err != nil {
				response.status, response.err = http.StatusBadRequest, err
				return false
			}
			return true
		},
	})
	return handler, store, response
}

func workflowHTTPAdminPrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin-1", WorkspaceID: "workspace-1"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "ops.workflow.run", "ops.workflow.simulate", "ops.workflow.retry", "ops.workflow.resolve", "ops.workflow.read", "ops.workflow.process", "workflow.process.read", "workflow.process.operate"}})
}

func workflowHTTPSchema(key, name string) definitionmodel.WorkflowSchema {
	return definitionmodel.WorkflowSchema{Key: key, Name: name, Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger", Name: "Start"}}}}
}

func seedWorkflowHTTPDefinition(store *workflowHTTPDefinitionStore) {
	definition := workflowmodel.WorkflowDefinition{ID: "definition-1", Key: "order.approve", Name: "Order approve", Enabled: true, CurrentDraftVersionID: "draft-3", CurrentPublishedVersionID: "published-2"}
	store.definitions[definition.Key] = definition
	store.versions["published-1"] = workflowmodel.WorkflowDefinitionVersion{ID: "published-1", DefinitionID: definition.ID, Version: 1, Status: workflowmodel.WorkflowVersionPublished, Workflow: workflowHTTPSchema(definition.Key, "Version 1")}
	store.versions["published-2"] = workflowmodel.WorkflowDefinitionVersion{ID: "published-2", DefinitionID: definition.ID, Version: 2, Status: workflowmodel.WorkflowVersionPublished, Workflow: workflowHTTPSchema(definition.Key, "Version 2")}
	store.versions["draft-3"] = workflowmodel.WorkflowDefinitionVersion{ID: "draft-3", DefinitionID: definition.ID, Version: 3, Status: workflowmodel.WorkflowVersionDraft, Revision: 1, Workflow: workflowHTTPSchema(definition.Key, "Draft 3")}
}

func resetWorkflowHTTPResponse(response *workflowHTTPResponse) { *response = workflowHTTPResponse{} }

var errWorkflowHTTPTest = errors.New("workflow repository unavailable")
