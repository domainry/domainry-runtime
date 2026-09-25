package timevalue

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type testPointerMarshaler struct{ value string }

func (value *testPointerMarshaler) MarshalJSON() ([]byte, error) {
	return json.Marshal("custom:" + value.value)
}

func TestJSONUsesStrictUnixMillisecondsForOwnedInstantFields(t *testing.T) {
	instant := time.Date(2026, 9, 25, 4, 5, 6, 789000000, time.UTC)
	value := struct {
		CreatedAt string          `json:"created_at"`
		Metadata  map[string]any  `json:"metadata"`
		Opaque    json.RawMessage `json:"opaque"`
	}{
		CreatedAt: instant.Format(time.RFC3339Nano),
		Metadata:  map[string]any{"scheduled_for": "external-label", "plain": "unchanged"},
		Opaque:    json.RawMessage(`{"occurred_at":"provider-owned"}`),
	}
	raw, err := MarshalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, `"created_at":1790309106789`) || !strings.Contains(text, `"scheduled_for":"external-label"`) || !strings.Contains(text, `"occurred_at":"provider-owned"`) {
		t.Fatalf("unexpected durable JSON: %s", text)
	}
	var decoded struct {
		CreatedAt string         `json:"created_at"`
		Metadata  map[string]any `json:"metadata"`
	}
	if err = UnmarshalJSON(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.CreatedAt != instant.Format(timestampLayout) || decoded.Metadata["scheduled_for"] != "external-label" {
		t.Fatalf("unexpected decoded value: %#v", decoded)
	}
	if err = UnmarshalJSON([]byte(`{"created_at":"2026-09-25T04:05:06Z","metadata":{}}`), &decoded); err == nil {
		t.Fatal("accepted string-encoded durable instant")
	}
}

func TestJSONProjectsTypedInstantWithoutInterpretingOpaqueMap(t *testing.T) {
	type value struct {
		ScheduledFor time.Time      `json:"scheduled_for"`
		Payload      map[string]any `json:"payload"`
	}
	instant := time.Date(2026, 9, 25, 12, 0, 0, 123_000_000, time.FixedZone("local", 8*3600))
	raw, err := MarshalJSON(value{ScheduledFor: instant, Payload: map[string]any{"scheduled_for": "external-label"}})
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		ScheduledFor int64          `json:"scheduled_for"`
		Payload      map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil || stored.ScheduledFor != instant.UnixMilli() || stored.Payload["scheduled_for"] != "external-label" {
		t.Fatalf("stored=%+v err=%v payload=%s", stored, err, raw)
	}
	var restored value
	if err := UnmarshalJSON(raw, &restored); err != nil || !restored.ScheduledFor.Equal(instant) || restored.Payload["scheduled_for"] != "external-label" {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
}

func TestJSONProjectionPreservesStandardOmitEmptyAndPointerMarshaler(t *testing.T) {
	value := struct {
		ZeroStruct struct{}              `json:"zero_struct,omitempty"`
		Empty      string                `json:"empty,omitempty"`
		Custom     *testPointerMarshaler `json:"custom"`
	}{Custom: &testPointerMarshaler{value: "kept"}}
	raw, err := MarshalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"custom":"custom:kept","zero_struct":{}}` {
		t.Fatalf("unexpected JSON shape: %s", raw)
	}
}
