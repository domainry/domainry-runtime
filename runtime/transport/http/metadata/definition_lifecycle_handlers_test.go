package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	auditapplication "github.com/domainry/domainry-runtime/runtime/application/audit"
	metadataapplication "github.com/domainry/domainry-runtime/runtime/application/metadata"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	auditrepository "github.com/domainry/domainry-runtime/runtime/domain/audit/repository"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type definitionLifecycleRepository struct {
	metadatarepository.MetadataRepository
	definition         metadatamodel.MetadataDefinition
	found              bool
	getErr             error
	definitions        []metadatamodel.MetadataDefinition
	definitionsErr     error
	versions           []metadatamodel.MetadataDefinitionVersion
	versionsErr        error
	rolledBack         metadatamodel.MetadataDefinition
	published          metadatamodel.MetadataDefinition
	publishErr         error
	completeRefreshErr error
	migrationSteps     []metadatamodel.MetadataMigrationStep
	migrationErr       error
	disableErr         error
	loadErr            error
	syncErr            error
	disableCalls       []string
}

func (r *definitionLifecycleRepository) GetDefinition(context.Context, principalmodel.SystemScope, string, string) (metadatamodel.MetadataDefinition, bool, error) {
	return r.definition, r.found, r.getErr
}

func (r *definitionLifecycleRepository) ListDefinitionVersions(context.Context, principalmodel.SystemScope, string, string) ([]metadatamodel.MetadataDefinitionVersion, error) {
	return append([]metadatamodel.MetadataDefinitionVersion(nil), r.versions...), r.versionsErr
}

func (r *definitionLifecycleRepository) ListDefinitions(context.Context, principalmodel.SystemScope, string) ([]metadatamodel.MetadataDefinition, error) {
	return append([]metadatamodel.MetadataDefinition(nil), r.definitions...), r.definitionsErr
}

func (r *definitionLifecycleRepository) RollbackDefinition(context.Context, principalmodel.SystemScope, string, string, metadatamodel.MetadataDefinitionRollbackRequest, auditmodel.AuditEvent) (metadatamodel.MetadataDefinition, error) {
	return r.rolledBack, nil
}

func (r *definitionLifecycleRepository) PublishDefinition(context.Context, principalmodel.SystemScope, string, string, metadatamodel.MetadataDefinitionUpsertRequest, auditmodel.AuditEvent) (metadatamodel.MetadataDefinition, error) {
	return r.published, r.publishErr
}

func (r *definitionLifecycleRepository) CompleteDefinitionRefresh(context.Context, principalmodel.SystemScope, string, string, string, string) error {
	return r.completeRefreshErr
}

func (r *definitionLifecycleRepository) DisableDefinition(_ context.Context, _ principalmodel.SystemScope, resourceType, resourceKey string) error {
	r.disableCalls = append(r.disableCalls, resourceType+"/"+resourceKey)
	return r.disableErr
}

func (r *definitionLifecycleRepository) LoadManifest(context.Context, principalmodel.SystemScope) (manifestmodel.ManifestSchema, error) {
	return manifestmodel.ManifestSchema{TemplateID: "template", Version: "1", Name: "Runtime"}, r.loadErr
}

func (r *definitionLifecycleRepository) SyncManifest(context.Context, principalmodel.SystemScope, manifestmodel.ManifestSchema) error {
	return r.syncErr
}

func (r *definitionLifecycleRepository) MigrationPlan(context.Context, principalmodel.SystemScope, manifestmodel.ManifestSchema) ([]metadatamodel.MetadataMigrationStep, error) {
	return append([]metadatamodel.MetadataMigrationStep(nil), r.migrationSteps...), r.migrationErr
}

type definitionLifecycleReferences struct {
	graph changeplanmodel.ReferenceGraph
	err   error
}

func (r definitionLifecycleReferences) Graph(context.Context, principalmodel.Principal) (changeplanmodel.ReferenceGraph, error) {
	return r.graph, r.err
}

type definitionLifecycleWorkflows struct{ err error }

func (w definitionLifecycleWorkflows) InitializePublishedWorkflowDefinitions(context.Context, []definitionmodel.WorkflowSchema, principalmodel.SystemScope) error {
	return w.err
}

type definitionLifecycleOperations struct {
	claim            changeplanmodel.ChangePlanOperationClaimResult
	claimErr         error
	draft            changeplanmodel.BusinessChangePlanDraft
	found            bool
	getWorkspace     string
	publishWorkspace string
}

