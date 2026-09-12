package service

import (
	"errors"
	"fmt"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
)

// TestRecordInternalErrorKeepsARepositoryClientError pins the classification
// boundary: the store's keyset refusal is a client error with a stable code,
// and the domain service must not relabel it backend.internal. Before this
// test every repository error, classified or not, became a 500 whose only
// detail was the operation name.
func TestRecordInternalErrorKeepsARepositoryClientError(t *testing.T) {
	refusal := &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.record.pagination_cursor_required", Params: map[string]string{"page": "2"}}
	got := recordInternalError("list records", refusal)
	var classified *apperror.AppError
	if !errors.As(got, &classified) || classified.Kind != apperror.KindBadRequest || classified.Code != "backend.record.pagination_cursor_required" {
		t.Fatalf("classified repository error was relabelled: %#v", got)
	}
	wrapped := recordInternalError("list records", fmt.Errorf("outer: %w", refusal))
	if !errors.As(wrapped, &classified) || classified.Kind != apperror.KindBadRequest {
		t.Fatalf("wrapped classified error was relabelled: %#v", wrapped)
	}
	plain := recordInternalError("list records", errors.New("sql: connection reset"))
	if !errors.As(plain, &classified) || classified.Kind != apperror.KindInternal || classified.Code != "backend.internal" || classified.Params["operation"] != "list records" {
		t.Fatalf("plain error must still be backend.internal with the operation: %#v", plain)
	}
	internal := recordInternalError("get record", &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal"})
	if !errors.As(internal, &classified) || classified.Params["operation"] != "get record" {
		t.Fatalf("an internal repository error still gains the operation: %#v", internal)
	}
}

// A leaf CodedError is a client-facing refusal, not a server fault: an unknown
// filter key is a documented 400 that names the key, and it used to reach the
// caller as a 500 with the key discarded.
func TestRecordInternalErrorClassifiesLeafCodedErrorsAsBadRequest(t *testing.T) {
	coded := &apperror.CodedError{Code: "backend.validation.filter_field_unknown", Params: map[string]string{"field": "not_a_field", "object_key": "member_profile"}}
	err := recordInternalError("list records", coded)
	var classified *apperror.AppError
	if !errors.As(err, &classified) || classified.Kind != apperror.KindBadRequest {
		t.Fatalf("kind = %v, want %v (err=%v)", apperror.KindOf(err), apperror.KindBadRequest, err)
	}
	if classified.Code != "backend.validation.filter_field_unknown" || classified.Params["field"] != "not_a_field" {
		t.Fatalf("code=%q params=%v", classified.Code, classified.Params)
	}
	// An unclassified failure is still an internal one.
	if apperror.KindOf(recordInternalError("list records", errors.New("boom"))) != apperror.KindInternal {
		t.Fatal("an unclassified repository failure must stay internal")
	}
}
