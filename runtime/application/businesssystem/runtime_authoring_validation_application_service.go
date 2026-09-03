package businesssystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	changeplanvalidation "github.com/domainry/domainry-runtime/runtime/domain/changeplan/validation"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	manifestvalidation "github.com/domainry/domainry-runtime/runtime/domain/manifest/validation"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type RuntimeAuthoringValidationDependencies struct {
	CurrentManifest     func(context.Context, principalmodel.Principal) (manifestmodel.ManifestSchema, error)
	CurrentSnapshot     func(context.Context, principalmodel.Principal) (changeplanprojection.BusinessSystemSnapshot, error)
	CurrentSeedRecords  func(context.Context, []businessseedmodel.BusinessSeedProvenance, principalmodel.Principal) ([]businessseedmodel.SeedRecordSchema, error)
	BaseManifest        manifestmodel.ManifestSchema
	ValidateDefinitions func(context.Context, []connectormodel.ConnectorSchema) error
	StorageReadiness    func(context.Context) error
	MigrationReadiness  func(context.Context) error
	ScenarioReceipts    *RuntimeAuthoringScenarioReceiptService
}

type RuntimeAuthoringValidationDiagnostic struct {
	Code          string                       `json:"code"`
	Owner         string                       `json:"owner"`
	CapabilityKey string                       `json:"capability_key,omitempty"`
	ResourcePath  string                       `json:"resource_path"`
	Message       string                       `json:"message"`
	Repair        *RuntimeAuthoringRepairFacts `json:"repair,omitempty"`
}

// RuntimeAuthoringRepairFacts keeps validation repair machine-readable. The
// message remains for humans; callers should not have to infer these facts
// from prose.
type RuntimeAuthoringRepairFacts struct {
	Operation         string   `json:"operation"`
	JSONPointer       string   `json:"json_pointer,omitempty"`
	Expected          []string `json:"expected,omitempty"`
	Actual            string   `json:"actual,omitempty"`
	ResourceType      string   `json:"resource_type,omitempty"`
	ResourceKey       string   `json:"resource_key,omitempty"`
	CapabilityKey     string   `json:"capability_key,omitempty"`
	ReferenceEndpoint string   `json:"reference_endpoint,omitempty"`
}

type RuntimeAuthoringValidationReport struct {
	Version            string                                                     `json:"version"`
	Scope              string                                                     `json:"scope"`
	Valid              bool                                                       `json:"valid"`
	Status             string                                                     `json:"status"`
	SnapshotHash       string                                                     `json:"snapshot_hash"`
	Diagnostics        []RuntimeAuthoringValidationDiagnostic                     `json:"diagnostics"`
	Checks             map[string]string                                          `json:"checks"`
	Coverage           changeplanmodel.RuntimeAuthoringCoverageReport             `json:"coverage"`
	Binding            changeplanmodel.RuntimeAuthoringEvidenceBinding            `json:"binding"`
	EvidenceCollection changeplanmodel.RuntimeAuthoringEvidenceCollectionContract `json:"evidence_collection"`
	EvidenceSession    *changeplanmodel.RuntimeAuthoringEvidenceSession           `json:"evidence_session,omitempty"`
}

const (
	runtimeAuthoringValidationReportVersion      = "runtime-authoring-validation-v4"
	runtimeAuthoringConfigurationValidationScope = "configuration"
	runtimeAuthoringCoverageValidationScope      = "configuration_and_coverage"
	runtimeAuthoringEvidencePlanValidationScope  = "configuration_coverage_and_evidence_plan"
)

type RuntimeAuthoringValidationApplicationService struct {
	dependencies RuntimeAuthoringValidationDependencies
}

func NewRuntimeAuthoringValidationApplicationService(dependencies RuntimeAuthoringValidationDependencies) *RuntimeAuthoringValidationApplicationService {
	return &RuntimeAuthoringValidationApplicationService{dependencies: dependencies}
}

