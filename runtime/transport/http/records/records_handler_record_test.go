package records

import (
	"context"
	"errors"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	pipelineapplication "github.com/domainry/domainry-runtime/runtime/application/pipeline"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	runtimeactioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	endpointmodel "github.com/domainry/domainry-runtime/runtime/domain/endpoint/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordruntime "github.com/domainry/domainry-runtime/runtime/domain/record/runtime"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type recordsHTTPRepository struct {
	page      recordmodel.RecordPageResult
	record    recordmodel.Record
	found     bool
	err       error
	getCalls  int
	lastQuery recordmodel.RecordListQuery
}

func recordsAuthorizedEndpointRequest(t *testing.T, request *http.Request, endpointIdentity string) *http.Request {
	t.Helper()
	definition, err := endpointmodel.AuthorizationActionDefinition(endpointmodel.EndpointContracts[endpointIdentity])
	if err != nil {
		t.Fatal(err)
	}
	return request.WithContext(runtimeactioncontract.WithAuthorizedAction(request.Context(), definition))
}

type recordsHTTPMutationExecutionStore struct {
	commit *transactionmodel.RecordMutationCommit
}

func (recordsHTTPMutationExecutionStore) TryBeginRecordMutation(_ context.Context, request recordmodel.RecordMutationClaimRequest) (recordmodel.RecordMutationClaimResult, error) {
	execution := request.Execution
	execution.ID = "record-mutation"
	execution.LeaseOwner = request.LeaseOwner
	execution.FencingToken = 1
	return recordmodel.RecordMutationClaimResult{Decision: idempotency.DecisionAcquired, Execution: execution}, nil
}

func (s recordsHTTPMutationExecutionStore) CommitRecordMutationExecution(_ context.Context, commit transactionmodel.RecordMutationCommit, completion recordmodel.RecordMutationCompletion) (recordmodel.RecordMutationExecution, error) {
	if s.commit != nil {
		*s.commit = commit
	}
	return recordmodel.RecordMutationExecution{
		ID: completion.ExecutionID, WorkspaceID: completion.WorkspaceID, Result: commit.Record,
	}, nil
}

func (s recordsHTTPMutationExecutionStore) CommitRecordMutationBatchExecution(_ context.Context, commits []transactionmodel.RecordMutationCommit, completion recordmodel.RecordMutationCompletion) (recordmodel.RecordMutationExecution, error) {
	if s.commit != nil && len(commits) > 0 {
		*s.commit = commits[len(commits)-1]
	}
	return recordmodel.RecordMutationExecution{ID: completion.ExecutionID, WorkspaceID: completion.WorkspaceID}, nil
}

func (recordsHTTPMutationExecutionStore) CompleteRecordMutationExecution(_ context.Context, completion recordmodel.RecordMutationCompletion) (recordmodel.RecordMutationExecution, error) {
	return recordmodel.RecordMutationExecution{ID: completion.ExecutionID, WorkspaceID: completion.WorkspaceID}, nil
}

func (r *recordsHTTPRepository) ListRecords(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	r.lastQuery = query
	return r.page, r.err
}
func (r *recordsHTTPRepository) GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	r.getCalls++
	return r.record, r.found, r.err
}
func (*recordsHTTPRepository) InsertRecord(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record) error {
	return nil
}
func (*recordsHTTPRepository) UpdateRecord(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record) error {
	return nil
}
func (*recordsHTTPRepository) UpdateRecordWhere(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
	return true, nil
}
func (*recordsHTTPRepository) DeleteRecord(context.Context, string, definitionmodel.ObjectSchema, string) error {
	return nil
}
func (*recordsHTTPRepository) CommitRecordMutation(context.Context, string, transactionmodel.RecordMutationCommit) error {
	return nil
}
func (*recordsHTTPRepository) CommitRecordMutationBatch(context.Context, string, []transactionmodel.RecordMutationCommit) error {
	return nil
}
func (*recordsHTTPRepository) UniqueExists(context.Context, string, string, string, string, any) (bool, error) {
	return false, nil
}

func recordsHTTPApplication(repository *recordsHTTPRepository) *recordapplication.RecordApplicationService {
	return recordsHTTPApplicationWithExecutionStore(repository, recordsHTTPMutationExecutionStore{}, false)
}

