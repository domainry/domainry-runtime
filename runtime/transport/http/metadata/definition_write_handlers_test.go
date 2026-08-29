package metadata

import (
	"encoding/json"
	"errors"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	auditapplication "github.com/domainry/domainry-runtime/runtime/application/audit"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	metadataapplication "github.com/domainry/domainry-runtime/runtime/application/metadata"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func newDefinitionWriteHandler(t *testing.T, repository *definitionLifecycleRepository) (*MetadataHandler, *localizedTextHandlerCapture, *definitionLifecycleAuditRepository) {
	t.Helper()
	auditRepository := &definitionLifecycleAuditRepository{}
	auditService := auditapplication.NewAuditApplicationService(auditRepository)
	definitions := metadataapplication.NewApplicationSchemaService(metadataapplication.ApplicationSchemaDependencies{
		Repository: repository,
		Runtime:    localizedTextHandlerRuntime{snapshot: metadatamodel.ApplicationSchemaSnapshot{Name: "Runtime", SchemaHash: "schema-hash", Objects: []definitionmodel.ObjectSchema{{Key: "customer"}}}},
		Workflows:  definitionLifecycleWorkflows{},
		Audit:      auditService,
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}, RequestID: "request-1"}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
	capture := &localizedTextHandlerCapture{}
	handler := NewMetadataHandler(MetadataDependencies{
		Definitions:  definitions,
		Capabilities: capabilityapplication.NewCapabilityAuthoringApplicationService(nil),
		Audit:        auditService,
		Principal:    func(*http.Request) principalmodel.Principal { return principal },
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

func TestListMetadataDefinitionsDefaultsWorkspaceAndRejectsFailures(t *testing.T) {
	repository := &definitionLifecycleRepository{definitions: []metadatamodel.MetadataDefinition{{ResourceType: "view", ResourceKey: "customer-list"}}}
	handler, capture, _ := newDefinitionWriteHandler(t, repository)

	response := httptest.NewRecorder()
	handler.listMetadataDefinitions(response, definitionLifecycleRequest(http.MethodGet, "/metadata/definitions/view", "", " view ", ""))
	if response.Code != http.StatusOK || capture.serviceErr != nil || !strings.Contains(response.Body.String(), `"resource_type":"view"`) || response.Header().Get("ETag") == "" {
		t.Fatalf("list status=%d error=%v headers=%v body=%s", response.Code, capture.serviceErr, response.Header(), response.Body.String())
	}

	capture.serviceErr = nil
	crossWorkspaceResponse := httptest.NewRecorder()
	handler.listMetadataDefinitions(crossWorkspaceResponse, definitionLifecycleRequest(http.MethodGet, "/?workspace_id=workspace-b", "", "view", ""))
	if crossWorkspaceResponse.Code != http.StatusUnprocessableEntity || capture.serviceErr == nil {
		t.Fatalf("cross workspace status=%d error=%v", crossWorkspaceResponse.Code, capture.serviceErr)
	}

	repository.definitionsErr = errors.New("list failed")
	capture.serviceErr = nil
	errorResponse := httptest.NewRecorder()
	handler.listMetadataDefinitions(errorResponse, definitionLifecycleRequest(http.MethodGet, "/?workspace_id=workspace-a", "", "view", ""))
	if errorResponse.Code != http.StatusUnprocessableEntity || capture.serviceErr == nil {
		t.Fatalf("list failure status=%d error=%v", errorResponse.Code, capture.serviceErr)
	}
}

func TestValidateMetadataDefinitionHandlerSuccessDecodeAndValidationError(t *testing.T) {
	handler, capture, _ := newDefinitionWriteHandler(t, &definitionLifecycleRepository{})

	request := definitionLifecycleRequest(http.MethodPost, "/", `{"payload":{"key":"theme","name":"Theme","value_type":"text","value":"dark","effective_from":"2026-07-01"}}`, " preference ", " theme ")
	response := httptest.NewRecorder()
	handler.validateMetadataDefinition(response, request)
	if response.Code != http.StatusOK || capture.serviceErr != nil || !strings.Contains(response.Body.String(), `"valid":true`) {
		t.Fatalf("validate status=%d error=%v body=%s", response.Code, capture.serviceErr, response.Body.String())
	}

	badJSONResponse := httptest.NewRecorder()
	handler.validateMetadataDefinition(badJSONResponse, definitionLifecycleRequest(http.MethodPost, "/", "{", "preference", "theme"))
	if badJSONResponse.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON status = %d", badJSONResponse.Code)
	}

	invalidResponse := httptest.NewRecorder()
	handler.validateMetadataDefinition(invalidResponse, definitionLifecycleRequest(http.MethodPost, "/", `{"payload":{}}`, "unsupported", "key"))
	if invalidResponse.Code != http.StatusOK || capture.serviceErr != nil || !strings.Contains(invalidResponse.Body.String(), `"valid":false`) || !strings.Contains(invalidResponse.Body.String(), `"error_code":"backend.metadata.resource_type_invalid"`) {
		t.Fatalf("invalid definition status=%d error=%v body=%s", invalidResponse.Code, capture.serviceErr, invalidResponse.Body.String())
	}

	directRequest := definitionLifecycleRequest(http.MethodPost, "/", `{"payload":{}}`, "unsupported", "key")
	directRequest = directRequest.WithContext(operationscontract.WithBuilderTaskID(directRequest.Context(), "task-1"))
	directResponse := httptest.NewRecorder()
	handler.validateMetadataDefinition(directResponse, directRequest)
	if directResponse.Code != http.StatusUnprocessableEntity || !strings.Contains(directResponse.Body.String(), `"error_class":"repairable"`) || !strings.Contains(directResponse.Body.String(), `"repair_action":"repair_capability_payload"`) || !strings.Contains(directResponse.Body.String(), `"retryable":true`) || !strings.Contains(directResponse.Body.String(), `"errors":[`) {
		t.Fatalf("direct validation status=%d body=%s", directResponse.Code, directResponse.Body.String())
	}
	emptyFailureResponse := httptest.NewRecorder()
	handler.writeMetadataAuthoringValidationResult(emptyFailureResponse, directRequest, metadatamodel.MetadataDefinitionValidationResult{Valid: false})
	if emptyFailureResponse.Code != http.StatusOK {
		t.Fatalf("empty validation failure status=%d", emptyFailureResponse.Code)
	}

	handler.principal = func(*http.Request) principalmodel.Principal { return principalmodel.Principal{} }
	capture.serviceErr = nil
	unauthorizedResponse := httptest.NewRecorder()
	handler.validateMetadataDefinition(unauthorizedResponse, definitionLifecycleRequest(http.MethodPost, "/", `{"payload":{"key":"theme"}}`, "preference", "theme"))
	if unauthorizedResponse.Code != http.StatusUnprocessableEntity || capture.serviceErr == nil {
		t.Fatalf("unauthorized validation status=%d error=%v", unauthorizedResponse.Code, capture.serviceErr)
	}
}
