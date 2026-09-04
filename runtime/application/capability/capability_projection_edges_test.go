package capability

import (
	"context"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"reflect"
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestCapabilityAuthoringInstanceSortsAndKeepsAnyReadyConnection(t *testing.T) {
	t.Parallel()

	service := NewCapabilityAuthoringApplicationService(func(context.Context, principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema {
		return capabilitycontract.CapabilityInstanceSchema{
			Objects:   []definitionmodel.ObjectSchema{{Key: "z_object"}, {Key: "a_object"}},
			Actions:   []definitionmodel.ActionSchema{{Key: "z_action"}, {Key: "a_action"}},
			Workflows: []definitionmodel.WorkflowSchema{{Key: "z_workflow"}, {Key: "a_workflow"}},
			Reports:   []reportmodel.ReportSchema{{Key: "z_report"}, {Key: "a_report"}},
			Integrations: connectormodel.IntegrationSchema{
				Connectors: []connectormodel.ConnectorSchema{
					{Key: "z_connector"},
					{Key: "a_connector", Providers: []connectormodel.ConnectorProviderSchema{{Key: "z_provider"}, {Key: "a_provider"}}, Operations: []connectormodel.ConnectorOperationSchema{{Key: "z_operation"}, {Key: "a_operation"}}},
				},
				Connections: []connectormodel.ConnectionSchema{
					{ConnectorKey: "a_connector", Status: "ready"},
					{ConnectorKey: "a_connector", Status: "disabled"},
					{ConnectorKey: "z_connector", Status: "draft"},
				},
			},
		}
	})
	instance, err := service.capabilityAuthoringInstance(t.Context(), principalmodel.Principal{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(instance.ObjectKeys, []string{"a_object", "z_object"}) ||
		!reflect.DeepEqual(instance.ActionKeys, []string{"a_action", "z_action"}) ||
		!reflect.DeepEqual(instance.WorkflowKeys, []string{"a_workflow", "z_workflow"}) ||
		!reflect.DeepEqual(instance.ReportKeys, []string{"a_report", "z_report"}) {
		t.Fatalf("instance keys are not deterministic: %#v", instance)
	}
	if len(instance.ConnectorOperations) != 2 || instance.ConnectorOperations[0].ConnectorKey != "a_connector" || !instance.ConnectorOperations[0].Ready || instance.ConnectorOperations[1].Ready {
		t.Fatalf("unexpected connector readiness projection: %#v", instance.ConnectorOperations)
	}
	if !reflect.DeepEqual(instance.ConnectorOperations[0].ProviderKeys, []string{"a_provider", "z_provider"}) || !reflect.DeepEqual(instance.ConnectorOperations[0].Operations, []string{"a_operation", "z_operation"}) {
		t.Fatalf("connector values are not sorted: %#v", instance.ConnectorOperations[0])
	}
}

func TestCapabilityAuthoringInstanceWithoutSchemaIsEmpty(t *testing.T) {
	t.Parallel()
	instance, err := NewCapabilityAuthoringApplicationService(nil).capabilityAuthoringInstance(t.Context(), principalmodel.Principal{})
	if err != nil {
		t.Fatal(err)
	}
	if len(instance.ObjectKeys) != 0 || len(instance.ActionKeys) != 0 || len(instance.WorkflowKeys) != 0 || len(instance.ReportKeys) != 0 || len(instance.ConnectorOperations) != 0 {
		t.Fatalf("unexpected empty instance: %#v", instance)
	}
}

func TestCapabilityAuthoringCatalogWrappers(t *testing.T) {
	t.Parallel()

	contract := RuntimeAuthoringCapabilities()
	if got, want := authoringContractHash(contract), ContractHash(contract); got != want {
		t.Fatalf("authoring contract hash=%q want=%q", got, want)
	}
	if got, want := CapabilityAuthoringInstanceHash(contract.Instance), capabilitycontract.CapabilityAuthoringInstanceHash(contract.Instance); got != want {
		t.Fatalf("instance hash=%q want=%q", got, want)
	}
	unsorted := capabilitycontract.CapabilityRuntimeAuthoringContract{Domains: []capabilitycontract.CapabilityAuthoringDomain{{Key: "z"}, {Key: "a"}}}
	sortAuthoringContract(&unsorted)
	if unsorted.Domains[0].Key != "a" {
		t.Fatalf("private sort wrapper did not sort domains: %#v", unsorted.Domains)
	}
}

func TestMaterializeAuthoringCapabilityPermissions(t *testing.T) {
	t.Parallel()

	materializeAuthoringCapabilityPermissions(nil)
	contract := capabilitycontract.CapabilityRuntimeAuthoringContract{Domains: []capabilitycontract.CapabilityAuthoringDomain{{
		Key: "test",
		Capabilities: []capabilitycontract.CapabilityAuthoringDefinition{
			{Key: "parent", Permissions: []string{"write", "read", "read"}},
			{Key: "inherited", Requires: []string{"parent"}},
			{Key: "direct", Requires: []string{"parent"}, Permissions: []string{"own"}},
			{Key: "missing", Requires: []string{"absent"}},
			{Key: "cycle-a", Requires: []string{"cycle-b"}},
			{Key: "cycle-b", Requires: []string{"cycle-a"}},
		},
	}}}
	materializeAuthoringCapabilityPermissions(&contract)
	want := map[string][]string{
		"parent": {"read", "write"}, "inherited": {"read", "write"}, "direct": {"own"}, "missing": {}, "cycle-a": {}, "cycle-b": {},
	}
	for _, capability := range contract.Domains[0].Capabilities {
		if !reflect.DeepEqual(capability.Permissions, want[capability.Key]) {
			t.Errorf("permissions for %q=%v want=%v", capability.Key, capability.Permissions, want[capability.Key])
		}
	}

	locations := map[string]authoringCapabilityLocation{"parent": {domainIndex: 0, capabilityIndex: 0}}
	resolved := map[string][]string{"parent": {"cached"}}
	if got := resolveAuthoringCapabilityPermissions(&contract, "parent", locations, resolved, map[string]bool{}); !reflect.DeepEqual(got, []string{"cached"}) {
		t.Fatalf("resolved cache=%v", got)
	}
	if got := resolveAuthoringCapabilityPermissions(&contract, "unknown", locations, resolved, map[string]bool{}); got != nil {
		t.Fatalf("unknown capability permissions=%v", got)
	}
}
