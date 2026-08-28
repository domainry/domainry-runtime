package runtimeext

import "testing"

func TestDurableIntentBatchEntryKey(t *testing.T) {
	if got := DurableIntentBatchEntryKey(map[string]any{"batch_key": " batch ", "entry_key": "entry"}); got != "batch:5:batch:entry:5:entry" {
		t.Fatalf("key=%q", got)
	}
	if got := DurableIntentBatchEntryKey(map[string]any{"batch_key": "batch"}); got != "" {
		t.Fatalf("partial key=%q", got)
	}
	first := DurableIntentBatchEntryKey(map[string]any{"batch_key": "a:b", "entry_key": "c"})
	second := DurableIntentBatchEntryKey(map[string]any{"batch_key": "a", "entry_key": "b:c"})
	if first == second {
		t.Fatalf("length-framed keys collided: %q", first)
	}
}
