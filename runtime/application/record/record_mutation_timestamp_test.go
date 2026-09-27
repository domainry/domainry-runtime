package record

import (
	"testing"
	"time"
)

func TestRecordCanonicalMutationTimestampMatchesPersistedPrecisionAndAdvances(t *testing.T) {
	now := time.Date(2026, time.September, 27, 1, 2, 3, 490691000, time.UTC)
	created := recordCanonicalMutationTimestamp(now, "")
	if created != "2026-09-27T01:02:03.49Z" {
		t.Fatalf("create version %q does not match millisecond storage", created)
	}
	updated := recordCanonicalMutationTimestamp(now, created)
	if updated != "2026-09-27T01:02:03.491Z" {
		t.Fatalf("same-millisecond update did not advance version: %q", updated)
	}
	if next := recordCanonicalMutationTimestamp(now.Add(2*time.Millisecond), updated); next != "2026-09-27T01:02:03.492Z" {
		t.Fatalf("later update did not use canonical clock precision: %q", next)
	}
}
