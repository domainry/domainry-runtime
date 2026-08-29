package report

import (
	"reflect"
	"testing"
	"time"
)

func TestReportObjectSQLCursorPreservesBoundValueTypes(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 30, 0, 123, time.UTC)
	want := []any{nil, int64(42), 12.5, true, "record-42", now}
	encoded, err := encodeReportObjectSQLCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeReportObjectSQLCursor(encoded, len(want))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cursor values=%#v want=%#v", got, want)
	}
}

func TestReportObjectSQLCursorRejectsShapeAndTypeTampering(t *testing.T) {
	encoded, err := encodeReportObjectSQLCursor([]any{"id-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeReportObjectSQLCursor(encoded, 2); err == nil {
		t.Fatal("cursor with the wrong stable-order arity was accepted")
	}
	if _, err := decodeReportObjectSQLCursor(encoded+"x", 1); err == nil {
		t.Fatal("tampered cursor was accepted")
	}
}
