package runtimeext

import (
	"errors"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func testExternalAuthorizationAction(key, method, route string) actioncontract.ActionDefinition {
	return actioncontract.ActionDefinition{
		Key: key, Owner: "module:example", SourceKind: "module_http",
		CapabilityKey: "example", CapabilityLabel: "Example", OperationKey: "read", OperationLabel: "Read example", Label: "Read example",
		Exposures: []actioncontract.Exposure{actioncontract.ExposurePublic}, Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated},
		HTTP: &actioncontract.HTTPBinding{Method: method, RouteTemplate: route}, Permission: &actioncontract.PermissionDefinition{
			Key: key, Owner: "module:example", ResourceKey: "example", OperationKey: "read", Label: "Read example", Category: "Example",
			GrantMode: actioncontract.PermissionGrantDirect, LifecycleStatus: actioncontract.LifecycleActive,
		},
		EffectClass: actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "external_http", LifecycleStatus: actioncontract.LifecycleActive,
	}
}

func TestProjectDefinitionRegistryFreezesExternalAuthorizationActions(t *testing.T) {
	registry := NewProjectExtensionRegistry()
	definition := testExternalAuthorizationAction("example.read", "GET", "/example")
	if err := registry.RegisterProjectExtensions(ProjectExtensions{Definitions: ProjectDefinitions{ExternalAuthorizationActions: []actioncontract.ActionDefinition{definition}}}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	definition.Permission.Label = "mutated input"
	snapshot := registry.ProjectDefinitions()
	if len(snapshot.ExternalAuthorizationActions) != 1 || snapshot.ExternalAuthorizationActions[0].Permission == nil || snapshot.ExternalAuthorizationActions[0].Permission.Label != "Read example" {
		t.Fatalf("external actions=%#v", snapshot.ExternalAuthorizationActions)
	}
	snapshot.ExternalAuthorizationActions[0].Permission.Label = "mutated snapshot"
	if registry.ProjectDefinitions().ExternalAuthorizationActions[0].Permission.Label != "Read example" {
		t.Fatal("registry returned a mutable external authorization action snapshot")
	}
	identities := registry.ProjectDefinitionIdentities()
	if len(identities) != 1 || identities[0].Kind != ProjectDefinitionExternalAuthorization || identities[0].Key != "example.read" || len(identities[0].SHA256) != 64 {
		t.Fatalf("identities=%#v", identities)
	}
}

func TestProjectDefinitionRegistryRejectsInvalidExternalAuthorizationActions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*actioncontract.ActionDefinition)
	}{
		{name: "permission missing", mutate: func(value *actioncontract.ActionDefinition) { value.Permission = nil }},
		{name: "http missing", mutate: func(value *actioncontract.ActionDefinition) {
			value.HTTP = nil
			value.NonHTTP = []actioncontract.NonHTTPBinding{{Kind: "external", InvocationKey: "example.read"}}
		}},
		{name: "owner is not module", mutate: func(value *actioncontract.ActionDefinition) {
			value.Owner = "application:example"
			value.Permission.Owner = value.Owner
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := testExternalAuthorizationAction("example.read", "GET", "/example")
			test.mutate(&definition)
			err := NewProjectExtensionRegistry().RegisterProjectExtensions(ProjectExtensions{Definitions: ProjectDefinitions{ExternalAuthorizationActions: []actioncontract.ActionDefinition{definition}}})
			if !errors.Is(err, ErrProjectDefinitionInvalid) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	t.Run("duplicate route", func(t *testing.T) {
		first := testExternalAuthorizationAction("example.read", "GET", "/example")
		second := testExternalAuthorizationAction("example.list", "GET", "/example")
		second.OperationKey = "list"
		second.Permission.Key, second.Permission.OperationKey = second.Key, "list"
		err := NewProjectExtensionRegistry().RegisterProjectExtensions(ProjectExtensions{Definitions: ProjectDefinitions{ExternalAuthorizationActions: []actioncontract.ActionDefinition{first, second}}})
		if !errors.Is(err, ErrProjectDefinitionInvalid) {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestProjectDefinitionRegistryBindsIntegrationWorkspaceAtRuntime(t *testing.T) {
	mapping := integrationsdk.EventMappingRequirement{
		Key: "calendar-event", Provider: "calendar", EventType: "calendar.changed", TargetType: "action",
		ObjectKey: "meeting", ActionKey: "meeting.ingest", RecordID: "meeting-a", Enabled: true,
	}
	registry := NewProjectExtensionRegistry()
	if err := registry.RegisterProjectExtensions(ProjectExtensions{Definitions: ProjectDefinitions{IntegrationMappings: []integrationsdk.EventMappingRequirement{mapping}}}); err != nil {
		t.Fatalf("deployment-neutral mapping rejected: %v", err)
	}
	stored := registry.ProjectDefinitions().IntegrationMappings
	if len(stored) != 1 || stored[0].WorkspaceID != "" {
		t.Fatalf("stored mappings=%#v", stored)
	}
	mapping.WorkspaceID = "workspace-baked-into-source"
	if err := NewProjectExtensionRegistry().RegisterProjectExtensions(ProjectExtensions{Definitions: ProjectDefinitions{IntegrationMappings: []integrationsdk.EventMappingRequirement{mapping}}}); !errors.Is(err, ErrProjectDefinitionInvalid) {
		t.Fatalf("source-owned workspace error=%v", err)
	}
}

func testReportDefinition(key string) ReportDefinition {
	return ReportDefinition{Report: reportmodel.ReportSchema{
		Key: key,
		ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
			SQL: "SELECT customer.name AS name FROM customer AS customer ORDER BY customer.name LIMIT 100",
		},
		RequiredPermissions: []string{"report." + key + ".read"},
	}}
}

func TestProjectDefinitionRegistryFreezesCodeOwnedPublicResources(t *testing.T) {
	registry := NewProjectExtensionRegistry()
	definition := PublicResourceDefinition{
		ObjectKey: "card",
		Resource: definitionmodel.ObjectPublicResource{
			Key: "public_card", AccessKeyField: "share_key", StateField: "status", ActiveState: "published", Fields: []string{"name"},
		},
	}
	if err := registry.RegisterProjectExtensions(ProjectExtensions{Definitions: ProjectDefinitions{PublicResources: []PublicResourceDefinition{definition}}}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	snapshot := registry.ProjectDefinitions()
	if len(snapshot.PublicResources) != 1 || snapshot.PublicResources[0].Resource.Key != "public_card" {
		t.Fatalf("public resources=%#v", snapshot.PublicResources)
	}
	snapshot.PublicResources[0].Resource.Fields[0] = "changed"
	if registry.ProjectDefinitions().PublicResources[0].Resource.Fields[0] != "name" {
		t.Fatal("registry returned a mutable public resource snapshot")
	}
	identities := registry.ProjectDefinitionIdentities()
	if len(identities) != 1 || identities[0].Kind != ProjectDefinitionPublicResource || len(identities[0].SHA256) != 64 {
		t.Fatalf("identities=%#v", identities)
	}
}

func TestProjectDefinitionRegistryFreezesCodeOwnedReports(t *testing.T) {
	registry := NewProjectExtensionRegistry()
	if err := registry.RegisterProjectExtensions(ProjectExtensions{Definitions: ProjectDefinitions{Reports: []ReportDefinition{testReportDefinition("customer_list")}}}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	definition, found := registry.ReportDefinition("customer_list")
	if !found || definition.Report.ObjectSQLV1 == nil || definition.Report.ObjectSQLV1.SQL == "" {
		t.Fatalf("report definition=%#v found=%v", definition, found)
	}
	identities := registry.ProjectDefinitionIdentities()
	if len(identities) != 1 || identities[0].Kind != ProjectDefinitionReport || len(identities[0].SHA256) != 64 {
		t.Fatalf("identities=%#v", identities)
	}
	if err := registry.RegisterProjectExtensions(ProjectExtensions{}); !errors.Is(err, ErrProjectExtensionRegistryFrozen) {
		t.Fatalf("frozen registration error=%v", err)
	}
}

func TestProjectDefinitionRegistryRejectsDuplicatesAndJSONBehaviorGaps(t *testing.T) {
	registry := NewProjectExtensionRegistry()
	duplicate := testReportDefinition("customer_list")
	if err := registry.RegisterProjectExtensions(ProjectExtensions{Definitions: ProjectDefinitions{Reports: []ReportDefinition{duplicate, duplicate}}}); !errors.Is(err, ErrProjectDefinitionDuplicate) {
		t.Fatalf("duplicate error=%v", err)
	}
	missingObjectSQL := testReportDefinition("broken")
	missingObjectSQL.Report.ObjectSQLV1 = nil
	if err := registry.RegisterProjectExtensions(ProjectExtensions{Definitions: ProjectDefinitions{Reports: []ReportDefinition{missingObjectSQL}}}); !errors.Is(err, ErrProjectDefinitionInvalid) {
		t.Fatalf("invalid error=%v", err)
	}
}