func (s *RuntimeAuthoringValidationApplicationService) Validate(ctx context.Context, principal principalmodel.Principal) (RuntimeAuthoringValidationReport, error) {
	report, _, err := s.validateConfiguration(ctx, principal)
	return report, err
}

func (s *RuntimeAuthoringValidationApplicationService) ValidateWithCoverage(ctx context.Context, principal principalmodel.Principal, ledger *changeplanmodel.RuntimeAuthoringCoverageLedger) (RuntimeAuthoringValidationReport, error) {
	return s.ValidateWithCoverageAndEvidencePlan(ctx, principal, ledger, nil)
}

func (s *RuntimeAuthoringValidationApplicationService) ValidateWithCoverageAndEvidencePlan(ctx context.Context, principal principalmodel.Principal, ledger *changeplanmodel.RuntimeAuthoringCoverageLedger, plan *changeplanmodel.RuntimeAuthoringEvidencePlan) (RuntimeAuthoringValidationReport, error) {
	report, snapshot, err := s.validateConfiguration(ctx, principal)
	if err != nil {
		return RuntimeAuthoringValidationReport{}, err
	}
	report.Scope = runtimeAuthoringCoverageValidationScope
	runtimeAuthoringApplyCoverageValidation(&report, ledger, snapshot)
	runtimeAuthoringFinalizeValidation(&report)
	if plan == nil {
		return report, nil
	}
	report.Scope = runtimeAuthoringEvidencePlanValidationScope
	if !report.Valid {
		report.Checks["evidence_plan"] = "blocked"
		return report, nil
	}
	report.Checks["evidence_plan"] = "ok"
	if s.dependencies.ScenarioReceipts == nil {
		report.Checks["evidence_plan"] = "unavailable"
		report.Diagnostics = append(report.Diagnostics, RuntimeAuthoringValidationDiagnostic{
			Code: "backend.runtime.evidence_plan_unavailable", Owner: "businesssystem",
			CapabilityKey: "maintenance.current_state_snapshot", ResourcePath: "evidence_plan",
			Message: "Runtime evidence session signer is unavailable",
		})
		runtimeAuthoringFinalizeValidation(&report)
		return report, nil
	}
	if ledger == nil {
		report.Checks["evidence_plan"] = "invalid"
		report.Diagnostics = append(report.Diagnostics, RuntimeAuthoringValidationDiagnostic{
			Code: "backend.runtime.evidence_plan_invalid", Owner: "businesssystem",
			CapabilityKey: "maintenance.current_state_snapshot", ResourcePath: "evidence_plan",
			Message: "Runtime evidence plan requires a coverage ledger",
		})
		runtimeAuthoringFinalizeValidation(&report)
		return report, nil
	}
	session, issueErr := s.dependencies.ScenarioReceipts.IssueEvidenceSession(operationscontract.BuilderTaskID(ctx), report.Binding, *ledger, *plan)
	if issueErr != nil {
		report.Checks["evidence_plan"] = "invalid"
		report.Diagnostics = append(report.Diagnostics, RuntimeAuthoringValidationDiagnostic{
			Code: "backend.runtime.evidence_plan_invalid", Owner: "businesssystem",
			CapabilityKey: "maintenance.current_state_snapshot", ResourcePath: "evidence_plan",
			Message: issueErr.Error(),
		})
		runtimeAuthoringFinalizeValidation(&report)
		return report, nil
	}
	report.EvidenceSession = &session
	return report, nil
}

