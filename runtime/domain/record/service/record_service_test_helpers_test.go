package service

import (
	"errors"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func assertRecordAppError(t *testing.T, err error, kind apperror.ErrorKind, code string, params map[string]string) {
	t.Helper()
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected AppError, got %T: %v", err, err)
	}
	if appErr.Kind != kind || appErr.Code != code {
		t.Fatalf("error = kind %q code %q, want kind %q code %q", appErr.Kind, appErr.Code, kind, code)
	}
	for key, value := range params {
		if appErr.Params[key] != value {
			t.Fatalf("error param %q = %q, want %q", key, appErr.Params[key], value)
		}
	}
}
