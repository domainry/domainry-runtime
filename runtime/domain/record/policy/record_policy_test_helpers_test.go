package policy

import (
	"errors"
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
	var coded *apperror.CodedError
	if !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error=%v code=%q, want %q", err, recordPolicyValidationCode(coded), code)
	}
}

func recordPolicyValidationCode(err *apperror.CodedError) string {
	if err == nil {
		return ""
	}
	return err.Code
}
