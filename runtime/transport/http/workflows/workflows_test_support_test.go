package workflows

import (
	"bytes"
	"net/http"
	"net/http/httptest"
)

func workflowHTTPRequest(method, target, body string, pathValues map[string]string) (*httptest.ResponseRecorder, *http.Request) {
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	for key, value := range pathValues {
		request.SetPathValue(key, value)
	}
	return httptest.NewRecorder(), request
}
