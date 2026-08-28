package integration

import (
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	capacityplatform "github.com/domainry/domainry-runtime/runtime/platform/capacity"
)

func TestIntegrationWorkerPriorityQuotasKeepNewRetryAndReplayMoving(t *testing.T) {
	items := []integrationmodel.IntegrationEvent{
		{ID: "retry-1", WorkspaceID: "hot", Provider: "mail", Status: "failed"},
		{ID: "retry-2", WorkspaceID: "hot", Provider: "mail", Status: "failed"},
		{ID: "replay-1", WorkspaceID: "replay", Provider: "mail", Status: "received", Error: "backend.integration.event.manual_replay_queued"},
		{ID: "new-1", WorkspaceID: "a", Provider: "mail", Status: "received"},
		{ID: "new-2", WorkspaceID: "b", Provider: "mail", Status: "received"},
	}
	ordered := capacityplatform.QuotaFairOrder(items, 4, func(event integrationmodel.IntegrationEvent) string {
		return event.WorkspaceID + "\x00" + event.Provider
	}, integrationEventPriorityClass, []string{"retry", "manual_replay", "new"}, map[string]int{"retry": 1, "manual_replay": 1, "new": 2})
	classes := map[string]int{}
	for _, event := range ordered {
		classes[integrationEventPriorityClass(event)]++
	}
	if classes["retry"] != 1 || classes["manual_replay"] != 1 || classes["new"] != 2 {
		t.Fatalf("priority quotas not enforced: ordered=%#v classes=%#v", ordered, classes)
	}
}
