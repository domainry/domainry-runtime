package service

import (
	"testing"

	apperror "github.com/domainry/domainry-foundation/apperror"
)

// A caller that writes several relation fields in one payload gets one refusal.
// Without the field name it cannot tell which target it may not read, and the
// permission to change is on that target object, not on the one being written.
func TestRelationOutsideScopeNamesTheFieldAndObject(t *testing.T) {
	err := recordServiceError(apperror.KindForbidden, "backend.record.outside_scope", nil, "field", "member_profile", "object", "member_profile", "record_id", "member_profile_7")
	coded, ok := err.(*apperror.AppError)
	if !ok {
		t.Fatalf("error type = %T", err)
	}
	if coded.Code != "backend.record.outside_scope" {
		t.Fatalf("code = %s", coded.Code)
	}
	for key, want := range map[string]string{"field": "member_profile", "object": "member_profile", "record_id": "member_profile_7"} {
		if coded.Params[key] != want {
			t.Errorf("params[%s] = %q, want %q", key, coded.Params[key], want)
		}
	}
}