func recordsHTTPApplicationWithExecutionStore(repository *recordsHTTPRepository, executionStore recordsHTTPMutationExecutionStore, localized bool) *recordapplication.RecordApplicationService {
	nameConfig := map[string]any(nil)
	if localized {
		nameConfig = map[string]any{"localized": true}
	}
	object := definitionmodel.ObjectSchema{Key: "customer", Name: "Customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Name: "Name", Type: "text", Config: nameConfig}, {Key: "owner", Name: "Owner", Type: "user"}}}
	related := definitionmodel.ObjectSchema{Key: "order", Name: "Order", Fields: []definitionmodel.FieldSchema{{Key: "customer_id", Name: "Customer", Type: "relation", Config: map[string]any{"object_key": "customer"}}}}
	objects := map[string]definitionmodel.ObjectSchema{object.Key: object, related.Key: related}
	objectForKey := func(_ context.Context, key string) (definitionmodel.ObjectSchema, bool) {
		value, ok := objects[key]
		return value, ok
	}
	queryPolicy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{
		Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{object, related} },
	})
	pipeline := pipelineapplication.NewPipelineApplicationService(pipelineapplication.PipelineDependencies{
		Repository: repository,
		Object:     objectForKey,
		CanAccess:  queryPolicy.CanAccessRecord,
	})
	validation := recordservice.NewRecordValidationDomainService(recordservice.RecordValidationDependencies{
		Repository: repository,
		Object:     objectForKey,
		CanAccess:  queryPolicy.CanAccessRecord,
	})
	dependencies := recordapplication.RecordApplicationDependencies{
		Repository:                repository,
		QueryPolicy:               queryPolicy,
		Pipeline:                  pipeline,
		Validation:                validation,
		SchemaMap:                 func() map[string]definitionmodel.ObjectSchema { return objects },
		IdentityProfileExtensions: func() []profilebindingmodel.Binding { return nil },
		RunBefore: func(context.Context, string, string, string, map[string]any, map[string]any, map[string]any, principalmodel.Principal) error {
			return nil
		},
		PrepareWorkflow: func(context.Context, string, recordmodel.Record, map[string]any, principalmodel.Principal, string) ([]workflowmodel.WorkflowExecution, error) {
			return nil, nil
		},
	}
	if localized {
		dependencies.RecordMutationExecution = recordruntime.NewRecordMutationExecutionRuntime(executionStore)
	}
	return recordapplication.NewRecordApplicationService(dependencies)
}

func recordsHTTPBusinessProfileApplication(repository *recordsHTTPRepository) *recordapplication.RecordApplicationService {
	object := definitionmodel.ObjectSchema{
		Key: "customer", Name: "Customer",
		Fields: []definitionmodel.FieldSchema{
			{Key: "name", Name: "Name", Type: "text"},
			{Key: "status", Name: "Status", Type: "text"},
		},
	}
	objectForKey := func(_ context.Context, key string) (definitionmodel.ObjectSchema, bool) {
		return object, key == object.Key
	}
	queryPolicy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{
		Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{object} },
	})
	pipeline := pipelineapplication.NewPipelineApplicationService(pipelineapplication.PipelineDependencies{
		Repository: repository,
		Object:     objectForKey,
		CanAccess:  queryPolicy.CanAccessRecord,
	})
	validation := recordservice.NewRecordValidationDomainService(recordservice.RecordValidationDependencies{
		Repository: repository,
		Object:     objectForKey,
		CanAccess:  queryPolicy.CanAccessRecord,
	})
	return recordapplication.NewRecordApplicationService(recordapplication.RecordApplicationDependencies{
		Repository:  repository,
		QueryPolicy: queryPolicy,
		Pipeline:    pipeline,
		Validation:  validation,
		SchemaMap: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{object.Key: object}
		},
		IdentityProfileExtensions: func() []profilebindingmodel.Binding {
			return []profilebindingmodel.Binding{{
				ObjectKey: "customer",
				BusinessIdentity: profilebindingmodel.BusinessIdentityBinding{
					StatusField: "status", ActiveStatusValues: []string{"active"},
				},
			}}
		},
		RunBefore: func(context.Context, string, string, string, map[string]any, map[string]any, map[string]any, principalmodel.Principal) error {
			return nil
		},
		PrepareWorkflow: func(context.Context, string, recordmodel.Record, map[string]any, principalmodel.Principal, string) ([]workflowmodel.WorkflowExecution, error) {
			return nil, nil
		},
		RecordMutationExecution: recordruntime.NewRecordMutationExecutionRuntime(recordsHTTPMutationExecutionStore{}),
	})
}

