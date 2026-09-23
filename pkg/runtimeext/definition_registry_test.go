package runtimeext

import (
	"errors"
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

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
