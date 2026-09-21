package model

import "testing"

func TestNotificationAudienceResolverKeysAreClosedAndDetached(t *testing.T) {
	keys := NotificationAudienceResolverKeys()
	if len(keys) != 1 || keys[0] != NotificationAudienceResolverWorkflowTaskAssignee {
		t.Fatalf("keys=%v", keys)
	}
	keys[0] = "mutated"
	if NotificationAudienceResolverKeys()[0] != NotificationAudienceResolverWorkflowTaskAssignee {
		t.Fatal("caller mutated the Runtime audience resolver registry")
	}
}
