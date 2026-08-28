package integration

import "testing"

func TestOutboxDedupIDIsStableAndScopeSensitive(t *testing.T) {
	first := OutboxDedupID("workspace-a", "webhook", "primary", "notify", "record:1")
	if first == "" || first != OutboxDedupID("workspace-a", "webhook", "primary", "notify", "record:1") {
		t.Fatalf("outbox dedup id is not stable: %q", first)
	}
	if first == OutboxDedupID("workspace-a", "webhook", "primary", "notify", "record:2") || first == OutboxDedupID("workspace-b", "webhook", "primary", "notify", "record:1") {
		t.Fatalf("outbox dedup id does not isolate scope: %q", first)
	}
}