func (o *definitionLifecycleOperations) GetDraft(_ context.Context, workspaceID, _ string) (changeplanmodel.BusinessChangePlanDraft, bool, error) {
	o.getWorkspace = workspaceID
	return o.draft, o.found, nil
}
func (*definitionLifecycleOperations) SaveDraft(context.Context, string, changeplanmodel.BusinessChangePlanDraft, int) (changeplanmodel.BusinessChangePlanDraft, bool, error) {
	return changeplanmodel.BusinessChangePlanDraft{}, false, nil
}
func (o *definitionLifecycleOperations) PublishDraft(_ context.Context, workspaceID, _ string, _ int, _, _ string) (changeplanmodel.BusinessChangePlanDraft, bool, error) {
	o.publishWorkspace = workspaceID
	published := o.draft
	published.Status = "published"
	return published, true, nil
}
func (o *definitionLifecycleOperations) TryBeginOperation(context.Context, string, changeplanmodel.ChangePlanOperationClaimRequest) (changeplanmodel.ChangePlanOperationClaimResult, error) {
	return o.claim, o.claimErr
}
func (*definitionLifecycleOperations) CompleteOperation(context.Context, string, changeplanmodel.ChangePlanOperationCompletion) (changeplanmodel.ChangePlanOperationExecution, error) {
	return changeplanmodel.ChangePlanOperationExecution{}, nil
}
func (*definitionLifecycleOperations) FailOperation(context.Context, string, changeplanmodel.ChangePlanOperationFailure) (changeplanmodel.ChangePlanOperationExecution, error) {
	return changeplanmodel.ChangePlanOperationExecution{}, nil
}

type definitionLifecycleAuditRepository struct {
	auditrepository.AuditRepository
	events []auditmodel.AuditEvent
}

func (r *definitionLifecycleAuditRepository) InsertAuditEvent(_ context.Context, _ string, event auditmodel.AuditEvent) error {
	r.events = append(r.events, event)
	return nil
}

func newDefinitionLifecycleHandler(t *testing.T, repository *definitionLifecycleRepository, references definitionLifecycleReferences, operations *definitionLifecycleOperations) (*MetadataHandler, *localizedTextHandlerCapture, *definitionLifecycleAuditRepository) {
	t.Helper()
	auditRepository := &definitionLifecycleAuditRepository{}
	auditService := auditapplication.NewAuditApplicationService(auditRepository)
	service := metadataapplication.NewMetadataApplicationService(metadataapplication.MetadataApplicationDependencies{
		Repository:    repository,
		Runtime:       localizedTextHandlerRuntime{snapshot: metadatamodel.MetadataSchemaSnapshot{Name: "Runtime", SchemaHash: "schema-hash"}},
		Workflows:     definitionLifecycleWorkflows{},
		References:    references,
		ChangePlans:   operations,
		Audit:         auditService,
		AuditAppender: nil,
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}, RequestID: "request-1"}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
	capture := &localizedTextHandlerCapture{}
	handler := NewMetadataHandler(MetadataDependencies{
		Definitions: service,
		Audit:       auditService,
		Principal:   func(*http.Request) principalmodel.Principal { return principal },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteError: func(w http.ResponseWriter, _ *http.Request, status int, code string, args ...string) {
			capture.errorCode, capture.errorArgs = code, append([]string(nil), args...)
			w.WriteHeader(status)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			capture.serviceErr = err
			w.WriteHeader(http.StatusUnprocessableEntity)
		},
		DecodeJSON: func(w http.ResponseWriter, r *http.Request, value any) bool {
			if err := json.NewDecoder(r.Body).Decode(value); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return false
			}
			return true
		},
	})
	return handler, capture, auditRepository
}

func definitionLifecycleRequest(method, target, body, resourceType, resourceKey string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.SetPathValue("resourceType", resourceType)
	request.SetPathValue("resourceKey", resourceKey)
	return request
}

