package runtime

import (
	"errors"
	"reflect"
	"testing"

	apperror "github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func assertRecordAppError(t *testing.T, err error, kind apperror.ErrorKind, code string, params map[string]string) {
	t.Helper()
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Kind != kind || appErr.Code != code || !reflect.DeepEqual(appErr.Params, params) {
		t.Fatalf("error = %#v, want kind=%s code=%s params=%#v", err, kind, code, params)
	}
}
