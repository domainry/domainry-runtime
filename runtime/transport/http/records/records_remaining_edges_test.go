package records

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordsCRUDSuccessReplayAndNilPatch(t *testing.T) {
	fixture := newRecordBatchHTTPFixture(t)
	created := fixture.call(http.MethodPost, "/objects/customer/records", `{"data":{"name":"Ada"}}`, map[string]string{"Idempotency-Key": "record-create-edge"})
	var record recordmodel.Record
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &record) != nil || record.ID == "" {
		t.Fatalf("create status=%d record=%+v body=%s", created.Code, record, created.Body.String())
	}
	replayed := fixture.call(http.MethodPost, "/objects/customer/records", `{"data":{"name":"Ada"}}`, map[string]string{"Idempotency-Key": "record-create-edge"})
	if replayed.Code != http.StatusCreated || replayed.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay status=%d headers=%v body=%s", replayed.Code, replayed.Header(), replayed.Body.String())
	}

	missingUpdateKey := fixture.call(http.MethodPatch, "/objects/customer/records/"+record.ID, `{"data":{"name":"Grace","expected_updated_at":"`+record.UpdatedAt+`"}}`, nil)
	if missingUpdateKey.Code != http.StatusBadRequest || !strings.Contains(missingUpdateKey.Body.String(), idempotency.ErrorCodeMissingKey) {
		t.Fatalf("missing update key status=%d body=%s", missingUpdateKey.Code, missingUpdateKey.Body.String())
	}
	updated := fixture.call(http.MethodPatch, "/objects/customer/records/"+record.ID, `{"data":{"name":"Grace","expected_updated_at":"`+record.UpdatedAt+`"}}`, map[string]string{"Idempotency-Key": "record-update-edge"})
	if updated.Code != http.StatusOK || json.Unmarshal(updated.Body.Bytes(), &record) != nil || record.Data["name"] != "Grace" {
		t.Fatalf("update status=%d record=%+v body=%s", updated.Code, record, updated.Body.String())
	}
	deleted := fixture.call(http.MethodDelete, "/objects/customer/records/"+record.ID+"?expected_updated_at="+record.UpdatedAt, "", nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}

	handler, serviceErr := recordsHandlerForTest(principalmodel.Principal{})
	handler.queries = recordsHTTPApplication(&recordsHTTPRepository{})
	request := recordsRequest(http.MethodPatch, "/record", `{"data":null}`, map[string]string{"objectKey": "customer", "recordID": "one"})
	request.Header.Set("If-Match", `"version"`)
	request.Header.Set("Idempotency-Key", "record-nil-patch-edge")
	response := httptest.NewRecorder()
	handler.updateRecord(response, request)
	if response.Code != 599 || *serviceErr == nil {
		t.Fatalf("nil patch status=%d error=%v", response.Code, *serviceErr)
	}
	*serviceErr = nil
	response = httptest.NewRecorder()
	handler.createRecord(response, recordsRequest(http.MethodPost, "/record", `{`, nil))
	if response.Code != http.StatusBadRequest || *serviceErr == nil {
		t.Fatalf("invalid create status=%d error=%v", response.Code, *serviceErr)
	}
	*serviceErr = nil
	response = httptest.NewRecorder()
	request = recordsRequest(http.MethodPatch, "/record", `{"data":{}}`, map[string]string{"objectKey": "customer", "recordID": "one"})
	request.Header.Set("Idempotency-Key", "record-no-match-edge")
	handler.updateRecord(response, request)
	if response.Code != 599 || *serviceErr == nil {
		t.Fatalf("update without match status=%d error=%v", response.Code, *serviceErr)
	}
}

func TestRecordCreateMissingKeyUsesOperationsEnvelopeContract(t *testing.T) {
	fixture := newRecordBatchHTTPFixture(t)
	response := fixture.call(http.MethodPost, "/objects/customer/records", `{"data":{"name":"Ada"}}`, nil)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "operations.idempotency_contract_required") {
		t.Fatalf("missing create key status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRelatedRecordsSuccess(t *testing.T) {
	repository := &recordsHTTPRepository{
		page:   recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "order-one", Data: map[string]any{"customer_id": "one"}}}},
		record: recordmodel.Record{ID: "one", Data: map[string]any{"name": "Ada"}},
		found:  true,
	}
	handler, serviceErr := recordsHandlerForTest(recordsHTTPPrincipal())
	handler.queries = recordsHTTPApplication(repository)
	response := httptest.NewRecorder()
	handler.relatedRecords(response, recordsRequest(http.MethodGet, "/related?field=customer_id", "", map[string]string{"objectKey": "customer", "recordID": "one", "relatedObjectKey": "order"}))
	if response.Code != http.StatusOK || *serviceErr != nil || len(repository.lastQuery.Filters) != 1 || repository.lastQuery.Filters["customer_id"] != "one" {
		t.Fatalf("status=%d error=%v query=%+v", response.Code, *serviceErr, repository.lastQuery)
	}
}

