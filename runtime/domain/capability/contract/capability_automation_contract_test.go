package contract

import "testing"

func TestRuntimeAutomationCatalogPublishesWebhookAndHumanReview(t *testing.T) {
	catalog := RuntimeAutomationExecutionCatalog()
	if !stringListContains(catalog.Phases, "webhook") || !stringListContains(catalog.InstructionTypes, "request_human_review") || !stringListContains(catalog.AfterInstructionTypes, "request_human_review") {
		t.Fatalf("catalog=%#v", catalog)
	}
	for _, capability := range catalog.Capabilities {
		if capability.Type == "request_human_review" {
			if !stringListContains(capability.SupportedContexts, "after") || !stringListContains(capability.SupportedContexts, "webhook") {
				t.Fatalf("human review capability=%#v", capability)
			}
			return
		}
	}
	t.Fatal("request_human_review capability missing")
}

func stringListContains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
