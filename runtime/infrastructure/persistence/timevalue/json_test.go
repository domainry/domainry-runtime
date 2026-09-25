package timevalue

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestJSONUsesStrictUnixMillisecondsForOwnedInstantFields(t *testing.T) {
	instant := time.Date(2026, 9, 25, 4, 5, 6, 789000000, time.UTC)
	value := struct {
		CreatedAt string          `json:"created_at"`
		Metadata  map[string]any  `json:"metadata"`
		Opaque    json.RawMessage `json:"opaque"`
	}{
		CreatedAt: instant.Format(time.RFC3339Nano),
		Metadata:  map[string]any{"scheduled_for": instant, "plain": "unchanged"},
		Opaque:    json.RawMessage(`{"occurred_at":"provider-owned"}`),
	}
	raw, err := MarshalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, `"created_at":1790309106789`) || !strings.Contains(text, `"scheduled_for":1790309106789`) || !strings.Contains(text, `"occurred_at":"provider-owned"`) {
		t.Fatalf("unexpected durable JSON: %s", text)
	}
	var decoded struct {
		CreatedAt string         `json:"created_at"`
		Metadata  map[string]any `json:"metadata"`
	}
	if err = UnmarshalJSON(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.CreatedAt != instant.Format(timestampLayout) || decoded.Metadata["scheduled_for"] != instant.Format(timestampLayout) {
		t.Fatalf("unexpected decoded value: %#v", decoded)
	}
	if err = UnmarshalJSON([]byte(`{"created_at":"2026-09-25T04:05:06Z","metadata":{}}`), &decoded); err == nil {
		t.Fatal("accepted string-encoded durable instant")
	}
}
