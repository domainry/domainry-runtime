package capability

import (
	"reflect"
	"sort"
	"testing"
)

func TestRuntimeAuthoringCapabilitiesContainOnlyRuntimeOwnedDomains(t *testing.T) {
	contract := RuntimeAuthoringCapabilities()
	got := make([]string, 0, len(contract.Domains))
	for _, domain := range contract.Domains {
		got = append(got, domain.Key)
	}
	sort.Strings(got)
	want := []string{"action", "automation", "maintenance", "principal", "schema", "workflow"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Runtime authoring ownership drifted: got=%v want=%v", got, want)
	}
}

func TestRuntimeAuthoringCapabilitiesAreValidAndDeterministic(t *testing.T) {
	first := RuntimeAuthoringCapabilities()
	second := RuntimeAuthoringCapabilities()
	if err := first.Validate(); err != nil {
		t.Fatalf("validate authoring capability contract: %v", err)
	}
	if !reflect.DeepEqual(first, second) || first.ContractHash == "" {
		t.Fatal("authoring capability contract must be deterministic")
	}
	if first.ContractHash != RuntimeAuthoringContractHash {
		t.Fatalf("published authoring contract hash is stale: catalog=%s published=%s", first.ContractHash, RuntimeAuthoringContractHash)
	}
	if !reflect.DeepEqual(first.Instance.NotificationAudienceResolverKeys, []string{"workflow_task_assignee"}) {
		t.Fatalf("Notification audience resolver registry is not disclosed: %v", first.Instance.NotificationAudienceResolverKeys)
	}
}

func TestRuntimeAuthoringCatalogSummaryMatchesCatalogAndIsMutationSafe(t *testing.T) {
	contract := RuntimeAuthoringCapabilities()
	summary := RuntimeAuthoringCatalogSummary()
	capabilityCount := 0
	for _, domain := range contract.Domains {
		capabilityCount += len(domain.Capabilities)
	}
	if summary.ContractVersion != contract.ContractVersion || summary.EndpointContractVersion != contract.EndpointContractVersion || summary.RuntimeVersion != contract.RuntimeVersion || summary.ContractHash != contract.ContractHash || len(summary.CapabilityKeys) != capabilityCount || len(summary.Domains) != len(contract.Domains) {
		t.Fatalf("summary=%#v contract=%#v", summary, contract)
	}
	firstKey := summary.CapabilityKeys[0]
	summary.CapabilityKeys[0] = "mutated"
	summary.Domains[0].Key = "mutated"
	again := RuntimeAuthoringCatalogSummary()
	if again.CapabilityKeys[0] != firstKey || again.Domains[0].Key == "mutated" {
		t.Fatalf("caller mutated cached authoring summary: %#v", again)
	}
}

func TestRuntimeAuthoringErrorContractFallbackClassification(t *testing.T) {
	unknown := RuntimeAuthoringErrorContract("backend.example.unknown", map[string]string{"field_path": "items[2].value"})
	if unknown.ContractVersion == "" || unknown.CapabilityKey != "" || unknown.FieldPath != "items[2].value" {
		t.Fatalf("unexpected unknown fallback: %#v", unknown)
	}
}
