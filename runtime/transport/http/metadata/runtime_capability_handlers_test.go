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

	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	metadataapplication "github.com/domainry/domainry-runtime/runtime/application/metadata"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

type runtimeMetadataDictionary struct {
	result metadatamodel.DictionaryItemsResult
	found  bool
	err    error
	key    string
	locale string
}

func (*runtimeMetadataDictionary) Invalidate() {}
func (d *runtimeMetadataDictionary) Items(_ context.Context, _ metadatarepository.MetadataRepository, key, locale string, _ principalmodel.Principal) (metadatamodel.DictionaryItemsResult, bool, error) {
	d.key, d.locale = key, locale
	return d.result, d.found, d.err
}

type runtimeMetadataRecordRepository struct {
	recordrepository.RecordRepository
	err error
}

func (r *runtimeMetadataRecordRepository) ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return recordmodel.RecordPageResult{Total: 3, Page: 1, PageSize: 1}, r.err
}

func newRuntimeCapabilityHandler(t *testing.T, repository *definitionLifecycleRepository, dictionary *runtimeMetadataDictionary) (*MetadataHandler, *localizedTextHandlerCapture, *int) {
	t.Helper()
	runtimeCatalog := metadataapplication.NewMetadataApplicationService(metadataapplication.MetadataApplicationDependencies{
		Repository: repository,
		Runtime:    localizedTextHandlerRuntime{snapshot: metadatamodel.MetadataSchemaSnapshot{Name: "Runtime", SchemaHash: "schema-hash", Objects: []definitionmodel.ObjectSchema{{Key: "customer"}}}},
		Workflows:  definitionLifecycleWorkflows{},
		Dictionary: dictionary,
		Records:    &runtimeMetadataRecordRepository{},
	})
	capabilities := capabilityapplication.NewCapabilityAuthoringApplicationService(nil)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
	capture, legacyCalls := &localizedTextHandlerCapture{}, 0
	handler := NewMetadataHandler(MetadataDependencies{
		RuntimeCatalog: runtimeCatalog,
		Capabilities:   capabilities,
		Principal:      func(*http.Request) principalmodel.Principal { return principal },
		LegacyHeaders:  func(w http.ResponseWriter) { legacyCalls++; w.Header().Set("X-Legacy", "true") },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			capture.serviceErr = err
			w.WriteHeader(http.StatusUnprocessableEntity)
		},
	})
	return handler, capture, &legacyCalls
}

