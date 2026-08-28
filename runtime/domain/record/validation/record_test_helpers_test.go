package validation

import (
	"errors"
	"reflect"
	"testing"

	apperror "github.com/domainry/domainry-foundation/apperror"
)

func assertValidationCode(t *testing.T, err error, code string) {
	t.Helper()
	if code == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	var validation *apperror.CodedError
	if !errors.As(err, &validation) || validation.Code != code {
		t.Fatalf("error=%v code=%q, want %q", err, validationCodeOf(validation), code)
	}
}

func validationCodeOf(err *apperror.CodedError) string {
	if err == nil {
		return ""
	}
	return err.Code
}

func assertRecordAppError(t *testing.T, err error, kind apperror.ErrorKind, code string, params map[string]string) {
	t.Helper()
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Kind != kind || appErr.Code != code || !reflect.DeepEqual(appErr.Params, params) {
		t.Fatalf("error = %#v, want kind=%s code=%s params=%#v", err, kind, code, params)
	}
}
