package action

import "testing"

func TestActionApplicationValueHelpers(t *testing.T) {
	value := map[string]any{"status": "ready"}
	if got := actionMapValue(value); got["status"] != "ready" {
		t.Fatalf("mapped value=%#v", got)
	}
	if got := actionMapValue("not-a-map"); got != nil {
		t.Fatalf("non-map value=%#v", got)
	}
	if got := actionFirstNonNil(nil, "first", "second"); got != "first" {
		t.Fatalf("first non-nil=%#v", got)
	}
	if got := actionFirstNonNil(nil, nil); got != nil {
		t.Fatalf("all-nil value=%#v", got)
	}
}
