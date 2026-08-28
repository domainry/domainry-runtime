package records

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

func TestRecordsActionIdempotencyKeyHeaderCompatibility(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/objects/order/actions/create/run", nil)
	request.Header.Set("Idempotency-Key", " key-1 ")
	if key, err := recordsActionIdempotencyKey(request, "key-1"); err != nil || key != "key-1" {
		t.Fatalf("matching key=%q err=%v", key, err)
	}
	if _, err := recordsActionIdempotencyKey(request, "different"); apperror.CodeOf(err) != idempotency.ErrorCodeKeyReused {
		t.Fatalf("mismatch error=%v", err)
	}
	request.Header.Del("Idempotency-Key")
	if key, err := recordsActionIdempotencyKey(request, " payload-key "); err != nil || key != "payload-key" {
		t.Fatalf("payload compatibility key=%q err=%v", key, err)
	}
	if _, err := recordsActionIdempotencyKey(request, ""); apperror.CodeOf(err) != idempotency.ErrorCodeMissingKey {
		t.Fatalf("missing key error=%v", err)
	}
}

func TestRecordsActionIdempotencyResponseHeaders(t *testing.T) {
	var captured error
	handler := &RecordsHandler{writeServiceError: func(_ http.ResponseWriter, _ *http.Request, errorValue error) { captured = errorValue }}
	writer := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/objects/order/actions/create/run", nil)
	inProgress := apperror.New(apperror.KindConflict, idempotency.ErrorCodeInProgress, errors.New("processing"), map[string]string{"retry_after": "7"})
	handler.writeActionServiceError(writer, request, inProgress)
	if !errors.Is(captured, inProgress) || writer.Header().Get("Retry-After") != "7" {
		t.Fatalf("captured=%v retry-after=%q", captured, writer.Header().Get("Retry-After"))
	}
	recordsMarkActionReplay(writer, "backend.action.idempotent_replay")
	if writer.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay header=%q", writer.Header().Get("Idempotency-Replayed"))
	}
}