func TestRecordsActionHandlersCoverEmptyBodiesDecodeFailuresAndKeyMismatch(t *testing.T) {
	var objectInvocation, recordInvocation actionmodel.ActionInvocation
	handler, serviceErr := recordsHandlerForTest(recordsHTTPPrincipal())
	handler.actions = recordsActionService([]definitionmodel.ActionSchema{
		{Key: "approve", ObjectKey: "customer", Kind: "record_update", RequiresPermission: "workspace.admin"},
		{Key: "approve_object", ObjectKey: "customer", Kind: "object_operation", RequiresPermission: "workspace.admin"},
	}, func(_ context.Context, invocation actionmodel.ActionInvocation, action definitionmodel.ActionSchema, _ map[string]any) (actionapplication.ActionExecutionResult, error) {
		if invocation.RecordID == "" {
			objectInvocation = invocation
			result := actionmodel.ActionObjectResult{ActionKey: action.Key, ObjectKey: action.ObjectKey, Status: "success"}
			return actionapplication.ActionExecutionResult{Object: &result}, nil
		}
		recordInvocation = invocation
		result := actionmodel.ActionResult{ActionKey: action.Key, ObjectKey: action.ObjectKey, RecordID: invocation.RecordID}
		return actionapplication.ActionExecutionResult{Record: &result}, nil
	}, nil)

	for name, call := range map[string]func(http.ResponseWriter, *http.Request){
		"object": handler.executeObjectAction,
		"record": handler.executeAction,
	} {
		t.Run(name+" empty body", func(t *testing.T) {
			actionKey := "approve"
			if name == "object" {
				actionKey = "approve_object"
			}
			request := recordsRequest(http.MethodPost, "/action", "", map[string]string{"objectKey": "customer", "recordID": "one", "actionKey": actionKey})
			request.Body = nil
			request.Header.Set("Idempotency-Key", name+"-key")
			response := httptest.NewRecorder()
			call(response, request)
			if response.Code != http.StatusOK || *serviceErr != nil {
				t.Fatalf("status=%d error=%v", response.Code, *serviceErr)
			}
		})
	}
	if objectInvocation.IdempotencyKey != "object-key" || recordInvocation.IdempotencyKey != "record-key" {
		t.Fatalf("object=%+v record=%+v", objectInvocation, recordInvocation)
	}
	if _, exists := recordInvocation.Input["idempotency_key"]; exists {
		t.Fatalf("system idempotency leaked into business data: %+v", recordInvocation.Input)
	}
	bulkEmpty := recordsRequest(http.MethodPost, "/bulk", "", map[string]string{"objectKey": "customer", "actionKey": "approve"})
	bulkEmpty.Body = nil
	bulkEmpty.Header.Set("Idempotency-Key", "bulk-key")
	handler.executeBulkAction(httptest.NewRecorder(), bulkEmpty)

	for name, call := range map[string]func(http.ResponseWriter, *http.Request){"object": handler.executeObjectAction, "record": handler.executeAction} {
		t.Run(name+" decode failure", func(t *testing.T) {
			*serviceErr = nil
			response := httptest.NewRecorder()
			call(response, recordsRequest(http.MethodPost, "/action", `{`, nil))
			if response.Code != http.StatusBadRequest || *serviceErr == nil {
				t.Fatalf("status=%d error=%v", response.Code, *serviceErr)
			}
		})
	}
	for name, call := range map[string]func(http.ResponseWriter, *http.Request){"bulk": handler.executeBulkAction, "record": handler.executeAction} {
		t.Run(name+" key mismatch", func(t *testing.T) {
			*serviceErr = nil
			body := `{"idempotency_key":"payload"}`
			if name == "record" {
				body = `{"data":{"idempotency_key":"payload"}}`
			}
			request := recordsRequest(http.MethodPost, "/action", body, nil)
			request.Header.Set("Idempotency-Key", "header")
			response := httptest.NewRecorder()
			call(response, request)
			wantCode := idempotency.ErrorCodeKeyReused
			if name == "record" {
				wantCode = "backend.validation.unknown_field"
			}
			if response.Code != 599 || apperror.CodeOf(*serviceErr) != wantCode {
				t.Fatalf("status=%d error=%v", response.Code, *serviceErr)
			}
		})
	}
}

func TestRecordsActionHandlersRejectMissingTypedResults(t *testing.T) {
	handler, serviceErr := recordsHandlerForTest(recordsHTTPPrincipal())
	handler.actions = recordsActionService([]definitionmodel.ActionSchema{
		{Key: "approve", ObjectKey: "customer", Kind: "record_update", RequiresPermission: "workspace.admin"},
		{Key: "approve_object", ObjectKey: "customer", Kind: "object_operation", RequiresPermission: "workspace.admin"},
	}, func(context.Context, actionmodel.ActionInvocation, definitionmodel.ActionSchema, map[string]any) (actionapplication.ActionExecutionResult, error) {
		return actionapplication.ActionExecutionResult{}, nil
	}, nil)
	for name, test := range map[string]struct {
		call   func(http.ResponseWriter, *http.Request)
		action string
	}{
		"object": {handler.executeObjectAction, "approve_object"},
		"record": {handler.executeAction, "approve"},
	} {
		t.Run(name, func(t *testing.T) {
			*serviceErr = nil
			request := recordsRequest(http.MethodPost, "/action", "", map[string]string{"objectKey": "customer", "recordID": "one", "actionKey": test.action})
			request.Body = nil
			test.call(httptest.NewRecorder(), request)
			if *serviceErr == nil {
				t.Fatal("missing typed action result was accepted")
			}
		})
	}
}

