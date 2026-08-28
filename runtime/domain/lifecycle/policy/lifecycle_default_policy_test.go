package policy

import (
	"testing"
	"time"
)

func TestDefaultPolicyCatalogCompleteAndPublished(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	catalog := DefaultPolicyCatalog("workspace-1", "admin-1", now)
	if len(catalog) != 32 {
		t.Fatalf("catalog size = %d", len(catalog))
	}
	byKey := map[string]int{}
	for _, version := range catalog {
		if version.WorkspaceID != "workspace-1" || version.PublishedBy != "admin-1" || !version.PublishedAt.Equal(now) || version.Revision != 1 || version.Policy.Key == "" {
			t.Fatalf("invalid catalog entry = %#v", version)
		}
		byKey[version.Policy.Key]++
	}
	for _, key := range []string{"integration.webhook_nonce.v1", "integration.event.v1", "workflow.execution.v1", "automation.execution.v1", "scheduler.execution.v1", "report.export.v1", "cache.dictionary.v1"} {
		if byKey[key] != 1 {
			t.Fatalf("catalog key %q count = %d", key, byKey[key])
		}
	}
}
