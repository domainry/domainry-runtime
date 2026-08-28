package policy

import "testing"

func TestWebhookExternalEventIDPrefersProviderIDAndCanonicalizesFallback(t *testing.T) {
	if value, fallback, err := WebhookExternalEventID("stripe", "primary", "invoice.paid", "evt_1", map[string]any{"ignored": true}); err != nil || fallback || value != "evt_1" {
		t.Fatalf("provider id=%q fallback=%v err=%v", value, fallback, err)
	}
	first, fallback, err := WebhookExternalEventID("custom", "primary", "updated", "", map[string]any{"record": map[string]any{"id": "one", "version": 2}})
	if err != nil || !fallback {
		t.Fatalf("fallback id=%q fallback=%v err=%v", first, fallback, err)
	}
	reordered, _, _ := WebhookExternalEventID("custom", "primary", "updated", "", map[string]any{"record": map[string]any{"version": 2, "id": "one"}})
	changed, _, _ := WebhookExternalEventID("custom", "primary", "updated", "", map[string]any{"record": map[string]any{"id": "two", "version": 2}})
	if first != reordered || first == changed {
		t.Fatalf("canonical fallback first=%q reordered=%q changed=%q", first, reordered, changed)
	}
	if value, fallback, err := WebhookExternalEventID("custom", "primary", "updated", "", map[string]any{"bad": make(chan int)}); err == nil || fallback || value != "" {
		t.Fatalf("invalid fallback = %q/%v/%v", value, fallback, err)
	}
}

func TestProviderRetryRequiresProtectionForNonIdempotentWrites(t *testing.T) {
	if ProviderRetryIsProtected(false, "write", 1, nil) {
		t.Fatal("non-idempotent write retry must declare a protection strategy")
	}
	for _, strategy := range []string{"query", "reconcile", "compensate"} {
		if !ProviderRetryIsProtected(false, "write", 1, map[string]any{"non_idempotent_retry_strategy": strategy}) {
			t.Fatalf("strategy %s was rejected", strategy)
		}
	}
	if !ProviderRetryIsProtected(true, "write", 2, nil) || !ProviderRetryIsProtected(false, "read", 2, nil) || !ProviderRetryIsProtected(false, "write", 0, nil) {
		t.Fatal("safe provider calls must not require a fallback strategy")
	}
}
