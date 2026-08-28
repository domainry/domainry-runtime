package records

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type recordsFailingBody struct{ err error }

func (b recordsFailingBody) Read([]byte) (int, error) { return 0, b.err }
func (recordsFailingBody) Close() error               { return nil }

func TestReadCSVPayloadSupportsCSVAndJSONBoundaries(t *testing.T) {
	handler, serviceErr := recordsHandlerForTest(recordsHTTPPrincipal())

	r := recordsRequest("POST", "/import", "name\nAda\n", nil)
	r.Header.Set("Content-Type", "text/csv")
	if raw, ok := handler.readCSVPayload(httptest.NewRecorder(), r); !ok || string(raw) != "name\nAda\n" {
		t.Fatalf("csv raw=%q ok=%v", raw, ok)
	}

	r = recordsRequest("POST", "/import", `{"csv":"name\nGrace\n"}`, nil)
	if raw, ok := handler.readCSVPayload(httptest.NewRecorder(), r); !ok || string(raw) != "name\nGrace\n" {
		t.Fatalf("json raw=%q ok=%v err=%v", raw, ok, *serviceErr)
	}

	w := httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/import", nil)
	r.Header.Set("Content-Type", "text/csv")
	r.Body = recordsFailingBody{err: errors.New("read failed")}
	if _, ok := handler.readCSVPayload(w, r); ok || w.Code != http.StatusBadRequest || (*serviceErr).Error() != "backend.import.read_csv_failed" {
		t.Fatalf("read failure status=%d ok=%v err=%v", w.Code, ok, *serviceErr)
	}

	*serviceErr = nil
	w = httptest.NewRecorder()
	r = recordsRequest("POST", "/import", strings.Repeat("x", maxRecordImportPayloadBytes+1), nil)
	r.Header.Set("Content-Type", "text/csv")
	if _, ok := handler.readCSVPayload(w, r); ok || w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large csv status=%d ok=%v err=%v", w.Code, ok, *serviceErr)
	}

	*serviceErr = nil
	w = httptest.NewRecorder()
	if _, ok := handler.readCSVPayload(w, recordsRequest("POST", "/import", `{"csv":" "}`, nil)); ok || w.Code != http.StatusBadRequest {
		t.Fatalf("empty json status=%d ok=%v err=%v", w.Code, ok, *serviceErr)
	}

	*serviceErr = nil
	w = httptest.NewRecorder()
	largeJSON := `{"csv":"` + strings.Repeat("x", maxRecordImportPayloadBytes+1) + `"}`
	if _, ok := handler.readCSVPayload(w, recordsRequest("POST", "/import", largeJSON, nil)); ok || w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large json status=%d ok=%v err=%v", w.Code, ok, *serviceErr)
	}

	*serviceErr = nil
	w = httptest.NewRecorder()
	if _, ok := handler.readCSVPayload(w, recordsRequest("POST", "/import", `{`, nil)); ok || w.Code != http.StatusBadRequest || !errors.Is(*serviceErr, io.ErrUnexpectedEOF) && *serviceErr == nil {
		t.Fatalf("malformed json status=%d ok=%v err=%v", w.Code, ok, *serviceErr)
	}
}
