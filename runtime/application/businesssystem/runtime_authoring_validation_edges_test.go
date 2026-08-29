package businesssystem

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func runtimeAuthoringEdgeDependencies() RuntimeAuthoringValidationDependencies {
	return RuntimeAuthoringValidationDependencies{
		CurrentManifest: func(context.Context, principalmodel.Principal) (manifestmodel.ManifestSchema, error) {
			return manifestmodel.ManifestSchema{}, nil
		},
		CurrentSnapshot: func(context.Context, principalmodel.Principal) (changeplanprojection.BusinessSystemSnapshot, error) {
			return runtimeAuthoringCompleteConfigurationSnapshot(nil), nil
		},
		ValidateDefinitions: func(context.Context, []integrationmodel.ConnectorSchema) error { return nil },
	}
}

func TestRuntimeAuthoringValidationDependencyAndSourceErrors(t *testing.T) {
	admin := runtimeAuthoringValidationAdmin()
	knownNonAdmin := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "builder", WorkspaceID: "default"}}
	if _, err := NewRuntimeAuthoringValidationApplicationService(RuntimeAuthoringValidationDependencies{}).Validate(t.Context(), knownNonAdmin); err == nil {
		t.Fatal("known non-admin accepted")
	}
	var nilService *RuntimeAuthoringValidationApplicationService
	if _, err := nilService.Validate(t.Context(), admin); err == nil {
		t.Fatal("nil service accepted")
	}
	if _, err := NewRuntimeAuthoringValidationApplicationService(RuntimeAuthoringValidationDependencies{}).Validate(t.Context(), admin); err == nil {
		t.Fatal("missing manifest dependency accepted")
	}
	dependencies := runtimeAuthoringEdgeDependencies()
	dependencies.CurrentSnapshot = nil
	if _, err := NewRuntimeAuthoringValidationApplicationService(dependencies).Validate(t.Context(), admin); err == nil {
		t.Fatal("missing current snapshot dependency accepted")
	}
	dependencies = runtimeAuthoringEdgeDependencies()
	dependencies.ValidateDefinitions = nil
	if _, err := NewRuntimeAuthoringValidationApplicationService(dependencies).Validate(t.Context(), admin); err == nil {
		t.Fatal("missing definition validation dependency accepted")
	}

	want := errors.New("source")
	dependencies = runtimeAuthoringEdgeDependencies()
	dependencies.CurrentManifest = func(context.Context, principalmodel.Principal) (manifestmodel.ManifestSchema, error) {
		return manifestmodel.ManifestSchema{}, want
	}
	if _, err := NewRuntimeAuthoringValidationApplicationService(dependencies).Validate(t.Context(), admin); !errors.Is(err, want) {
		t.Fatalf("manifest error=%v", err)
	}
	dependencies = runtimeAuthoringEdgeDependencies()
	dependencies.CurrentSnapshot = func(context.Context, principalmodel.Principal) (changeplanprojection.BusinessSystemSnapshot, error) {
		return changeplanprojection.BusinessSystemSnapshot{}, want
	}
	if _, err := NewRuntimeAuthoringValidationApplicationService(dependencies).Validate(t.Context(), admin); !errors.Is(err, want) {
		t.Fatalf("snapshot error=%v", err)
	}
	dependencies = runtimeAuthoringEdgeDependencies()
	dependencies.CurrentSnapshot = func(context.Context, principalmodel.Principal) (changeplanprojection.BusinessSystemSnapshot, error) {
		snapshot := runtimeAuthoringCompleteConfigurationSnapshot(nil)
		snapshot.SeedRecords = []businessseedmodel.BusinessSeedProvenance{{SeedKey: "order.primary"}}
		return snapshot, nil
	}
	dependencies.CurrentSeedRecords = func(context.Context, []businessseedmodel.BusinessSeedProvenance, principalmodel.Principal) ([]businessseedmodel.SeedRecordSchema, error) {
		return nil, want
	}
	if _, err := NewRuntimeAuthoringValidationApplicationService(dependencies).Validate(t.Context(), admin); !errors.Is(err, want) {
		t.Fatalf("seed materialization error=%v", err)
	}

	dependencies = runtimeAuthoringEdgeDependencies()
	dependencies.CurrentSnapshot = func(context.Context, principalmodel.Principal) (changeplanprojection.BusinessSystemSnapshot, error) {
		snapshot := runtimeAuthoringCompleteConfigurationSnapshot(nil)
		snapshot.SeedRecords = []businessseedmodel.BusinessSeedProvenance{{SeedKey: "order.primary"}}
		return snapshot, nil
	}
	if _, err := NewRuntimeAuthoringValidationApplicationService(dependencies).Validate(t.Context(), admin); err != nil {
		t.Fatalf("optional seed materializer rejected: %v", err)
	}

	dependencies = runtimeAuthoringEdgeDependencies()
	dependencies.StorageReadiness = func(context.Context) error { return nil }
	dependencies.MigrationReadiness = func(context.Context) error { return errors.New("migration") }
	report, err := NewRuntimeAuthoringValidationApplicationService(dependencies).Validate(t.Context(), admin)
	if err != nil || report.Checks["storage"] != "ok" || report.Checks["migration"] != "unavailable" {
		t.Fatalf("report=%#v err=%v", report, err)
	}
}

