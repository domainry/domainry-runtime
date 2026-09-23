package runtimeengine

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestHTTPStatusAndErrorCodePreservePublicRuntimeFailure(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", NewError(ErrorConflict, "backend.action.conflict", map[string]string{"record": "one"}, errors.New("conflict")))
	if status := HTTPStatus(err); status != http.StatusConflict {
		t.Fatalf("status=%d", status)
	}
	if code := ErrorCode(err); code != "backend.action.conflict" {
		t.Fatalf("code=%q", code)
	}
	if status := HTTPStatus(errors.New("plain")); status != http.StatusInternalServerError {
		t.Fatalf("plain status=%d", status)
	}
}