func TestCreateRecordHTTPAcceptsLocalizedBusinessValues(t *testing.T) {
	repository := &recordsHTTPRepository{}
	var commit transactionmodel.RecordMutationCommit
	application := recordsHTTPApplicationWithExecutionStore(repository, recordsHTTPMutationExecutionStore{commit: &commit}, true)
	handler, serviceErr := recordsHandlerForTest(recordsHTTPPrincipal())
	handler.UseQueries(application)

	w := httptest.NewRecorder()
	request := recordsRequest(http.MethodPost, "/records", `{"data":{"name":"Default"},"translations":{"en-US":{"name":"Product"},"zh-CN":{"name":"商品"}}}`, map[string]string{"objectKey": "customer"})
	request.Header.Set("Idempotency-Key", "localized-create-1")
	handler.createRecord(w, request)
	if w.Code != http.StatusCreated || *serviceErr != nil {
		t.Fatalf("status=%d err=%v body=%s", w.Code, *serviceErr, w.Body.String())
	}
	if len(commit.LocalizedValues) != 2 || commit.LocalizedValues[0].Locale != "en-US" || commit.LocalizedValues[1].Locale != "zh-CN" {
		t.Fatalf("localized HTTP commit=%+v", commit.LocalizedValues)
	}
}

func TestRecordsQueryExportReferenceAndImportPreviewHandlers(t *testing.T) {
	repository := &recordsHTTPRepository{
		page:   recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "one", Data: map[string]any{"name": "Ada"}}}, Page: 1, PageSize: 20, Total: 1},
		record: recordmodel.Record{ID: "one", Data: map[string]any{"name": "Ada"}},
		found:  true,
	}
	application := recordsHTTPApplication(repository)
	handler, serviceErr := recordsHandlerForTest(recordsHTTPPrincipal())
	handler.UseQueries(application)

	w := httptest.NewRecorder()
	handler.listRecords(w, recordsRequest("GET", "/records?page=2&page_size=4&search=+Ada+&sort=-name&locale=zh-CN&fallback_locale=en-US", "", map[string]string{"objectKey": " customer "}))
	if w.Code != http.StatusOK || *serviceErr != nil || repository.lastQuery.Page != 2 || repository.lastQuery.PageSize != 4 || repository.lastQuery.Search != "Ada" || repository.lastQuery.Locale != "zh-CN" || repository.lastQuery.FallbackLocale != "en-US" {
		t.Fatalf("list status=%d err=%v query=%#v", w.Code, *serviceErr, repository.lastQuery)
	}

	w = httptest.NewRecorder()
	handler.getRecord(w, recordsRequest("GET", "/record", "", map[string]string{"objectKey": " customer ", "recordID": " one "}))
	if w.Code != http.StatusOK || *serviceErr != nil {
		t.Fatalf("get status=%d err=%v", w.Code, *serviceErr)
	}

	w = httptest.NewRecorder()
	handler.recordReferences(w, recordsRequest("GET", "/references", "", map[string]string{"objectKey": " customer ", "recordID": " one "}))
	if w.Code != http.StatusOK || *serviceErr != nil {
		t.Fatalf("references status=%d err=%v", w.Code, *serviceErr)
	}

	w = httptest.NewRecorder()
	handler.exportRecords(w, recordsRequest("GET", "/export?fields=+name+&reason=+review+&masking_policy=+default+&filter_summary=+all+&page=3&locale=zh-CN&fallback_locale=en-US", "", map[string]string{"objectKey": " customer "}))
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), "text/csv") || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment; filename=") || !strings.Contains(w.Body.String(), "Ada") {
		t.Fatalf("export status=%d headers=%v body=%q err=%v", w.Code, w.Header(), w.Body.String(), *serviceErr)
	}
	if repository.lastQuery.Locale != "zh-CN" || repository.lastQuery.FallbackLocale != "en-US" {
		t.Fatalf("export locale query=%#v", repository.lastQuery)
	}

	w = httptest.NewRecorder()
	r := recordsRequest("POST", "/import", "name\nGrace\n", map[string]string{"objectKey": " customer "})
	r.Header.Set("Content-Type", "text/csv; charset=utf-8")
	handler.previewImport(w, r)
	if w.Code != http.StatusOK || *serviceErr != nil {
		t.Fatalf("preview status=%d body=%s err=%v", w.Code, w.Body.String(), *serviceErr)
	}
}

