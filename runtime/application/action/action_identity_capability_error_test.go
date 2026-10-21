package action

import (
	"errors"
	"fmt"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

func TestNormalizeIdentityCapabilityErrorKeepsTheModuleClassification(t *testing.T) {
	// The embedded Identity module refuses a duplicate login email as a bad
	// request with the email in params; a Handler delivery must surface it as
	// that refusal, never as a 500 whose code happens to be the same.
	refusal := &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.identity.user_email_exists", Params: map[string]string{"email": "a@example.test"}}
	err := normalizeIdentityCapabilityError(fmt.Errorf("deliver: %w", refusal), "identity.handler_delivery_failed")
	var got *apperror.AppError
	if !errors.As(err, &got) || got.Kind != apperror.KindBadRequest || got.Code != "backend.identity.user_email_exists" || got.Params["email"] != "a@example.test" {
		t.Fatalf("classified refusal must keep kind, code and params: %+v", err)
	}
	for _, kind := range []apperror.ErrorKind{apperror.KindForbidden, apperror.KindConflict, apperror.KindNotFound} {
		err := normalizeIdentityCapabilityError(&apperror.AppError{Kind: kind, Code: "backend.identity.some_refusal"}, "identity.handler_delivery_failed")
		if !errors.As(err, &got) || got.Kind != kind {
			t.Fatalf("kind %s must survive normalization: %+v", kind, err)
		}
	}
	// An internal error from the module is still a Runtime failure with its code.
	err = normalizeIdentityCapabilityError(&apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal"}, "identity.handler_delivery_failed")
	if !errors.As(err, &got) || got.Kind != apperror.KindInternal {
		t.Fatalf("internal stays internal: %+v", err)
	}
}

func TestNormalizeIdentityCapabilityErrorUsesOwnerClassificationWithoutCodeGuessing(t *testing.T) {
	var got *apperror.AppError
	err := normalizeIdentityCapabilityError(&identitysdk.Error{Class: identitysdk.ErrorClassConflict, Code: "identity.user_email_exists"}, "identity.handler_delivery_failed")
	if !errors.As(err, &got) || got.Kind != apperror.KindConflict || got.Code != "identity.user_email_exists" {
		t.Fatalf("an owner-classified conflict must remain a conflict: %+v", err)
	}
	err = normalizeIdentityCapabilityError(&identitysdk.Error{Code: "identity.login_already_taken"}, "identity.handler_delivery_failed")
	if !errors.As(err, &got) || got.Kind != apperror.KindInternal {
		t.Fatalf("an unclassified code must not be guessed from its text: %+v", err)
	}
	err = normalizeIdentityCapabilityError(&identitysdk.Error{Class: identitysdk.ErrorClassUnavailable, Code: "identity.handler_delivery_unavailable"}, "identity.handler_delivery_failed")
	if !errors.As(err, &got) || got.Kind != apperror.KindUnavailable {
		t.Fatalf("an owner-classified unavailable failure must remain unavailable: %+v", err)
	}
}
