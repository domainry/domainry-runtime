package businesssystem

import (
	"context"
	"encoding/json"
	"errors"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"os"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestRuntimeAuthoringValidationAcceptsCanonicalRuntimeManifest(t *testing.T) {
	raw, err := os.ReadFile("../../domain/manifest/testdata/manifests/domain-only-minimal.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest := manifestmodel.ManifestSchema{}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	metadataSubset := manifest
	metadataSubset.SeedRecords = nil
	service := NewRuntimeAuthoringValidationApplicationService(RuntimeAuthoringValidationDependencies{
		CurrentManifest: func(context.Context, principalmodel.Principal) (manifestmodel.ManifestSchema, error) {
			return metadataSubset, nil
		},
		BaseManifest: manifest,
		CurrentSnapshot: func(context.Context, principalmodel.Principal) (changeplanprojection.BusinessSystemSnapshot, error) {
			snapshot := runtimeAuthoringCompleteConfigurationSnapshot([]connectormodel.ConnectorSchema{{Key: "file_storage"}})
			snapshot.Schema.Objects = append([]definitionmodel.ObjectSchema(nil), manifest.Objects...)
			snapshot.SeedRecords = []businessseedmodel.BusinessSeedProvenance{{SeedKey: "customer.primary", ObjectKey: "customer", RecordID: "customer-1", ContentHash: "hash"}}
			snapshot.CapabilityKeys = []string{"schema.object"}
			snapshot.ResourceSources = []changeplanprojection.SystemResourceSource{{ResourceType: "object", ResourceKey: "customer", SourceKind: "manifest"}}
			snapshot.Finalize()
			return snapshot, nil
		},
		CurrentSeedRecords: func(_ context.Context, provenance []businessseedmodel.BusinessSeedProvenance, _ principalmodel.Principal) ([]businessseedmodel.SeedRecordSchema, error) {
			if len(provenance) != 1 || provenance[0].SeedKey != "customer.primary" {
				t.Fatalf("seed provenance=%#v", provenance)
			}
			return manifest.SeedRecords, nil
		},
		ValidateDefinitions: func(context.Context, []connectormodel.ConnectorSchema) error { return nil },
	})
	coverage := &changeplanmodel.RuntimeAuthoringCoverageLedger{Version: changeplanmodel.RuntimeAuthoringCoverageLedgerVersion, Requirements: []changeplanmodel.RuntimeAuthoringCoverageRequirement{{
		RequirementID: "customer-management", CapabilityKeys: []string{"schema.object"},
		Resources: []changeplanmodel.RuntimeAuthoringCoverageResource{{ResourceType: "object", ResourceKey: "customer"}}, ScenarioIDs: []string{"customer.create.success"},
	}}}
	first, err := service.ValidateWithCoverage(t.Context(), runtimeAuthoringValidationAdmin(), coverage)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ValidateWithCoverage(t.Context(), runtimeAuthoringValidationAdmin(), coverage)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Valid || first.Status != "valid" || first.SnapshotHash == "" || first.SnapshotHash != second.SnapshotHash {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
}

func TestRuntimeAuthoringValidationMapsOwnerDiagnosticsAndReadiness(t *testing.T) {
	service := NewRuntimeAuthoringValidationApplicationService(RuntimeAuthoringValidationDependencies{
		CurrentManifest: func(context.Context, principalmodel.Principal) (manifestmodel.ManifestSchema, error) {
			return manifestmodel.ManifestSchema{
				SchemaVersion: "2", TemplateID: "direct", Version: "0.0.0-configuring",
				Objects: []definitionmodel.ObjectSchema{{Key: "order", Name: "Order", Fields: []definitionmodel.FieldSchema{{Key: "status", Name: "Status", Type: "text", Required: true}}}},
			}, nil
		},
		CurrentSnapshot: func(context.Context, principalmodel.Principal) (changeplanprojection.BusinessSystemSnapshot, error) {
			return runtimeAuthoringCompleteConfigurationSnapshot(nil), nil
		},
		ValidateDefinitions: func(context.Context, []connectormodel.ConnectorSchema) error { return nil },
		StorageReadiness:    func(context.Context) error { return errors.New("storage unavailable") },
	})
	report, err := service.Validate(t.Context(), runtimeAuthoringValidationAdmin())
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid || report.Status != "invalid" || report.SnapshotHash == "" || report.Checks["storage"] != "unavailable" {
		t.Fatalf("report=%#v", report)
	}
	for _, key := range []string{"cross_resource_references", "cycles", "permission_closure", "foundation_usage"} {
		if report.Checks[key] != "invalid" {
			t.Fatalf("manifest-backed check %s=%q report=%#v", key, report.Checks[key], report)
		}
	}
	capabilities := map[string]bool{}
	for _, diagnostic := range report.Diagnostics {
		capabilities[diagnostic.CapabilityKey] = true
	}
	if !capabilities["seed.record"] || !capabilities["deployment.runtime"] {
		t.Fatalf("owner diagnostics=%#v", report.Diagnostics)
	}
}

func TestRuntimeAuthoringValidationRejectsIncompleteConfigurationSnapshot(t *testing.T) {
	snapshot := runtimeAuthoringCompleteConfigurationSnapshot(nil)
	snapshot.ResourceVisibility["runtime_state.scheduler"] = "hidden"
	service := NewRuntimeAuthoringValidationApplicationService(RuntimeAuthoringValidationDependencies{
		CurrentManifest: func(context.Context, principalmodel.Principal) (manifestmodel.ManifestSchema, error) {
			return manifestmodel.ManifestSchema{SchemaVersion: "2", TemplateID: "direct", Version: "0.0.0-configuring", Objects: []definitionmodel.ObjectSchema{}}, nil
		},
		CurrentSnapshot: func(context.Context, principalmodel.Principal) (changeplanprojection.BusinessSystemSnapshot, error) {
			return snapshot, nil
		},
		ValidateDefinitions: func(context.Context, []connectormodel.ConnectorSchema) error { return nil },
	})
	report, err := service.Validate(t.Context(), runtimeAuthoringValidationAdmin())
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid || report.Checks["configuration.runtime_state.scheduler"] != "unavailable" {
		t.Fatalf("report=%#v", report)
	}
	found := false
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == "backend.runtime.configuration_snapshot_incomplete" && diagnostic.CapabilityKey == "maintenance.current_state_snapshot" && diagnostic.ResourcePath == "configuration.runtime_state.scheduler" {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics=%#v", report.Diagnostics)
	}
}

func TestRuntimeAuthoringValidationHashIncludesLiveOwnerConfiguration(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{SchemaVersion: "2", TemplateID: "direct", Version: "configuring"}
	first := runtimeAuthoringCompleteConfigurationSnapshot(nil)
	second := runtimeAuthoringCompleteConfigurationSnapshot(nil)
	second.ObjectRecordCounts["order"] = 1
	second.Finalize()
	if runtimeAuthoringConfigurationHash(manifest, first) == runtimeAuthoringConfigurationHash(manifest, second) {
		t.Fatal("complete configuration hash ignored live owner state")
	}
}

func TestRuntimeAuthoringGlobalChecksRejectLiveReadinessAndProvenanceGaps(t *testing.T) {
	snapshot := runtimeAuthoringCompleteConfigurationSnapshot(nil)
	snapshot.Schema.Objects = []definitionmodel.ObjectSchema{{Key: "order"}}
	snapshot.RuntimeState.Connections = []changeplanprojection.IntegrationConnectionSummary{{Key: "crm", Status: "active", Ready: false}}
	report := RuntimeAuthoringValidationReport{Checks: map[string]string{"definition_graph": "ok", "manifest": "ok"}, Diagnostics: []RuntimeAuthoringValidationDiagnostic{}}
	runtimeAuthoringApplyGlobalChecks(&report, snapshot)
	for _, key := range []string{"cross_resource_references", "cycles", "permission_closure", "foundation_usage"} {
		if report.Checks[key] != "ok" {
			t.Fatalf("semantic check %s=%q", key, report.Checks[key])
		}
	}
	for _, key := range []string{"connector_readiness"} {
		if report.Checks[key] != "invalid" {
			t.Fatalf("live check %s=%q report=%#v", key, report.Checks[key], report)
		}
	}
	if len(report.Diagnostics) != 1 {
		t.Fatalf("diagnostics=%#v", report.Diagnostics)
	}
}

func TestRuntimeAuthoringValidationRequiresWorkspaceAdmin(t *testing.T) {
	service := NewRuntimeAuthoringValidationApplicationService(RuntimeAuthoringValidationDependencies{})
	if _, err := service.Validate(t.Context(), principalmodel.Principal{}); err == nil {
		t.Fatal("unknown principal passed global validation")
	}
}

func runtimeAuthoringValidationAdmin() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "builder", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Key: "builder", Permissions: []string{ActionBusinessSystemSnapshot}})
}

func runtimeAuthoringCompleteConfigurationSnapshot(connectors []connectormodel.ConnectorSchema) changeplanprojection.BusinessSystemSnapshot {
	visibility := map[string]string{}
	for _, category := range runtimeAuthoringRequiredConfigurationCategories {
		visibility[category] = "visible"
	}
	snapshot := changeplanprojection.BusinessSystemSnapshot{
		SnapshotVersion:    changeplanprojection.BusinessSystemSnapshotVersion,
		ResourceVisibility: visibility,
		RuntimeState:       changeplanprojection.BusinessRuntimeStateSnapshot{Connectors: connectors},
	}
	snapshot.Finalize()
	return snapshot
}