func TestRecordsMutationAndRelatedHandlersForwardApplicationErrors(t *testing.T) {
	repository := &recordsHTTPRepository{}
	application := recordsHTTPApplication(repository)
	handler, serviceErr := recordsHandlerForTest(principalmodel.Principal{})
	handler.UseQueries(application)

	tests := []struct {
		name string
		call func(http.ResponseWriter)
	}{
		{name: "list", call: func(w http.ResponseWriter) {
			handler.listRecords(w, recordsRequest("GET", "/records", "", map[string]string{"objectKey": "customer"}))
		}},
		{name: "references", call: func(w http.ResponseWriter) {
			handler.recordReferences(w, recordsRequest("GET", "/references", "", map[string]string{"objectKey": "customer", "recordID": "one"}))
		}},
		{name: "related", call: func(w http.ResponseWriter) {
			handler.relatedRecords(w, recordsRequest("GET", "/related?page=2&page_size=3&field=+customer_id+", "", map[string]string{"objectKey": "customer", "recordID": "one", "relatedObjectKey": "order"}))
		}},
		{name: "create", call: func(w http.ResponseWriter) {
			r := recordsRequest("POST", "/records", `{"data":{"name":"Ada"}}`, map[string]string{"objectKey": "customer"})
			r.Header.Set("Idempotency-Key", "create")
			handler.createRecord(w, r)
		}},
		{name: "update", call: func(w http.ResponseWriter) {
			r := recordsRequest("PATCH", "/record", `{"data":{"name":"Ada"}}`, map[string]string{"objectKey": "customer", "recordID": "one"})
			r.Header.Set("If-Match", `"version"`)
			r.Header.Set("Idempotency-Key", "update")
			handler.updateRecord(w, r)
		}},
		{name: "delete", call: func(w http.ResponseWriter) {
			r := recordsRequest("DELETE", "/record", "", map[string]string{"objectKey": "customer", "recordID": "one"})
			r.Header.Set("If-Match", `"version"`)
			r.Header.Set("Idempotency-Key", "delete")
			handler.deleteRecord(w, r)
		}},
		{name: "preview", call: func(w http.ResponseWriter) {
			r := recordsRequest("POST", "/preview", "name\nAda\n", map[string]string{"objectKey": "customer"})
			r.Header.Set("Content-Type", "text/csv")
			handler.previewImport(w, r)
		}},
		{name: "apply", call: func(w http.ResponseWriter) {
			r := recordsRequest("POST", "/apply", "name\nAda\n", map[string]string{"objectKey": "customer"})
			r.Header.Set("Content-Type", "text/csv")
			r.Header.Set("Idempotency-Key", "apply")
			handler.applyImport(w, r)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			*serviceErr = nil
			w := httptest.NewRecorder()
			test.call(w)
			if w.Code != 599 || *serviceErr == nil {
				t.Fatalf("status=%d err=%v body=%s", w.Code, *serviceErr, w.Body.String())
			}
		})
	}
}

func TestRecordsInputGuardsRejectMissingKeysAndInvalidBodies(t *testing.T) {
	handler, serviceErr := recordsHandlerForTest(recordsHTTPPrincipal())
	handler.queries = recordsHTTPApplication(&recordsHTTPRepository{})

	w := httptest.NewRecorder()
	handler.createRecord(w, recordsRequest("POST", "/records", `{"data":{}}`, map[string]string{"objectKey": "customer"}))
	if w.Code != 599 || *serviceErr == nil {
		t.Fatalf("missing create key status=%d err=%v", w.Code, *serviceErr)
	}
	*serviceErr = nil
	w = httptest.NewRecorder()
	r := recordsRequest("POST", "/apply", "name\nAda\n", map[string]string{"objectKey": "customer"})
	r.Header.Set("Content-Type", "text/csv")
	handler.applyImport(w, r)
	if w.Code != 599 || *serviceErr == nil {
		t.Fatalf("missing import key status=%d err=%v", w.Code, *serviceErr)
	}
	*serviceErr = nil
	w = httptest.NewRecorder()
	handler.updateRecord(w, recordsRequest("PATCH", "/record", `{`, map[string]string{}))
	if w.Code != http.StatusBadRequest || *serviceErr == nil {
		t.Fatalf("invalid update status=%d err=%v", w.Code, *serviceErr)
	}

	*serviceErr = nil
	repository := &recordsHTTPRepository{
		found:  true,
		record: recordmodel.Record{ID: "one", UpdatedAt: "version-1", Data: map[string]any{"name": "Before"}},
	}
	handler.queries = recordsHTTPApplication(repository)
	w = httptest.NewRecorder()
	r = recordsRequest("PATCH", "/record", `{"data":{"name":"After"}}`, map[string]string{"objectKey": "customer", "recordID": "one"})
	r.Header.Set("Idempotency-Key", "update-1")
	handler.updateRecord(w, r)
	if w.Code != 599 || apperror.CodeOf(*serviceErr) != idempotency.ErrorCodeReceiptUnavailable || repository.getCalls != 0 {
		t.Fatalf("idempotent update status=%d err=%v gets=%d", w.Code, *serviceErr, repository.getCalls)
	}
}