func TestDefinitionLifecycleGetAndListVersions(t *testing.T) {
	repository := &definitionLifecycleRepository{
		definition: metadatamodel.MetadataDefinition{ResourceType: "view", ResourceKey: "customer-list", SchemaVersion: "2", Payload: json.RawMessage(`{"key":"customer-list"}`)},
		found:      true,
		versions: []metadatamodel.MetadataDefinitionVersion{
			{SchemaVersion: "2", Payload: json.RawMessage(`{"name":"new"}`)},
			{SchemaVersion: "1", Payload: json.RawMessage(`{"name":"old"}`)},
		},
	}
	handler, capture, _ := newDefinitionLifecycleHandler(t, repository, definitionLifecycleReferences{}, &definitionLifecycleOperations{})

	getResponse := httptest.NewRecorder()
	handler.getMetadataDefinition(getResponse, definitionLifecycleRequest(http.MethodGet, "/", "", " view ", " customer-list "))
	if getResponse.Code != http.StatusOK || capture.serviceErr != nil || !strings.Contains(getResponse.Body.String(), `"schema_version":"2"`) {
		t.Fatalf("get status=%d error=%v body=%s", getResponse.Code, capture.serviceErr, getResponse.Body.String())
	}

	listResponse := httptest.NewRecorder()
	handler.listMetadataDefinitionVersions(listResponse, definitionLifecycleRequest(http.MethodGet, "/", "", " view ", " customer-list "))
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"versions"`) || listResponse.Header().Get("ETag") == "" {
		t.Fatalf("list status=%d headers=%v body=%s", listResponse.Code, listResponse.Header(), listResponse.Body.String())
	}

	repository.found = false
	notFoundResponse := httptest.NewRecorder()
	handler.getMetadataDefinition(notFoundResponse, definitionLifecycleRequest(http.MethodGet, "/", "", "view", "missing"))
	if notFoundResponse.Code != http.StatusNotFound || capture.errorCode != "metadata.definition.not_found" {
		t.Fatalf("not found status=%d code=%q", notFoundResponse.Code, capture.errorCode)
	}

	repository.getErr = errors.New("get failed")
	capture.serviceErr = nil
	errorResponse := httptest.NewRecorder()
	handler.getMetadataDefinition(errorResponse, definitionLifecycleRequest(http.MethodGet, "/", "", "view", "customer-list"))
	if errorResponse.Code != http.StatusUnprocessableEntity || capture.serviceErr == nil {
		t.Fatalf("get error status=%d error=%v", errorResponse.Code, capture.serviceErr)
	}

	repository.versionsErr = errors.New("versions failed")
	for _, handle := range []func(http.ResponseWriter, *http.Request){handler.listMetadataDefinitionVersions, handler.diffMetadataDefinitionVersions} {
		capture.serviceErr = nil
		response := httptest.NewRecorder()
		handle(response, definitionLifecycleRequest(http.MethodGet, "/?from=1&to=2", "", "view", "customer-list"))
		if response.Code != http.StatusUnprocessableEntity || capture.serviceErr == nil {
			t.Fatalf("version error status=%d error=%v", response.Code, capture.serviceErr)
		}
	}
}

func TestDiffMetadataDefinitionVersionsUsesLatestVersionWhenToIsOmitted(t *testing.T) {
	repository := &definitionLifecycleRepository{versions: []metadatamodel.MetadataDefinitionVersion{
		{SchemaVersion: "2", Payload: json.RawMessage(`{"name":"new"}`)},
		{SchemaVersion: "1", Payload: json.RawMessage(`{"name":"old"}`)},
	}}
	handler, _, _ := newDefinitionLifecycleHandler(t, repository, definitionLifecycleReferences{}, &definitionLifecycleOperations{})

	response := httptest.NewRecorder()
	handler.diffMetadataDefinitionVersions(response, definitionLifecycleRequest(http.MethodGet, "/?from=%201%20", "", " view ", " customer-list "))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"from_version":"1"`) || !strings.Contains(response.Body.String(), `"to_version":"2"`) || !strings.Contains(response.Body.String(), `"from_payload":{"name":"old"}`) || !strings.Contains(response.Body.String(), `"to_payload":{"name":"new"}`) {
		t.Fatalf("default diff response = %s", response.Body.String())
	}

	explicitResponse := httptest.NewRecorder()
	handler.diffMetadataDefinitionVersions(explicitResponse, definitionLifecycleRequest(http.MethodGet, "/?from=2&to=1", "", "view", "customer-list"))
	if explicitResponse.Code != http.StatusOK || !strings.Contains(explicitResponse.Body.String(), `"to_version":"1"`) {
		t.Fatalf("explicit diff response = %s", explicitResponse.Body.String())
	}

	repository.versions = nil
	emptyResponse := httptest.NewRecorder()
	handler.diffMetadataDefinitionVersions(emptyResponse, definitionLifecycleRequest(http.MethodGet, "/", "", "view", "customer-list"))
	if emptyResponse.Code != http.StatusOK || !strings.Contains(emptyResponse.Body.String(), `"from_payload":null`) || !strings.Contains(emptyResponse.Body.String(), `"to_payload":null`) {
		t.Fatalf("empty diff response = %s", emptyResponse.Body.String())
	}
}

func TestMetadataAuditPayloadProjectsStableViewRegistryFields(t *testing.T) {
	if value := metadataAuditPayload(json.RawMessage(`{`)); len(value) != 0 {
		t.Fatalf("invalid payload = %#v", value)
	}
	payload := metadataAuditPayload(json.RawMessage(`{"key":"customer-list","object_key":"customer","type":"table","ignored":"value","config":{"view_registry":{"scope":"workspace","locked":true,"default_for_object":false,"team_key":"sales","migrated_from_local_storage":true}}}`))
	if len(payload) != 8 || payload["key"] != "customer-list" || payload["scope"] != "workspace" || payload["locked"] != true || payload["team_key"] != "sales" {
		t.Fatalf("projected payload = %#v", payload)
	}
	withoutConfig := metadataAuditPayload(json.RawMessage(`{"key":"plain","config":"invalid"}`))
	if len(withoutConfig) != 1 || withoutConfig["key"] != "plain" {
		t.Fatalf("non-map config payload = %#v", withoutConfig)
	}
	withoutRegistry := metadataAuditPayload(json.RawMessage(`{"config":{"view_registry":"invalid"}}`))
	if len(withoutRegistry) != 0 {
		t.Fatalf("non-map registry payload = %#v", withoutRegistry)
	}
}