func (s *RuntimeAuthoringValidationApplicationService) validateConfiguration(ctx context.Context, principal principalmodel.Principal) (RuntimeAuthoringValidationReport, changeplanprojection.BusinessSystemSnapshot, error) {
	if !principal.Known {
		return RuntimeAuthoringValidationReport{}, changeplanprojection.BusinessSystemSnapshot{}, businessSystemForbidden("auth.permission_denied")
	}
	if s == nil || s.dependencies.CurrentManifest == nil || s.dependencies.CurrentSnapshot == nil || s.dependencies.ValidateDefinitions == nil {
		return RuntimeAuthoringValidationReport{}, changeplanprojection.BusinessSystemSnapshot{}, businessSystemInternalError("global authoring validation dependencies", nil)
	}
	manifest, err := s.dependencies.CurrentManifest(ctx, principal)
	if err != nil {
		return RuntimeAuthoringValidationReport{}, changeplanprojection.BusinessSystemSnapshot{}, err
	}
	snapshot, err := s.dependencies.CurrentSnapshot(ctx, principal)
	if err != nil {
		return RuntimeAuthoringValidationReport{}, changeplanprojection.BusinessSystemSnapshot{}, err
	}
	manifest = runtimeAuthoringCompleteManifest(manifest, s.dependencies.BaseManifest)
	if len(snapshot.SeedRecords) > 0 && s.dependencies.CurrentSeedRecords != nil {
		seedRecords, seedErr := s.dependencies.CurrentSeedRecords(ctx, snapshot.SeedRecords, principal)
		if seedErr != nil {
			return RuntimeAuthoringValidationReport{}, changeplanprojection.BusinessSystemSnapshot{}, seedErr
		}
		manifest.SeedRecords = seedRecords
	}
	configurationHash := runtimeAuthoringConfigurationHash(manifest, snapshot)
	report := RuntimeAuthoringValidationReport{
		Version: runtimeAuthoringValidationReportVersion, Scope: runtimeAuthoringConfigurationValidationScope,
		Status: "valid", SnapshotHash: configurationHash, Binding: runtimeAuthoringEvidenceBinding(snapshot, configurationHash),
		EvidenceCollection: runtimeAuthoringEvidenceCollectionContract(),
		Diagnostics:        []RuntimeAuthoringValidationDiagnostic{}, Checks: map[string]string{"definition_graph": "ok", "manifest": "ok", "storage": "ok", "migration": "ok"},
	}
	for key, status := range runtimeAuthoringConfigurationCoverage(snapshot, &report.Diagnostics) {
		report.Checks[key] = status
	}
	if definitionErr := s.dependencies.ValidateDefinitions(ctx, snapshot.RuntimeState.Connectors); definitionErr != nil {
		if apperror.CodeOf(definitionErr) != "backend.metadata.candidate_invalid" {
			return RuntimeAuthoringValidationReport{}, changeplanprojection.BusinessSystemSnapshot{}, definitionErr
		}
		report.Checks["definition_graph"] = "invalid"
		report.Diagnostics = append(report.Diagnostics, runtimeAuthoringDefinitionDiagnostic(definitionErr))
	}
	if validationErr := manifestvalidation.ValidateManifestWithConnectorCatalog(manifest, snapshot.RuntimeState.Connectors); validationErr != nil {
		report.Checks["manifest"] = "invalid"
		var validationErrors manifestvalidation.ValidationErrors
		// ValidateManifestWithConnectorCatalog owns this closed error contract:
		// every non-nil result is a ValidationErrors collection.
		_ = errors.As(validationErr, &validationErrors)
		for _, issue := range validationErrors {
			report.Diagnostics = append(report.Diagnostics, runtimeAuthoringValidationDiagnostic(issue.Path, issue.Message))
		}
	}
	runtimeAuthoringApplyGlobalChecks(&report, snapshot)
	for key, check := range map[string]func(context.Context) error{"storage": s.dependencies.StorageReadiness, "migration": s.dependencies.MigrationReadiness} {
		if check != nil {
			if checkErr := check(ctx); checkErr != nil {
				report.Checks[key] = "unavailable"
				report.Diagnostics = append(report.Diagnostics, RuntimeAuthoringValidationDiagnostic{
					Code: "backend.runtime." + key + "_unavailable", Owner: "deployment", CapabilityKey: "deployment.runtime", ResourcePath: key, Message: key + " readiness failed",
					Repair: &RuntimeAuthoringRepairFacts{Operation: "restore_readiness", JSONPointer: "/checks/" + key, Expected: []string{"ok"}, Actual: "unavailable"},
				})
			}
		}
	}
	runtimeAuthoringFinalizeValidation(&report)
	return report, snapshot, nil
}