func TestBusinessProfileLifecycleHandlersRejectInvalidBodiesAndForwardApplicationErrors(t *testing.T) {
	handler, serviceErr := recordsHandlerForTest(recordsHTTPPrincipal())
	handler.queries = recordsHTTPApplication(&recordsHTTPRepository{})

	for _, test := range []struct {
		name string
		call func(http.ResponseWriter, *http.Request)
		body string
	}{
		{name: "deactivate decode", call: handler.deactivateBusinessProfile, body: `{`},
		{name: "reactivate decode", call: handler.reactivateBusinessProfile, body: `{`},
	} {
		t.Run(test.name, func(t *testing.T) {
			*serviceErr = nil
			w := httptest.NewRecorder()
			test.call(w, recordsRequest(http.MethodPost, "/profile", test.body, map[string]string{
				"objectKey": "customer",
				"recordID":  "one",
			}))
			if w.Code != http.StatusBadRequest || *serviceErr == nil {
				t.Fatalf("status=%d err=%v", w.Code, *serviceErr)
			}
		})
	}

	for _, test := range []struct {
		name string
		call func(http.ResponseWriter, *http.Request)
		body string
	}{
		{name: "deactivate service", call: handler.deactivateBusinessProfile, body: `{"inactive_status":"inactive"}`},
		{name: "reactivate service", call: handler.reactivateBusinessProfile, body: `{"active_status":"active"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			*serviceErr = nil
			w := httptest.NewRecorder()
			request := recordsRequest(http.MethodPost, "/profile", test.body, map[string]string{
				"objectKey": "customer",
				"recordID":  "one",
			})
			request.Header.Set("Idempotency-Key", test.name)
			test.call(w, request)
			if w.Code != 599 || apperror.CodeOf(*serviceErr) != "backend.identity.profile_binding_definition_not_found" {
				t.Fatalf("status=%d err=%v", w.Code, *serviceErr)
			}
		})
	}
}

func TestBusinessProfileLifecycleHandlersReturnUpdatedRecord(t *testing.T) {
	repository := &recordsHTTPRepository{
		record: recordmodel.Record{
			ID: "one", UpdatedAt: "version-1",
			Data: map[string]any{"name": "Ada", "status": "active"},
		},
		found: true,
	}
	handler, serviceErr := recordsHandlerForTest(recordsHTTPPrincipal())
	handler.queries = recordsHTTPBusinessProfileApplication(repository)

	for _, test := range []struct {
		name, endpointIdentity string
		call                   func(http.ResponseWriter, *http.Request)
		body                   string
	}{
		{name: "deactivate", endpointIdentity: "POST /objects/{objectKey}/records/{recordID}/deactivate-profile", call: handler.deactivateBusinessProfile, body: `{"inactive_status":"inactive","expected_updated_at":"version-1","reason":" review "}`},
		{name: "reactivate", endpointIdentity: "POST /objects/{objectKey}/records/{recordID}/reactivate-profile", call: handler.reactivateBusinessProfile, body: `{"active_status":"active","expected_updated_at":"version-1","reason":" restore "}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			*serviceErr = nil
			w := httptest.NewRecorder()
			request := recordsRequest(http.MethodPost, "/profile", test.body, map[string]string{
				"objectKey": "customer",
				"recordID":  "one",
			})
			request.Header.Set("Idempotency-Key", test.name)
			request = recordsAuthorizedEndpointRequest(t, request, test.endpointIdentity)
			test.call(w, request)
			if w.Code != http.StatusOK || *serviceErr != nil || !strings.Contains(w.Body.String(), `"record"`) {
				t.Fatalf("status=%d err=%v body=%s", w.Code, *serviceErr, w.Body.String())
			}
		})
	}
}

func TestRecordHandlersForwardRepositoryFailures(t *testing.T) {
	want := errors.New("repository unavailable")
	repository := &recordsHTTPRepository{err: want}
	handler, serviceErr := recordsHandlerForTest(recordsHTTPPrincipal())
	handler.queries = recordsHTTPApplication(repository)
	for _, call := range []func(http.ResponseWriter, *http.Request){handler.listRecords, handler.getRecord, handler.recordReferences, handler.exportRecords} {
		*serviceErr = nil
		w := httptest.NewRecorder()
		r := recordsRequest("GET", "/record", "", map[string]string{"objectKey": "customer", "recordID": "one"})
		call(w, r)
		if w.Code != 599 || *serviceErr == nil {
			t.Fatalf("status=%d err=%v", w.Code, *serviceErr)
		}
	}
}
