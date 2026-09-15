package http

// These tests guard shared context-error translation at the HTTP boundary.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
)

func TestWriteServiceErrorMapsWrappedContextErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "cancelled", err: context.Canceled, status: 499, code: "backend.request_cancelled"},
		{name: "deadline", err: context.DeadlineExceeded, status: http.StatusGatewayTimeout, code: "backend.deadline_exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/records/account", nil)
			wrapped := &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal", Err: tc.err}
			writeServiceError(response, request, wrapped)
			if response.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tc.status, response.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["code"] != tc.code || body["message_key"] != tc.code {
				t.Fatalf("unexpected response contract: %#v", body)
			}
		})
	}
}

func TestWriteServiceErrorMapsExpiredAuthorizationToUnauthorized(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/records/account", nil)
	writeServiceError(response, request, apperror.New(apperror.KindForbidden, "auth.session_expired", nil, nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusUnauthorized, response.Body.String())
	}
}