func runtimeAuthoringEvidenceCollectionContract() changeplanmodel.RuntimeAuthoringEvidenceCollectionContract {
	return changeplanmodel.RuntimeAuthoringEvidenceCollectionContract{
		Version: changeplanmodel.RuntimeAuthoringEvidenceCollectionVersion, TrustPolicy: changeplanmodel.RuntimeAuthoringEvidenceTrustPolicy,
		EvidencePlanVersion: changeplanmodel.RuntimeAuthoringEvidencePlanVersion, EvidenceSessionVersion: changeplanmodel.RuntimeAuthoringEvidenceSessionVersion,
		EvidenceStepTokenVersion: changeplanmodel.RuntimeAuthoringEvidenceStepTokenVersion,
		StepReceiptVersion:       changeplanmodel.RuntimeAuthoringStepReceiptVersion,
		BuilderTaskHeader:        changeplanmodel.RuntimeAuthoringBuilderTaskHeader, ScenarioIDHeader: changeplanmodel.RuntimeAuthoringScenarioIDHeader,
		ScenarioCategoriesHeader: changeplanmodel.RuntimeAuthoringScenarioCategoriesHeader, StepLabelHeader: changeplanmodel.RuntimeAuthoringStepLabelHeader,
		StepObservationHeader: changeplanmodel.RuntimeAuthoringStepObservationHeader, ExpectedStatusHeader: changeplanmodel.RuntimeAuthoringExpectedStatusHeader,
		SnapshotHashHeader: changeplanmodel.RuntimeAuthoringSnapshotHashHeader, CoverageHashHeader: changeplanmodel.RuntimeAuthoringCoverageHashHeader,
		StepReceiptHeader: changeplanmodel.RuntimeAuthoringStepReceiptHeader, EvidenceErrorHeader: changeplanmodel.RuntimeAuthoringEvidenceErrorHeader,
		EvidenceStepTokenHeader:  changeplanmodel.RuntimeAuthoringEvidenceStepTokenHeader,
		StreamingPathRestriction: changeplanmodel.RuntimeAuthoringEvidenceStreamingPathRestriction,
	}
}

func runtimeAuthoringApplyCoverageValidation(report *RuntimeAuthoringValidationReport, ledger *changeplanmodel.RuntimeAuthoringCoverageLedger, snapshot changeplanprojection.BusinessSystemSnapshot) {
	report.Coverage = changeplanvalidation.ValidateRuntimeAuthoringCoverage(ledger, snapshot.ChangePlanSnapshot())
	report.Binding.CoverageHash = runtimeAuthoringCoverageHash(ledger)
	report.Checks["coverage_ledger"] = "ok"
	if report.Coverage.Status != "complete" {
		report.Checks["coverage_ledger"] = "invalid"
	}
	for _, issue := range report.Coverage.Issues {
		report.Diagnostics = append(report.Diagnostics, RuntimeAuthoringValidationDiagnostic{Code: "backend.runtime.coverage_ledger_invalid", Owner: "businesssystem", CapabilityKey: "maintenance.current_state_snapshot", ResourcePath: "coverage", Message: issue, Repair: runtimeAuthoringCoverageRepair(issue, -1)})
	}
	for entryIndex, entry := range report.Coverage.Entries {
		for _, issue := range entry.Issues {
			report.Diagnostics = append(report.Diagnostics, RuntimeAuthoringValidationDiagnostic{Code: "backend.runtime.requirement_uncovered", Owner: "businesssystem", CapabilityKey: "maintenance.current_state_snapshot", ResourcePath: "coverage.requirements." + entry.RequirementID, Message: issue, Repair: runtimeAuthoringCoverageRepair(issue, entryIndex)})
		}
	}
	for _, resource := range report.Coverage.SourcelessResources {
		report.Diagnostics = append(report.Diagnostics, RuntimeAuthoringValidationDiagnostic{Code: "backend.runtime.resource_source_missing", Owner: "businesssystem", CapabilityKey: "maintenance.current_state_snapshot", ResourcePath: "resources." + resource.ResourceType + "." + resource.ResourceKey, Message: "resource has no authoritative source", Repair: &RuntimeAuthoringRepairFacts{Operation: "declare_resource_source", JSONPointer: "/resource_sources", ResourceType: resource.ResourceType, ResourceKey: resource.ResourceKey, ReferenceEndpoint: "/domain-system-snapshot?projection=resource"}})
	}
	for _, resource := range report.Coverage.UnreachableResources {
		report.Diagnostics = append(report.Diagnostics, RuntimeAuthoringValidationDiagnostic{Code: "backend.runtime.resource_unreachable", Owner: "businesssystem", CapabilityKey: "maintenance.current_state_snapshot", ResourcePath: "resources." + resource.ResourceType + "." + resource.ResourceKey, Message: "resource is not reachable from a requirement", Repair: &RuntimeAuthoringRepairFacts{Operation: "link_resource_to_requirement", JSONPointer: "/coverage/requirements", ResourceType: resource.ResourceType, ResourceKey: resource.ResourceKey, ReferenceEndpoint: "/domain-system-snapshot?projection=resource"}})
	}
}

