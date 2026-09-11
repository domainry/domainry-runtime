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