func TestRuntimeAuthoringValidationReusesCurrentDefinitionValidation(t *testing.T) {
	dependencies := runtimeAuthoringEdgeDependencies()
	dependencies.ValidateDefinitions = func(context.Context, []integrationmodel.ConnectorSchema) error {
		return apperror.New(apperror.KindBadRequest, "backend.change_plan.candidate_invalid", nil, map[string]string{
			"resource_type": "scheduler", "resource_key": "daily-refresh", "diagnostic": "unknown workflow target",
		})
	}
	report, err := NewRuntimeAuthoringValidationApplicationService(dependencies).Validate(t.Context(), runtimeAuthoringValidationAdmin())
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid || report.Checks["definition_graph"] != "invalid" {
		t.Fatalf("report=%#v", report)
	}
	found := false
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == "backend.runtime.global_definition_invalid" && diagnostic.Owner == "scheduler" && diagnostic.CapabilityKey == "scheduler.business_job" && diagnostic.ResourcePath == "definitions.scheduler.daily-refresh" {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics=%#v", report.Diagnostics)
	}

	want := errors.New("definition source unavailable")
	dependencies.ValidateDefinitions = func(context.Context, []integrationmodel.ConnectorSchema) error { return want }
	if _, err := NewRuntimeAuthoringValidationApplicationService(dependencies).Validate(t.Context(), runtimeAuthoringValidationAdmin()); !errors.Is(err, want) {
		t.Fatalf("definition source error=%v", err)
	}
}

func TestRuntimeAuthoringCompleteManifestOverlayEdges(t *testing.T) {
	var base manifestmodel.ManifestSchema
	raw := []byte(`{
      "schema_version":"2","source_blueprint_id":"blueprint","target_api_contract_version":"api-v1","target_api_contract_hash":"api-hash",
      "authoring_contract_version":"author-v1","authoring_contract_hash":"author-hash","description":"base",
	  "source_intent_coverage":{},"i18n":{"title":{"en":"Title"}},"notification_templates":[{}],
	  "identity_profile_extensions":[{}],"seed_records":[{}],
      "automation_execution_seeds":[{}],"business_loops":[{}],"state_machines":[{}],"validation_plan":[{}]
    }`)
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}
	completed := runtimeAuthoringCompleteManifest(manifestmodel.ManifestSchema{}, base)
	if completed.SchemaVersion != "2" || completed.SourceIntentCoverage == nil || len(completed.I18n) != 1 || len(completed.NotificationTemplates) != 1 || len(completed.IdentityProfileExtensions) != 1 || len(completed.SeedRecords) != 1 || len(completed.AutomationExecutionSeeds) != 1 || len(completed.BusinessLoops) != 1 || len(completed.StateMachines) != 1 || len(completed.ValidationPlan) != 1 {
		t.Fatalf("completed=%#v", completed)
	}
	current := base
	current.SchemaVersion = "current"
	completed = runtimeAuthoringCompleteManifest(current, manifestmodel.ManifestSchema{})
	if completed.SchemaVersion != "current" || completed.SourceIntentCoverage != base.SourceIntentCoverage {
		t.Fatalf("preserved=%#v", completed)
	}
	if valueOrDefault(" value ", "fallback") != " value " || valueOrDefault(" ", "fallback") != "fallback" {
		t.Fatal("value fallback mismatch")
	}
}