func runtimeAuthoringCoverageRepair(issue string, entryIndex int) *RuntimeAuthoringRepairFacts {
	base := "/coverage"
	if entryIndex >= 0 {
		base += "/requirements/" + strconv.Itoa(entryIndex)
	}
	repair := &RuntimeAuthoringRepairFacts{Operation: "replace", JSONPointer: base}
	switch issue {
	case "coverage_ledger_required":
		repair.Expected = []string{changeplanmodel.RuntimeAuthoringCoverageLedgerVersion}
	case "coverage_version_invalid":
		repair.JSONPointer += "/version"
		repair.Expected = []string{changeplanmodel.RuntimeAuthoringCoverageLedgerVersion}
	case "requirements_required":
		repair.JSONPointer += "/requirements"
		repair.Operation, repair.Expected = "add", []string{"non_empty_array"}
	case "requirement_id_required", "requirement_id_duplicate":
		repair.JSONPointer += "/requirement_id"
		repair.Expected = []string{"unique_non_empty_string"}
	case "capability_required":
		repair.JSONPointer += "/capability_keys"
		repair.Operation, repair.Expected = "add", []string{"registered_capability_key"}
		repair.ReferenceEndpoint = "/tenant-admin/platform-capabilities/index"
	case "resource_required":
		repair.JSONPointer += "/resources"
		repair.Operation, repair.Expected = "add", []string{"snapshot_resource_reference"}
		repair.ReferenceEndpoint = "/domain-system-snapshot?projection=index"
	case "scenario_required":
		repair.JSONPointer += "/scenario_ids"
		repair.Operation, repair.Expected = "add", []string{"non_empty_scenario_id"}
	case "scenario_id_required":
		repair.JSONPointer += "/scenario_ids"
		repair.Expected = []string{"non_empty_string"}
	default:
		if value, found := strings.CutPrefix(issue, "capability_not_found:"); found {
			repair.JSONPointer += "/capability_keys"
			repair.Expected, repair.Actual, repair.CapabilityKey = []string{"registered_capability_key"}, value, value
			repair.ReferenceEndpoint = "/tenant-admin/platform-capabilities/capabilities/" + value
		} else if value, found := strings.CutPrefix(issue, "resource_not_found:"); found {
			parts := strings.SplitN(value, ":", 2)
			repair.JSONPointer += "/resources"
			repair.Expected, repair.Actual = []string{"snapshot_resource_reference"}, value
			if len(parts) == 2 {
				repair.ResourceType, repair.ResourceKey = parts[0], parts[1]
			}
			repair.ReferenceEndpoint = "/domain-system-snapshot?projection=resource"
		}
	}
	return repair
}

func runtimeAuthoringFinalizeValidation(report *RuntimeAuthoringValidationReport) {
	report.Valid = len(report.Diagnostics) == 0
	if !report.Valid {
		report.Status = "invalid"
		return
	}
	report.Status = "valid"
}