func TestRuntimeCapabilityHandlersSuccessAndServiceErrors(t *testing.T) {
	repository := &definitionLifecycleRepository{migrationSteps: []metadatamodel.MetadataMigrationStep{{ObjectKey: "customer", Operation: "create_table"}}}
	dictionary := &runtimeMetadataDictionary{result: metadatamodel.DictionaryItemsResult{DictionaryKey: "status"}, found: true}
	handler, capture, legacyCalls := newRuntimeCapabilityHandler(t, repository, dictionary)

	migrationResponse := httptest.NewRecorder()
	handler.metadataMigrationPlan(migrationResponse, httptest.NewRequest(http.MethodGet, "/metadata/migration-plan", nil))
	if migrationResponse.Code != http.StatusOK || !strings.Contains(migrationResponse.Body.String(), `"count":1`) || !strings.Contains(migrationResponse.Body.String(), `"create_table"`) {
		t.Fatalf("migration status=%d body=%s", migrationResponse.Code, migrationResponse.Body.String())
	}

	recordCountRequest := httptest.NewRequest(http.MethodGet, "/tenant-admin/metadata/objects/customer/record-count", nil)
	recordCountRequest.SetPathValue("objectKey", "customer")
	recordCountResponse := httptest.NewRecorder()
	handler.metadataObjectRecordCount(recordCountResponse, recordCountRequest)
	if recordCountResponse.Code != http.StatusOK || !strings.Contains(recordCountResponse.Body.String(), `"object_key":"customer"`) || !strings.Contains(recordCountResponse.Body.String(), `"count":3`) {
		t.Fatalf("record count status=%d body=%s", recordCountResponse.Code, recordCountResponse.Body.String())
	}
	runtimeCatalog := handler.runtimeCatalog
	runtimeCatalogWithError := metadataapplication.NewMetadataApplicationService(metadataapplication.MetadataApplicationDependencies{
		Repository: repository,
		Runtime:    localizedTextHandlerRuntime{snapshot: metadatamodel.MetadataSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "customer"}}}},
		Workflows:  definitionLifecycleWorkflows{}, Records: &runtimeMetadataRecordRepository{err: errors.New("count failed")},
	})
	handler.runtimeCatalog = runtimeCatalogWithError
	capture.serviceErr = nil
	failedCount := httptest.NewRecorder()
	handler.metadataObjectRecordCount(failedCount, recordCountRequest)
	if failedCount.Code != http.StatusUnprocessableEntity || capture.serviceErr == nil {
		t.Fatalf("failed record count status=%d err=%v", failedCount.Code, capture.serviceErr)
	}
	handler.runtimeCatalog = runtimeCatalog

	metadataResponse := httptest.NewRecorder()
	handler.listCapabilities(metadataResponse, httptest.NewRequest(http.MethodGet, "/metadata/capabilities", nil))
	if metadataResponse.Code != http.StatusOK || metadataResponse.Header().Get("X-Legacy") != "true" || !strings.Contains(metadataResponse.Body.String(), `"authoring_capabilities"`) {
		t.Fatalf("metadata capabilities status=%d headers=%v body=%s", metadataResponse.Code, metadataResponse.Header(), metadataResponse.Body.String())
	}

	executionResponse := httptest.NewRecorder()
	handler.executionCapabilities(executionResponse, httptest.NewRequest(http.MethodGet, "/execution-capabilities", nil))
	if executionResponse.Code != http.StatusOK || executionResponse.Header().Get("X-Legacy") != "true" || *legacyCalls != 2 || !strings.Contains(executionResponse.Body.String(), `"workflow_nodes"`) {
		t.Fatalf("execution capabilities status=%d legacy=%d body=%s", executionResponse.Code, *legacyCalls, executionResponse.Body.String())
	}

	reloadResponse := httptest.NewRecorder()
	handler.reloadMetadata(reloadResponse, httptest.NewRequest(http.MethodPost, "/metadata/reload", nil))
	if reloadResponse.Code != http.StatusOK || !strings.Contains(reloadResponse.Body.String(), `"schema_hash":"schema-hash"`) {
		t.Fatalf("reload status=%d body=%s", reloadResponse.Code, reloadResponse.Body.String())
	}

	dictionaryRequest := httptest.NewRequest(http.MethodGet, "/dictionaries/status/items?locale=%20en-US%20", nil)
	dictionaryRequest.SetPathValue("dictionaryKey", " status ")
	dictionaryResponse := httptest.NewRecorder()
	handler.getDictionaryItems(dictionaryResponse, dictionaryRequest)
	if dictionaryResponse.Code != http.StatusOK || dictionary.key != "status" || dictionary.locale != "en-US" || dictionaryResponse.Header().Get("Cache-Control") == "" {
		t.Fatalf("dictionary status=%d key=%q locale=%q headers=%v", dictionaryResponse.Code, dictionary.key, dictionary.locale, dictionaryResponse.Header())
	}

	repository.migrationErr = errors.New("migration failed")
	capture.serviceErr = nil
	migrationFailure := httptest.NewRecorder()
	handler.metadataMigrationPlan(migrationFailure, httptest.NewRequest(http.MethodGet, "/", nil))
	if migrationFailure.Code != http.StatusUnprocessableEntity || capture.serviceErr == nil {
		t.Fatalf("migration failure status=%d error=%v", migrationFailure.Code, capture.serviceErr)
	}

	repository.migrationErr = nil
	repository.loadErr = errors.New("reload failed")
	capture.serviceErr = nil
	reloadFailure := httptest.NewRecorder()
	handler.reloadMetadata(reloadFailure, httptest.NewRequest(http.MethodPost, "/", nil))
	if reloadFailure.Code != http.StatusUnprocessableEntity || capture.serviceErr == nil {
		t.Fatalf("reload failure status=%d error=%v", reloadFailure.Code, capture.serviceErr)
	}

	dictionary.err = errors.New("dictionary failed")
	capture.serviceErr = nil
	dictionaryFailure := httptest.NewRecorder()
	handler.getDictionaryItems(dictionaryFailure, dictionaryRequest)
	if dictionaryFailure.Code != http.StatusUnprocessableEntity || capture.serviceErr == nil {
		t.Fatalf("dictionary failure status=%d error=%v", dictionaryFailure.Code, capture.serviceErr)
	}
}

func TestCapabilityHandlersRejectUnknownPrincipalWithoutLegacyHeaders(t *testing.T) {
	handler, capture, legacyCalls := newRuntimeCapabilityHandler(t, &definitionLifecycleRepository{}, &runtimeMetadataDictionary{})
	handler.principal = func(*http.Request) principalmodel.Principal { return principalmodel.Principal{} }

	for _, handle := range []func(http.ResponseWriter, *http.Request){handler.listCapabilities, handler.executionCapabilities} {
		capture.serviceErr = nil
		response := httptest.NewRecorder()
		handle(response, httptest.NewRequest(http.MethodGet, "/", nil))
		if response.Code != http.StatusUnprocessableEntity || capture.serviceErr == nil {
			t.Fatalf("capability failure status=%d error=%v", response.Code, capture.serviceErr)
		}
	}
	if *legacyCalls != 0 {
		t.Fatalf("legacy headers called on failures: %d", *legacyCalls)
	}
}
