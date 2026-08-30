package appschema

import (
	"context"
	"encoding/json"
	"errors"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type localizedTextHandlerRepository struct {
	appschemarepository.ApplicationSchemaRepository
	values       []appschemamodel.LocalizedText
	listErr      error
	upsertErr    error
	failUpsertAt int
	queries      []appschemamodel.LocalizedTextQuery
	upserts      []appschemamodel.LocalizedTextUpsertRequest
}

func (r *localizedTextHandlerRepository) ListLocalizedTexts(_ context.Context, workspaceID string, query appschemamodel.LocalizedTextQuery) ([]appschemamodel.LocalizedText, error) {
	query.WorkspaceID = workspaceID
	r.queries = append(r.queries, query)
	if r.listErr != nil {
		return nil, r.listErr
	}
	return append([]appschemamodel.LocalizedText(nil), r.values...), nil
}

func (r *localizedTextHandlerRepository) UpsertLocalizedText(_ context.Context, workspaceID string, req appschemamodel.LocalizedTextUpsertRequest) (appschemamodel.LocalizedText, error) {
	req.WorkspaceID = workspaceID
	r.upserts = append(r.upserts, req)
	if r.upsertErr != nil || r.failUpsertAt > 0 && len(r.upserts) == r.failUpsertAt {
		if r.upsertErr != nil {
			return appschemamodel.LocalizedText{}, r.upsertErr
		}
		return appschemamodel.LocalizedText{}, errors.New("upsert failed")
	}
	return appschemamodel.LocalizedText{WorkspaceID: workspaceID, EntityType: req.EntityType, EntityKey: req.EntityKey, Property: req.Property, Locale: req.Locale, Text: req.Text}, nil
}

type localizedTextHandlerRuntime struct {
	snapshot appschemamodel.ApplicationSchemaSnapshot
}

func (r localizedTextHandlerRuntime) Schema() appschemamodel.ApplicationSchemaSnapshot {
	return r.snapshot
}
func (localizedTextHandlerRuntime) ApplyManifestMetadata(string, string, string, []definitionmodel.ObjectSchema, []definitionmodel.ViewSchema, []definitionmodel.ActionSchema, []definitionmodel.WorkflowSchema, []automationmodel.AutomationRuleSchema, []appschemamodel.DictionarySchema, integrationmodel.IntegrationSchema, []reportmodel.ReportSchema, []definitionmodel.EntryPointSchema, []agentmodel.SkillSchema, []agentmodel.AgentSchema, []profilebindingmodel.Binding) {
}

type localizedTextHandlerDictionary struct{ invalidations int }

func (d *localizedTextHandlerDictionary) Invalidate() { d.invalidations++ }
func (*localizedTextHandlerDictionary) Items(context.Context, appschemarepository.ApplicationSchemaRepository, string, string, principalmodel.Principal) (appschemamodel.DictionaryItemsResult, bool, error) {
	return appschemamodel.DictionaryItemsResult{}, false, nil
}

type localizedTextHandlerCapture struct {
	serviceErr error
	errorCode  string
	errorArgs  []string
}

func newLocalizedTextHandler(t *testing.T, repository *localizedTextHandlerRepository, snapshot appschemamodel.ApplicationSchemaSnapshot) (*ApplicationSchemaHandler, *localizedTextHandlerDictionary, *localizedTextHandlerCapture) {
	t.Helper()
	dictionary := &localizedTextHandlerDictionary{}
	localizedTexts := appschemaapplication.NewApplicationSchemaApplicationService(appschemaapplication.ApplicationSchemaDependencies{
		Repository: repository,
		Runtime:    localizedTextHandlerRuntime{snapshot: snapshot},
		Dictionary: dictionary,
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
	capture := &localizedTextHandlerCapture{}
	handler := NewApplicationSchemaHandler(ApplicationSchemaDependencies{
		LocalizedTexts: localizedTexts,
		Principal:      func(*http.Request) principalmodel.Principal { return principal },
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
	return handler, dictionary, capture
}

func TestLocalizedTextHandlersDefaultWorkspaceAndReturnExports(t *testing.T) {
	repository := &localizedTextHandlerRepository{values: []appschemamodel.LocalizedText{{WorkspaceID: "workspace-a", EntityType: "object", EntityKey: "customer", Property: "name", Locale: "en-US", Text: "Customer"}}}
	snapshot := appschemamodel.ApplicationSchemaSnapshot{Name: "CRM", Objects: []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}}}
	handler, dictionary, capture := newLocalizedTextHandler(t, repository, snapshot)

	listRequest := httptest.NewRequest(http.MethodGet, "/metadata/localized-texts?entity_type=%20object%20&entity_key=%20customer%20&property=%20name%20&locale=%20en-US%20", nil)
	listResponse := httptest.NewRecorder()
	handler.listLocalizedTexts(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || capture.serviceErr != nil || len(repository.queries) != 1 {
		t.Fatalf("list status=%d error=%v queries=%#v", listResponse.Code, capture.serviceErr, repository.queries)
	}
	query := repository.queries[0]
	if query.WorkspaceID != "workspace-a" || query.EntityType != "object" || query.EntityKey != "customer" || query.Property != "name" || query.Locale != "en-US" {
		t.Fatalf("normalized list query = %#v", query)
	}
	if listResponse.Header().Get("ETag") == "" || listResponse.Header().Get("Cache-Control") == "" || !strings.Contains(listResponse.Body.String(), `"localized_texts"`) {
		t.Fatalf("list response headers=%v body=%s", listResponse.Header(), listResponse.Body.String())
	}

	notModifiedRequest := httptest.NewRequest(http.MethodGet, "/metadata/localized-texts", nil)
	notModifiedRequest.Header.Set("If-None-Match", listResponse.Header().Get("ETag"))
	notModifiedResponse := httptest.NewRecorder()
	handler.listLocalizedTexts(notModifiedResponse, notModifiedRequest)
	if notModifiedResponse.Code != http.StatusNotModified {
		t.Fatalf("conditional list status = %d", notModifiedResponse.Code)
	}

	upsertRequest := httptest.NewRequest(http.MethodPut, "/metadata/localized-texts", strings.NewReader(`{"entity_type":"object","entity_key":"customer","property":"name","locale":"fr-FR","text":"Client"}`))
	upsertResponse := httptest.NewRecorder()
	handler.upsertLocalizedText(upsertResponse, upsertRequest)
	if upsertResponse.Code != http.StatusOK || len(repository.upserts) != 1 || repository.upserts[0].WorkspaceID != "workspace-a" || dictionary.invalidations != 1 {
		t.Fatalf("upsert status=%d requests=%#v invalidations=%d", upsertResponse.Code, repository.upserts, dictionary.invalidations)
	}

	coverageRequest := httptest.NewRequest(http.MethodGet, "/metadata/localized-texts/coverage?locale=%20fr-FR%20&fallback_locale=%20en-US%20", nil)
	coverageResponse := httptest.NewRecorder()
	handler.localizedTextCoverage(coverageResponse, coverageRequest)
	if coverageResponse.Code != http.StatusOK || !strings.Contains(coverageResponse.Body.String(), `"workspace_id":"workspace-a"`) {
		t.Fatalf("coverage status=%d body=%s", coverageResponse.Code, coverageResponse.Body.String())
	}

	for _, test := range []struct {
		name        string
		handle      func(http.ResponseWriter, *http.Request)
		contentType string
		bodyPrefix  string
	}{
		{name: "csv", handle: handler.exportLocalizedTexts, contentType: "text/csv; charset=utf-8", bodyPrefix: "workspace_id,entity_type"},
		{name: "xlsx", handle: handler.exportLocalizedTextsXLSX, contentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", bodyPrefix: "PK"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/metadata/localized-texts/export?locale=en-US", nil)
			response := httptest.NewRecorder()
			test.handle(response, request)
			if response.Code != http.StatusOK || response.Header().Get("Content-Type") != test.contentType || !strings.HasPrefix(response.Body.String(), test.bodyPrefix) {
				t.Fatalf("status=%d headers=%v body prefix=%q", response.Code, response.Header(), response.Body.String()[:min(30, response.Body.Len())])
			}
			if repository.queries[len(repository.queries)-1].WorkspaceID != "workspace-a" {
				t.Fatalf("export query = %#v", repository.queries[len(repository.queries)-1])
			}
		})
	}

	explicit := localizedTextQuery(httptest.NewRequest(http.MethodGet, "/?workspace_id=%20workspace-b%20&entity_type=%20field%20", nil), "workspace-a")
	if explicit.WorkspaceID != "workspace-b" || explicit.EntityType != "field" {
		t.Fatalf("explicit query = %#v", explicit)
	}
}

func TestMetadataPayloadETagFallsBackForUnencodablePayload(t *testing.T) {
	etag := metadataPayloadETag(make(chan int))
	if etag == "" || etag != metadataPayloadETag(make(chan string)) {
		t.Fatalf("fallback ETag = %q", etag)
	}
}

func TestLocalizedTextHandlersReportDecodeAndServiceErrors(t *testing.T) {
	repository := &localizedTextHandlerRepository{}
	handler, _, capture := newLocalizedTextHandler(t, repository, appschemamodel.ApplicationSchemaSnapshot{})

	badJSONResponse := httptest.NewRecorder()
	handler.upsertLocalizedText(badJSONResponse, httptest.NewRequest(http.MethodPut, "/", strings.NewReader("{")))
	if badJSONResponse.Code != http.StatusBadRequest || len(repository.upserts) != 0 {
		t.Fatalf("bad JSON status=%d upserts=%d", badJSONResponse.Code, len(repository.upserts))
	}

	repository.upsertErr = errors.New("write unavailable")
	upsertResponse := httptest.NewRecorder()
	handler.upsertLocalizedText(upsertResponse, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"workspace_id":"workspace-a","entity_type":"object","entity_key":"customer","property":"name","locale":"en-US","text":"Customer"}`)))
	if upsertResponse.Code != http.StatusUnprocessableEntity || capture.serviceErr == nil {
		t.Fatalf("upsert error status=%d error=%v", upsertResponse.Code, capture.serviceErr)
	}

	repository.upsertErr = nil
	repository.listErr = errors.New("read unavailable")
	for _, handle := range []func(http.ResponseWriter, *http.Request){handler.listLocalizedTexts, handler.localizedTextCoverage, handler.exportLocalizedTexts, handler.exportLocalizedTextsXLSX} {
		capture.serviceErr = nil
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/?locale=en-US", nil)
		handle(response, request)
		if response.Code != http.StatusUnprocessableEntity || capture.serviceErr == nil {
			t.Fatalf("service error status=%d error=%v", response.Code, capture.serviceErr)
		}
	}
}

func TestImportLocalizedTextsValidatesCatalogAndDefaultsWorkspace(t *testing.T) {
	snapshot := appschemamodel.ApplicationSchemaSnapshot{Name: "CRM", Objects: []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}}}
	repository := &localizedTextHandlerRepository{}
	handler, dictionary, capture := newLocalizedTextHandler(t, repository, snapshot)

	csv := "workspace_id,entity_type,entity_key,property,locale,text\n" +
		",object,customer,name,fr-FR,Client\n"
	response := httptest.NewRecorder()
	handler.importLocalizedTexts(response, httptest.NewRequest(http.MethodPost, "/metadata/localized-texts/import", strings.NewReader(csv)))
	if response.Code != http.StatusOK || capture.serviceErr != nil || len(repository.upserts) != 1 || dictionary.invalidations != 1 {
		t.Fatalf("import status=%d error=%v upserts=%#v invalidations=%d", response.Code, capture.serviceErr, repository.upserts, dictionary.invalidations)
	}
	for _, request := range repository.upserts {
		if request.WorkspaceID != "workspace-a" || request.SourceKind != "user" || request.SourceID != "metadata_csv_import" {
			t.Fatalf("normalized import request = %#v", request)
		}
	}
	if !strings.Contains(response.Body.String(), `"count":1`) {
		t.Fatalf("import body = %s", response.Body.String())
	}

	explicitWorkspaceResponse := httptest.NewRecorder()
	handler.importLocalizedTexts(explicitWorkspaceResponse, httptest.NewRequest(http.MethodPost, "/metadata/localized-texts/import", strings.NewReader(
		"workspace_id,entity_type,entity_key,property,locale,text\nworkspace-a,object,customer,name,de-DE,Kunde\n",
	)))
	if explicitWorkspaceResponse.Code != http.StatusOK || len(repository.upserts) != 2 || repository.upserts[1].WorkspaceID != "workspace-a" {
		t.Fatalf("explicit workspace status=%d upserts=%#v", explicitWorkspaceResponse.Code, repository.upserts)
	}

	allowed, err := handler.localizedTextImportAllowedKeys(t.Context(), accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}}))
	if err != nil || !allowed[localizedTextImportKey("object", "customer", "name")] {
		t.Fatalf("allowed=%#v err=%v", allowed, err)
	}
}

func TestImportLocalizedTextsRejectsInvalidInputAndDependencyFailures(t *testing.T) {
	snapshot := appschemamodel.ApplicationSchemaSnapshot{Name: "CRM", Objects: []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}}}
	tests := []struct {
		name             string
		body             string
		listErr          error
		failUpsertAt     int
		wantCode         string
		wantServiceError bool
	}{
		{name: "invalid csv", body: `"unterminated`, wantCode: "metadata.localized_texts.invalid_csv"},
		{name: "unknown schema key", body: "workspace_id,entity_type,entity_key,property,locale,text\n,object,missing,name,en-US,Missing\n", wantCode: "metadata.localized_texts.invalid_csv"},
		{name: "coverage failure", body: "workspace_id,entity_type,entity_key,property,locale,text\n,object,customer,name,en-US,Customer\n", listErr: errors.New("coverage failed"), wantServiceError: true},
		{name: "upsert failure", body: "workspace_id,entity_type,entity_key,property,locale,text\n,object,customer,name,en-US,Customer\n", failUpsertAt: 1, wantServiceError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &localizedTextHandlerRepository{listErr: test.listErr, failUpsertAt: test.failUpsertAt}
			handler, _, capture := newLocalizedTextHandler(t, repository, snapshot)
			response := httptest.NewRecorder()
			handler.importLocalizedTexts(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body)))
			if test.wantServiceError {
				if response.Code != http.StatusUnprocessableEntity || capture.serviceErr == nil {
					t.Fatalf("status=%d service error=%v", response.Code, capture.serviceErr)
				}
				return
			}
			if response.Code != http.StatusBadRequest || capture.errorCode != test.wantCode || len(capture.errorArgs) != 2 || capture.errorArgs[0] != "detail" {
				t.Fatalf("status=%d code=%q args=%#v", response.Code, capture.errorCode, capture.errorArgs)
			}
		})
	}
}