func runtimeAuthoringDefinitionDiagnostic(err error) RuntimeAuthoringValidationDiagnostic {
	params := apperror.ParamsOf(err)
	resourceType, resourceKey := strings.TrimSpace(params["resource_type"]), strings.TrimSpace(params["resource_key"])
	owner, capability := "metadata", "maintenance.current_state_snapshot"
	switch resourceType {
	case "object":
		capability = "schema.object"
	case "field":
		capability = "schema.field"
	case "action":
		owner, capability = "action", "action.definition"
	case "workflow":
		owner, capability = "workflow", "workflow.definition"
	case "scheduler":
		owner, capability = "scheduler", "scheduler.business_job"
	}
	path := strings.TrimSuffix(strings.ReplaceAll("definitions."+resourceType+"."+resourceKey, "..", "."), ".")
	message := valueOrDefault(strings.TrimSpace(params["diagnostic"]), "current Runtime definition graph is invalid")
	return RuntimeAuthoringValidationDiagnostic{Code: "backend.runtime.global_definition_invalid", Owner: owner, CapabilityKey: capability, ResourcePath: path, Message: message, Repair: &RuntimeAuthoringRepairFacts{Operation: "replace", JSONPointer: "/definitions/" + resourceType + "/" + resourceKey, ResourceType: resourceType, ResourceKey: resourceKey, ReferenceEndpoint: "/domain-system-snapshot?projection=resource"}}
}

var runtimeAuthoringRequiredConfigurationCategories = []string{
	"schema",
	"resource_sources",
	"runtime_state.automation",
	"runtime_state.integrations",
	"runtime_state.reports",
	"runtime_state.scheduler",
}

func runtimeAuthoringConfigurationCoverage(snapshot changeplanprojection.BusinessSystemSnapshot, diagnostics *[]RuntimeAuthoringValidationDiagnostic) map[string]string {
	checks := make(map[string]string, len(runtimeAuthoringRequiredConfigurationCategories))
	for _, category := range runtimeAuthoringRequiredConfigurationCategories {
		key := "configuration." + category
		if snapshot.ResourceVisibility[category] == "visible" {
			checks[key] = "ok"
			continue
		}
		checks[key] = "unavailable"
		*diagnostics = append(*diagnostics, RuntimeAuthoringValidationDiagnostic{
			Code:          "backend.runtime.configuration_snapshot_incomplete",
			Owner:         "changeplan",
			CapabilityKey: "maintenance.current_state_snapshot",
			ResourcePath:  key,
			Message:       "current Runtime configuration category is not visible: " + category,
			Repair:        &RuntimeAuthoringRepairFacts{Operation: "refresh_snapshot", JSONPointer: "/resource_visibility/" + category, Expected: []string{"visible"}, Actual: snapshot.ResourceVisibility[category], ReferenceEndpoint: "/domain-system-snapshot?projection=index"},
		})
	}
	return checks
}