func TestRecordsImportAndBatchReplayFailureEdges(t *testing.T) {
	fixture := newRecordBatchHTTPFixture(t)
	path := "/objects/customer/records/import/jobs"
	created := fixture.call(http.MethodPost, path, "name\nAda\n", map[string]string{"Content-Type": "text/csv", "Idempotency-Key": "import-replay-edge"})
	if created.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	replayed := fixture.call(http.MethodPost, path, "name\nAda\n", map[string]string{"Content-Type": "text/csv", "Idempotency-Key": "import-replay-edge"})
	if replayed.Code != http.StatusAccepted || replayed.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay status=%d headers=%v body=%s", replayed.Code, replayed.Header(), replayed.Body.String())
	}
	conflict := fixture.call(http.MethodPost, path, "name\nGrace\n", map[string]string{"Content-Type": "text/csv", "Idempotency-Key": "import-replay-edge"})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}

	applyPath := "/objects/customer/records/import/apply"
	applied := fixture.call(http.MethodPost, applyPath, "name\nLin\n", map[string]string{"Content-Type": "text/csv", "Idempotency-Key": "apply-replay-edge"})
	if applied.Code != http.StatusOK {
		t.Fatalf("apply status=%d body=%s", applied.Code, applied.Body.String())
	}
	applyReplay := fixture.call(http.MethodPost, applyPath, "name\nLin\n", map[string]string{"Content-Type": "text/csv", "Idempotency-Key": "apply-replay-edge"})
	if applyReplay.Code != http.StatusOK || applyReplay.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("apply replay status=%d headers=%v body=%s", applyReplay.Code, applyReplay.Header(), applyReplay.Body.String())
	}

	handler, _ := recordsHandlerForTest(recordsHTTPPrincipal())
	for name, call := range map[string]func(http.ResponseWriter, *http.Request){"preview": handler.previewImport, "apply": handler.applyImport, "enqueue": handler.enqueueImportJob} {
		t.Run(name+" read failure", func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/import", nil)
			request.Header.Set("Content-Type", "text/csv")
			request.Body = recordsFailingBody{err: errors.New("read failed")}
			call(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d", response.Code)
			}
		})
	}
}

func TestRecordsExportWriteFailureAndAscendingSort(t *testing.T) {
	repository := &recordsHTTPRepository{page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "one", Data: map[string]any{"name": "Ada"}}}}}
	handler, _ := recordsHandlerForTest(recordsHTTPPrincipal())
	handler.queries = recordsHTTPApplication(repository)
	writer := &recordsWriteProbe{writeErr: errors.New("disconnected")}
	handler.exportRecords(writer, recordsRequest(http.MethodGet, "/export?sort=name", "", map[string]string{"objectKey": "customer"}))
	if writer.writes != 1 {
		t.Fatalf("writes=%d", writer.writes)
	}
	query := parseListQuery(recordsRequest(http.MethodGet, "/records?sort=name", "", nil))
	if len(query.Sort) != 1 || query.Sort[0].Direction != "asc" {
		t.Fatalf("sort=%+v", query.Sort)
	}
	query = parseListQuery(recordsRequest(http.MethodGet, "/records?sort=name+asc", "", nil))
	if len(query.Sort) != 1 || query.Sort[0].Direction != "asc" {
		t.Fatalf("explicit sort=%+v", query.Sort)
	}
}

type recordsWriteProbe struct {
	header   http.Header
	writes   int
	writeErr error
}

func (w *recordsWriteProbe) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (*recordsWriteProbe) WriteHeader(int) {}

func (w *recordsWriteProbe) Write(payload []byte) (int, error) {
	w.writes++
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return len(payload), nil
}

func TestRecordsActionErrorHeaderDefaultsAndNonReplay(t *testing.T) {
	var captured error
	handler := &RecordsHandler{writeServiceError: func(_ http.ResponseWriter, _ *http.Request, err error) { captured = err }}
	response := httptest.NewRecorder()
	err := apperror.New(apperror.KindConflict, idempotency.ErrorCodeInProgress, nil, nil)
	handler.writeActionServiceError(response, httptest.NewRequest(http.MethodPost, "/", nil), err)
	if !errors.Is(captured, err) || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("captured=%v retry-after=%q", captured, response.Header().Get("Retry-After"))
	}
	plain := httptest.NewRecorder()
	recordsMarkActionReplay(plain, "completed")
	if plain.Header().Get("Idempotency-Replayed") != "" {
		t.Fatalf("unexpected replay header=%q", plain.Header().Get("Idempotency-Replayed"))
	}
}