func TestRuntimeAuthoringValidationDiagnosticOwnersAndHashEdges(t *testing.T) {
	tests := []struct{ path, owner, capability string }{
		{"objects[0]", "metadata", "schema.object"}, {"objects[0].fields[0]", "metadata", "schema.field"},
		{"actions[0]", "action", "action.definition"},
		{"workflows[0]", "workflow", "workflow.definition"}, {"seed_records[0]", "seed", "seed.record"},
		{"integrations.connections[0]", "integration", "integration.connection"}, {"reports[0]", "report", "report.definition"},
		{"other", "manifest", ""},
	}
	for _, test := range tests {
		diagnostic := runtimeAuthoringValidationDiagnostic(test.path, "message")
		if diagnostic.Owner != test.owner || diagnostic.CapabilityKey != test.capability || diagnostic.ResourcePath != test.path {
			t.Fatalf("diagnostic=%#v", diagnostic)
		}
	}
	if first, second := runtimeAuthoringValidationHash(manifestmodel.ManifestSchema{}), runtimeAuthoringValidationHash(manifestmodel.ManifestSchema{}); first == "" || first != second {
		t.Fatalf("hashes=%q/%q", first, second)
	}

	plain := runtimeAuthoringDefinitionDiagnostic(apperror.New(
		apperror.KindBadRequest,
		"backend.change_plan.candidate_invalid",
		nil,
		nil,
	))
	if plain.ResourcePath != "definitions" || plain.Message != "current Runtime definition graph is invalid" {
		t.Fatalf("plain definition diagnostic=%#v", plain)
	}
	withoutKey := runtimeAuthoringDefinitionDiagnostic(apperror.New(
		apperror.KindBadRequest,
		"backend.change_plan.candidate_invalid",
		nil,
		map[string]string{"resource_type": "workflow"},
	))
	if withoutKey.ResourcePath != "definitions.workflow" {
		t.Fatalf("definition diagnostic without key=%#v", withoutKey)
	}
	for _, resourceType := range []string{"object", "field", "action", "connector"} {
		diagnostic := runtimeAuthoringDefinitionDiagnostic(apperror.New(
			apperror.KindBadRequest,
			"backend.change_plan.candidate_invalid",
			nil,
			map[string]string{"resource_type": resourceType, "resource_key": "resource"},
		))
		if diagnostic.ResourcePath != "definitions."+resourceType+".resource" {
			t.Fatalf("definition diagnostic for %s=%#v", resourceType, diagnostic)
		}
	}

	categories := []string{"schema", "reports", "runtime"}
	filtered := removeBusinessSnapshotCategory(categories, "reports")
	if len(filtered) != 2 || filtered[0] != "schema" || filtered[1] != "runtime" {
		t.Fatalf("filtered categories=%v", filtered)
	}
}

func TestRuntimeAuthoringGlobalCheckConditionOutcomes(t *testing.T) {
	report := RuntimeAuthoringValidationReport{Checks: map[string]string{"definition_graph": "ok", "manifest": "ok"}}
	snapshot := changeplanprojection.BusinessSystemSnapshot{
		Schema: metadatamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "order"}}},
		RuntimeState: changeplanprojection.BusinessRuntimeStateSnapshot{Connections: []changeplanprojection.IntegrationConnectionSummary{
			{Key: "inactive", Status: "disabled", Ready: false},
			{Key: "ready", Status: "active", Ready: true},
			{Key: "not-ready", Status: " ACTIVE ", Ready: false},
		}},
		SeedRecords: []businessseedmodel.BusinessSeedProvenance{
			{},
			{SeedKey: "missing-record"},
			{SeedKey: "missing-hash", RecordID: "record-1"},
			{SeedKey: "missing-object", RecordID: "record-2", ContentHash: "hash", ObjectKey: "missing"},
			{SeedKey: "valid", RecordID: "record-3", ContentHash: "hash", ObjectKey: "order"},
		},
		FrontendCapabilities: changeplanmodel.FrontendCapabilities{StaleFrontendSupport: []changeplanmodel.FrontendSupportEntry{{SupportKey: "stale"}}},
	}
	runtimeAuthoringApplyGlobalChecks(&report, snapshot)
	if report.Checks["connector_readiness"] != "invalid" || report.Checks["seed_writability"] != "invalid" || report.Checks["frontend_support"] != "invalid" {
		t.Fatalf("global checks=%#v diagnostics=%#v", report.Checks, report.Diagnostics)
	}

	report = RuntimeAuthoringValidationReport{Checks: map[string]string{"definition_graph": "invalid", "manifest": "ok"}}
	snapshot = changeplanprojection.BusinessSystemSnapshot{FrontendCapabilities: changeplanmodel.FrontendCapabilities{MissingFrontendSupport: []changeplanmodel.FrontendRequirement{{CapabilityKey: "schema.object", SupportKey: "objects"}}}}
	runtimeAuthoringApplyGlobalChecks(&report, snapshot)
	if report.Checks["cross_resource_references"] != "invalid" || report.Checks["frontend_support"] != "invalid" {
		t.Fatalf("invalid semantic checks=%#v", report.Checks)
	}

	if businessSystemRuntimeOwnedObject(definitionmodel.ObjectSchema{Config: map[string]any{"runtime_owned": false, "system_object": true}}) != true || businessSystemRuntimeOwnedObject(definitionmodel.ObjectSchema{}) {
		t.Fatal("Runtime-owned object classification mismatch")
	}
	dependencies := businessSystemTestDependencies()
	dependencies.Runtime.SchedulerDefinitions = nil
	if _, err := NewBusinessSystemApplicationService(dependencies).RuntimeStateSnapshot(t.Context(), principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}); err != nil {
		t.Fatalf("optional scheduler definitions should be omitted: %v", err)
	}
	objectSchema := metadatamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{
		{Key: "runtime_job", Config: map[string]any{"runtime_owned": true}},
		{Key: "order"},
	}}
	dependencies.SchemaForPrincipal = func(context.Context, principalmodel.Principal) metadatamodel.ApplicationSchemaSnapshot {
		return objectSchema
	}
	counts, err := NewBusinessSystemApplicationService(dependencies).businessObjectRecordCounts(t.Context(), principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	if err != nil || len(counts) != 1 || counts["order"] != 7 {
		t.Fatalf("business object counts=%v err=%v", counts, err)
	}
}
