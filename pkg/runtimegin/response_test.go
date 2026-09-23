package runtimegin

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimeengine"
	"github.com/gin-gonic/gin"
)

func ginContext(method string) (*gin.Context, *httptest.ResponseRecorder) {
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(method, "/api/test", nil)
	return context, response
}

func TestWriteErrorUsesStableRuntimeEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, response := ginContext(http.MethodGet)
	WriteError(context, runtimeengine.NewError(runtimeengine.ErrorConflict, "backend.order.conflict", map[string]string{"order": "one"}, nil))
	var body ErrorBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusConflict || body.Error.Code != "backend.order.conflict" || body.Error.Parameters["order"] != "one" {
		t.Fatalf("status=%d body=%+v", response.Code, body)
	}

	context, response = ginContext(http.MethodGet)
	WriteError(context, errors.New("database secret"))
	if response.Code != http.StatusInternalServerError || response.Body.String() != `{"error":{"code":"backend.internal","message":"backend.internal"}}` {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRequireIdempotencyKey(t *testing.T) {
	context, response := ginContext(http.MethodPost)
	if key, ok := RequireIdempotencyKey(context); ok || key != "" || response.Code != http.StatusBadRequest {
		t.Fatalf("key=%q ok=%v status=%d", key, ok, response.Code)
	}
	context, _ = ginContext(http.MethodPost)
	context.Request.Header.Set("Idempotency-Key", " action-one ")
	if key, ok := RequireIdempotencyKey(context); !ok || key != "action-one" {
		t.Fatalf("key=%q ok=%v", key, ok)
	}
}

type validatedRequest struct {
	Stage string `json:"stage"`
}

func (r *validatedRequest) Validate() error {
	if r.Stage == "won" {
		return Invalid("stage", "use the governed operation")
	}
	return nil
}

func TestBindJSONRunsProjectRequestValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, response := ginContext(http.MethodPost)
	context.Request.Body = io.NopCloser(bytes.NewBufferString(`{"stage":"won"}`))
	context.Request.Header.Set("Content-Type", "application/json")
	var request validatedRequest
	if BindJSON(context, &request) {
		t.Fatal("expected custom validation to reject the request")
	}
	var body ErrorBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusBadRequest || body.Error.Code != "invalid_stage" || body.Error.Message != "use the governed operation" {
		t.Fatalf("status=%d body=%+v", response.Code, body)
	}
}
