package metadata

import (
	"net/http"
	"net/http/httptest"
	"testing"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func TestMetadataAuthoringHeadersBindBuilderIdempotencyAndSchemaHash(t *testing.T) {
	errorCode := ""
	handler := &MetadataHandler{writeError: func(w http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
		errorCode = code
		w.WriteHeader(status)
	}}
	request := httptest.NewRequest(http.MethodPut, "/metadata/definitions/object/order", nil)
	request.Header.Set("Builder-Task-ID", " task-1 ")
	request.Header.Set("Idempotency-Key", " create-order ")
	request.Header.Set("Expected-Schema-Hash", " schema-1 ")
	response := httptest.NewRecorder()
	body := metadatamodel.MetadataDefinitionUpsertRequest{}
	if !handler.applyMetadataAuthoringHeaders(response, request, &body) || errorCode != "" {
		t.Fatalf("code=%s status=%d", errorCode, response.Code)
	}
	if body.ExpectedSchemaHash == nil || *body.ExpectedSchemaHash != "schema-1" || body.SourceKind != "builder_v4" || body.SourceID != "task-1:create-order" {
		t.Fatalf("body=%#v", body)
	}
	if response.Header().Get("Builder-Task-ID") != "task-1" || response.Header().Get("Idempotency-Key") != "create-order" {
		t.Fatalf("headers=%v", response.Header())
	}
}

func TestMetadataAuthoringHeadersRejectIncompleteOrConflictingV4Request(t *testing.T) {
	for _, test := range []struct {
		name, idempotencyKey, expectedHeader, expectedBody, code string
	}{
		{name: "idempotency required", expectedHeader: "schema-1", code: "backend.idempotency.key_required"},
		{name: "schema hash required", idempotencyKey: "key-1", code: "backend.metadata.expected_schema_hash_required"},
		{name: "hash mismatch", idempotencyKey: "key-1", expectedHeader: "schema-2", expectedBody: "schema-1", code: "backend.metadata.expected_schema_hash_mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			actualCode := ""
			handler := &MetadataHandler{writeError: func(w http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
				actualCode = code
				w.WriteHeader(status)
			}}
			request := httptest.NewRequest(http.MethodPut, "/metadata/definitions/object/order", nil)
			request.Header.Set("Builder-Task-ID", "task-1")
			request.Header.Set("Idempotency-Key", test.idempotencyKey)
			request.Header.Set("Expected-Schema-Hash", test.expectedHeader)
			body := metadatamodel.MetadataDefinitionUpsertRequest{}
			if test.expectedBody != "" {
				body.ExpectedSchemaHash = &test.expectedBody
			}
			if handler.applyMetadataAuthoringHeaders(httptest.NewRecorder(), request, &body) || actualCode != test.code {
				t.Fatalf("code=%q want=%q", actualCode, test.code)
			}
		})
	}
}

func TestMetadataAuthoringHeadersRepresentCreateWithExplicitEmptyResourceHash(t *testing.T) {
	handler := &MetadataHandler{writeError: func(w http.ResponseWriter, _ *http.Request, status int, _ string, _ ...string) { w.WriteHeader(status) }}
	request := httptest.NewRequest(http.MethodPut, "/metadata/definitions/object/order", nil)
	request.Header.Set("Builder-Task-ID", "task-1")
	request.Header.Set("Idempotency-Key", "create-order")
	request.Header.Set("Expected-Schema-Hash", "empty")
	body := metadatamodel.MetadataDefinitionUpsertRequest{}
	if !handler.applyMetadataAuthoringHeaders(httptest.NewRecorder(), request, &body) || body.ExpectedSchemaHash == nil || *body.ExpectedSchemaHash != "" {
		t.Fatalf("body=%#v", body)
	}
}

func TestMetadataAuthoringHeadersCoverOptionalAndExistingSourceFields(t *testing.T) {
	handler := &MetadataHandler{
		writeError: func(w http.ResponseWriter, _ *http.Request, status int, _ string, _ ...string) { w.WriteHeader(status) },
		writeJSON:  func(w http.ResponseWriter, status int, _ any) { w.WriteHeader(status) },
	}
	request := httptest.NewRequest(http.MethodPut, "/metadata/definitions/object/order", nil)
	bodyHash := "schema-1"
	body := metadatamodel.MetadataDefinitionUpsertRequest{ExpectedSchemaHash: &bodyHash}
	if !handler.applyMetadataAuthoringHeaders(httptest.NewRecorder(), request, &body) {
		t.Fatal("non-builder request with body hash was rejected")
	}

	request.Header.Set("Builder-Task-ID", "task")
	request.Header.Set("Idempotency-Key", "key")
	request.Header.Set("Expected-Schema-Hash", "schema-1")
	body.SourceKind, body.SourceID = "manifest", "source"
	if !handler.applyMetadataAuthoringHeaders(httptest.NewRecorder(), request, &body) || body.SourceKind != "manifest" || body.SourceID != "source" {
		t.Fatalf("body=%#v", body)
	}

	request.Header.Del("Expected-Schema-Hash")
	if handler.applyMetadataAuthoringHeaders(httptest.NewRecorder(), request, &body) {
		t.Fatal("builder request with a body hash but no expected hash header was accepted")
	}
}
