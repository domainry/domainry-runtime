package businesssystem

import (
	"context"
	"testing"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestRuntimeAuthoringDeliveryPropagatesValidationFailure(t *testing.T) {
	service := NewRuntimeAuthoringValidationApplicationService(RuntimeAuthoringValidationDependencies{})
	if _, err := service.VerifyDelivery(t.Context(), runtimeAuthoringValidationAdmin(), changeplanmodel.RuntimeAuthoringDeliverySubmission{}); err == nil {
		t.Fatal("delivery verification accepted unavailable validation dependencies")
	}

	service = NewRuntimeAuthoringValidationApplicationService(runtimeAuthoringEdgeDependencies())
	if report, err := service.VerifyDelivery(t.Context(), runtimeAuthoringValidationAdmin(), changeplanmodel.RuntimeAuthoringDeliverySubmission{}); err != nil || report.Valid {
		t.Fatalf("invalid delivery evidence should produce a report without a service error: report=%#v err=%v", report, err)
	}
}

func TestRuntimeAuthoringValidationMapsCoverageDetailDiagnostics(t *testing.T) {
	dependencies := runtimeAuthoringEdgeDependencies()
	dependencies.CurrentSnapshot = func(_ context.Context, _ principalmodel.Principal) (changeplanprojection.BusinessSystemSnapshot, error) {
		snapshot := runtimeAuthoringCompleteConfigurationSnapshot(nil)
		snapshot.CapabilityKeys = []string{"schema.object"}
		snapshot.ResourceSources = []changeplanprojection.SystemResourceSource{{ResourceType: "object", ResourceKey: "unreachable"}}
		snapshot.Finalize()
		return snapshot, nil
	}
	ledger := &changeplanmodel.RuntimeAuthoringCoverageLedger{
		Requirements: []changeplanmodel.RuntimeAuthoringCoverageRequirement{{
			RequirementID: "missing-details", CapabilityKeys: []string{"unknown.capability"},
			Resources: []changeplanmodel.RuntimeAuthoringCoverageResource{{ResourceType: "object", ResourceKey: "missing"}}, ScenarioIDs: []string{"missing.scenario"},
		}},
	}
	report, err := NewRuntimeAuthoringValidationApplicationService(dependencies).ValidateWithCoverage(t.Context(), runtimeAuthoringValidationAdmin(), ledger)
	if err != nil || report.Valid || len(report.Coverage.Entries[0].Issues) == 0 || len(report.Coverage.SourcelessResources) != 1 || len(report.Coverage.UnreachableResources) != 1 {
		t.Fatalf("report=%#v err=%v", report, err)
	}
	foundCapabilityRepair, foundResourceRepair := false, false
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Repair == nil {
			continue
		}
		if diagnostic.Repair.CapabilityKey == "unknown.capability" && diagnostic.Repair.JSONPointer == "/coverage/requirements/0/capability_keys" && diagnostic.Repair.ReferenceEndpoint == "/tenant-admin/platform-capabilities/capabilities/unknown.capability" {
			foundCapabilityRepair = true
		}
		if diagnostic.Repair.ResourceType == "object" && diagnostic.Repair.ResourceKey == "missing" && diagnostic.Repair.JSONPointer == "/coverage/requirements/0/resources" {
			foundResourceRepair = true
		}
	}
	if !foundCapabilityRepair || !foundResourceRepair {
		t.Fatalf("structured repair facts missing: %#v", report.Diagnostics)
	}
}

func TestRuntimeAuthoringEvidenceBindingUsesEnabledResourceHashes(t *testing.T) {
	snapshot := changeplanprojection.BusinessSystemSnapshot{
		RuntimeVersion:        "runtime-primary",
		AuthoringContractHash: "contract-hash",
		SnapshotHash:          "instance-hash",
		ResourceSources: []changeplanprojection.SystemResourceSource{
			{ResourceType: "object", ResourceKey: "disabled", SchemaHash: "ignored", Disabled: true},
			{ResourceType: "object", ResourceKey: "declared", SchemaHash: " schema-hash "},
			{ResourceType: "view", ResourceKey: "derived"},
		},
	}
	binding := runtimeAuthoringEvidenceBinding(snapshot, "configuration-hash")
	if binding.RuntimeVersion != "runtime-primary" || binding.ResourceHashes["object:declared"] != "schema-hash" || binding.ResourceHashes["view:derived"] == "" {
		t.Fatalf("binding=%#v", binding)
	}
	if _, exists := binding.ResourceHashes["object:disabled"]; exists {
		t.Fatalf("disabled resource leaked into binding: %#v", binding.ResourceHashes)
	}

	snapshot.RuntimeVersion = ""
	snapshot.RuntimeMetadata.RuntimeVersion = "runtime-fallback"
	if fallback := runtimeAuthoringEvidenceBinding(snapshot, "configuration-hash"); fallback.RuntimeVersion != "runtime-fallback" {
		t.Fatalf("fallback binding=%#v", fallback)
	}
}