func runtimeAuthoringCompleteManifest(current, base manifestmodel.ManifestSchema) manifestmodel.ManifestSchema {
	current.SchemaVersion = valueOrDefault(current.SchemaVersion, base.SchemaVersion)
	current.SourceBlueprintID = valueOrDefault(current.SourceBlueprintID, base.SourceBlueprintID)
	current.TargetAPIContractVersion = valueOrDefault(current.TargetAPIContractVersion, base.TargetAPIContractVersion)
	current.TargetAPIContractHash = valueOrDefault(current.TargetAPIContractHash, base.TargetAPIContractHash)
	current.AuthoringContractVersion = valueOrDefault(current.AuthoringContractVersion, base.AuthoringContractVersion)
	current.AuthoringContractHash = valueOrDefault(current.AuthoringContractHash, base.AuthoringContractHash)
	if base.SourceIntentCoverage != nil {
		current.SourceIntentCoverage = base.SourceIntentCoverage
	}
	current.Description = valueOrDefault(current.Description, base.Description)
	if len(base.I18n) > 0 {
		current.I18n = base.I18n
	}
	if len(base.NotificationTemplates) > 0 {
		current.NotificationTemplates = base.NotificationTemplates
	}
	if len(base.IdentityProfileExtensions) > 0 {
		current.IdentityProfileExtensions = base.IdentityProfileExtensions
	}
	if len(base.SeedRecords) > 0 {
		current.SeedRecords = base.SeedRecords
	}
	if len(base.AutomationExecutionSeeds) > 0 {
		current.AutomationExecutionSeeds = base.AutomationExecutionSeeds
	}
	if len(base.BusinessLoops) > 0 {
		current.BusinessLoops = base.BusinessLoops
	}
	if len(base.StateMachines) > 0 {
		current.StateMachines = base.StateMachines
	}
	if len(base.ValidationPlan) > 0 {
		current.ValidationPlan = base.ValidationPlan
	}
	return current
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func runtimeAuthoringValidationDiagnostic(path, message string) RuntimeAuthoringValidationDiagnostic {
	owner, capability := "manifest", ""
	switch {
	case strings.HasPrefix(path, "objects"):
		owner, capability = "metadata", "schema.object"
		if strings.Contains(path, ".fields") {
			capability = "schema.field"
		}
	case strings.HasPrefix(path, "actions"):
		owner, capability = "action", "action.definition"
	case strings.HasPrefix(path, "workflows"):
		owner, capability = "workflow", "workflow.definition"
	case strings.HasPrefix(path, "seed_records"):
		owner, capability = "seed", "seed.record"
	case strings.HasPrefix(path, "integrations"):
		owner, capability = "integration", "integration.connection"
	case strings.HasPrefix(path, "reports"):
		owner, capability = "report", "report.definition"
	}
	return RuntimeAuthoringValidationDiagnostic{Code: "backend.runtime.global_manifest_invalid", Owner: owner, CapabilityKey: capability, ResourcePath: path, Message: message, Repair: &RuntimeAuthoringRepairFacts{Operation: "replace", JSONPointer: "/" + strings.ReplaceAll(path, ".", "/"), ReferenceEndpoint: "/domain-system-snapshot?projection=resource"}}
}

func runtimeAuthoringValidationHash(manifest manifestmodel.ManifestSchema) string {
	raw, _ := json.Marshal(manifest)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func runtimeAuthoringConfigurationHash(manifest manifestmodel.ManifestSchema, snapshot changeplanprojection.BusinessSystemSnapshot) string {
	raw, _ := json.Marshal(struct {
		Manifest manifestmodel.ManifestSchema                `json:"manifest"`
		Snapshot changeplanprojection.BusinessSystemSnapshot `json:"snapshot"`
	}{Manifest: manifest, Snapshot: snapshot})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func runtimeAuthoringApplyGlobalChecks(report *RuntimeAuthoringValidationReport, snapshot changeplanprojection.BusinessSystemSnapshot) {
	semanticStatus := "ok"
	if report.Checks["definition_graph"] != "ok" || report.Checks["manifest"] != "ok" {
		semanticStatus = "invalid"
	}
	for _, key := range []string{"cross_resource_references", "cycles", "permission_closure", "foundation_usage"} {
		report.Checks[key] = semanticStatus
	}
	report.Checks["connector_readiness"] = "ok"
	for _, connection := range snapshot.RuntimeState.Connections {
		if strings.EqualFold(strings.TrimSpace(connection.Status), "active") && !connection.Ready {
			report.Checks["connector_readiness"] = "invalid"
			report.Diagnostics = append(report.Diagnostics, RuntimeAuthoringValidationDiagnostic{
				Code: "backend.integration.connector.adapter_not_ready", Owner: "integration", CapabilityKey: "integration.connection",
				ResourcePath: "runtime_state.connections." + connection.Key, Message: "active Integration connection is not ready",
				Repair: &RuntimeAuthoringRepairFacts{Operation: "restore_readiness", JSONPointer: "/runtime_state/connections/" + connection.Key + "/ready", Expected: []string{"true"}, Actual: "false"},
			})
		}
	}

}
