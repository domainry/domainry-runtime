package businesssystem

// This file assembles the cross-owner business-system projection.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	apperror "github.com/domainry/domainry-foundation/apperror"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemaservice "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
	appschemavalidation "github.com/domainry/domainry-runtime/runtime/domain/appschema/validation"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	changeplanvalidation "github.com/domainry/domainry-runtime/runtime/domain/changeplan/validation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

const (
	ActionBusinessSystemSnapshot         = "runtime.businesssystem.business_system_snapshot"
	ActionValidateRuntimeAuthoring       = "runtime.businesssystem.validate_runtime_authoring"
	ActionVerifyRuntimeAuthoringDelivery = "runtime.businesssystem.verify_runtime_authoring_delivery"
)

type BusinessSystemApplicationDependencies struct {
	FeaturePermissions func(context.Context, principalmodel.Principal) (recordcontract.RecordFeaturePermissionSnapshot, error)
	SchemaForPrincipal func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot
	Definitions        metadatasdk.Definitions
	Evidence           changeplanrepository.ChangePlanEvidenceRepository
	Runtime            BusinessSystemRuntimeProjectionDependencies
}

// businessSystemSnapshotPorts contains only the owner reads required to build
// the governed business-system snapshot.
type businessSystemSnapshotPorts struct {
	featurePermissions  func(context.Context, principalmodel.Principal) (recordcontract.RecordFeaturePermissionSnapshot, error)
	schemaForPrincipal  func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot
	metadataDefinitions metadatasdk.Definitions
}

// BusinessSystemApplicationService is the cross-domain snapshot composition
// root. Snapshot shapes and normalization remain owned by domain/changeplan.
type BusinessSystemApplicationService struct {
	snapshotProjection businessSystemSnapshotPorts
	evidence           changeplanrepository.ChangePlanEvidenceRepository
	runtimeProjection  businessRuntimeProjectionPorts
}

func NewBusinessSystemApplicationService(dependencies BusinessSystemApplicationDependencies) *BusinessSystemApplicationService {
	return &BusinessSystemApplicationService{
		snapshotProjection: businessSystemSnapshotPorts{
			featurePermissions:  dependencies.FeaturePermissions,
			schemaForPrincipal:  dependencies.SchemaForPrincipal,
			metadataDefinitions: dependencies.Definitions,
		},
		evidence:          dependencies.Evidence,
		runtimeProjection: newBusinessSystemRuntimeProjectionPorts(dependencies.Runtime),
	}
}

func (s *BusinessSystemApplicationService) SetEvidenceRepository(repository changeplanrepository.ChangePlanEvidenceRepository) {
	if s != nil {
		s.evidence = repository
	}
}

func (s *BusinessSystemApplicationService) Snapshot(ctx context.Context, principal principalmodel.Principal) (changeplanprojection.BusinessSystemSnapshot, error) {
	if !principal.Known {
		return changeplanprojection.BusinessSystemSnapshot{}, businessSystemForbidden("auth.permission_denied")
	}
	permissions, err := s.snapshotProjection.featurePermissions(ctx, principal)
	if err != nil {
		return changeplanprojection.BusinessSystemSnapshot{}, err
	}
	authoring := businessSystemRuntimeAuthoringCapabilities()
	capabilityKeys := []string{}
	for _, capabilityDomain := range authoring.Domains {
		for _, capability := range capabilityDomain.Capabilities {
			capabilityKeys = append(capabilityKeys, capability.Key)
		}
	}
	schema := s.snapshotProjection.schemaForPrincipal(ctx, principal)
	snapshot := changeplanprojection.BusinessSystemSnapshot{
		SnapshotVersion: changeplanprojection.BusinessSystemSnapshotVersion, RuntimeVersion: capabilitycontract.RuntimeCapabilityContractVersion,
		AuthoringContractVersion: authoring.ContractVersion, AuthoringContractHash: authoring.ContractHash,
		SchemaHash: schema.SchemaHash, Schema: schema, EffectivePermissions: permissions,
		ResourceSources: []changeplanprojection.SystemResourceSource{}, RuntimeState: changeplanprojection.BusinessRuntimeStateSnapshot{},
		SeedRecords:              []businessseedmodel.BusinessSeedProvenance{},
		ObjectRecordCounts:       map[string]int{},
		HiddenResourceCategories: []string{},
		ResourceVisibility:       baseBusinessSnapshotVisibility(),
		CapabilityKeys:           capabilityKeys,
	}
	if principal.HasExactPermission(ActionBusinessSystemSnapshot) {
		if err := s.addBusinessAdministratorSnapshotFacts(ctx, &snapshot, principal); err != nil {
			return changeplanprojection.BusinessSystemSnapshot{}, err
		}
	} else {
		s.addLimitedBusinessSnapshotFacts(ctx, &snapshot, principal)
	}
	// Other snapshot projections can traverse shared runtime schema values while
	// this aggregate is assembled. Seal the nested schema hash from the exact
	// projection that will be returned before calculating the system hash.
	snapshot.Schema.SchemaHash = appschemaservice.SchemaSnapshotHash(snapshot.Schema)
	snapshot.Schema.SnapshotVersion = snapshot.Schema.SchemaHash
	snapshot.SchemaHash = snapshot.Schema.SchemaHash
	snapshot.Finalize()
	return snapshot, nil
}

// SnapshotIndex builds the default model-facing projection without first
// loading effective permissions, operational records, workflow history, or
// per-object record counts. Those facts remain available through explicit
// projections, while the default path scales with schema and source metadata
// rather than live business data.
func (s *BusinessSystemApplicationService) SnapshotIndex(ctx context.Context, principal principalmodel.Principal) (changeplanprojection.BusinessSystemSnapshotIndex, error) {
	if !principal.Known {
		return changeplanprojection.BusinessSystemSnapshotIndex{}, businessSystemForbidden("auth.permission_denied")
	}
	authoring := businessSystemRuntimeAuthoringCapabilities()
	capabilityKeys := make([]string, 0)
	for _, capabilityDomain := range authoring.Domains {
		for _, capability := range capabilityDomain.Capabilities {
			capabilityKeys = append(capabilityKeys, capability.Key)
		}
	}
	schema := s.snapshotProjection.schemaForPrincipal(ctx, principal)
	schema.SchemaHash = appschemaservice.SchemaSnapshotHash(schema)
	visibility := map[string]string{"schema": "summarized"}
	hidden := []string{}
	resources := []changeplanprojection.SystemResourceSource{}
	seeds := []businessseedmodel.BusinessSeedProvenance{}
	if principal.HasExactPermission(ActionBusinessSystemSnapshot) {
		var err error
		resources, err = s.businessResourceSources(ctx, principal)
		if err != nil {
			return changeplanprojection.BusinessSystemSnapshotIndex{}, err
		}
		if s.evidence != nil {
			seeds, err = s.evidence.ListSeedProvenance(ctx)
			if err != nil {
				return changeplanprojection.BusinessSystemSnapshotIndex{}, businessSystemInternalError("list domain seed provenance", err)
			}
		}
		visibility["resource_sources"] = "summarized"
		visibility["object_record_counts"] = "available_on_demand"
		for _, category := range []string{"runtime_state.automation", "runtime_state.integrations", "runtime_state.reports", "runtime_state.scheduler"} {
			visibility[category] = "available_on_demand"
		}
	} else {
		for _, category := range businessSnapshotGovernanceCategories {
			visibility[category] = "hidden"
			hidden = append(hidden, category)
		}
	}
	return (changeplanprojection.BusinessSystemSnapshot{
		RuntimeVersion:           capabilitycontract.RuntimeCapabilityContractVersion,
		AuthoringContractVersion: authoring.ContractVersion, AuthoringContractHash: authoring.ContractHash,
		SchemaHash: schema.SchemaHash, ResourceSources: resources, SeedRecords: seeds,
		CapabilityKeys: capabilityKeys, HiddenResourceCategories: hidden, ResourceVisibility: visibility,
	}).CompactIndex(), nil
}

func (s *BusinessSystemApplicationService) SnapshotResourcePage(ctx context.Context, principal principalmodel.Principal, resourceType, cursor string, limit int) (changeplanprojection.BusinessSystemResourcePage, bool, error) {
	if err := authorizeBusinessSystemSnapshotDetail(principal); err != nil {
		return changeplanprojection.BusinessSystemResourcePage{}, false, err
	}
	resources, err := s.businessResourceSources(ctx, principal)
	if err != nil {
		return changeplanprojection.BusinessSystemResourcePage{}, false, err
	}
	page, valid := changeplanprojection.ProjectBusinessSystemResourcePage(resources, resourceType, cursor, limit)
	return page, valid, nil
}

func (s *BusinessSystemApplicationService) SnapshotResourceDetail(ctx context.Context, principal principalmodel.Principal, resourceType, resourceKey string) (changeplanprojection.BusinessSystemResourceDetail, bool, error) {
	if err := authorizeBusinessSystemSnapshotDetail(principal); err != nil {
		return changeplanprojection.BusinessSystemResourceDetail{}, false, err
	}
	resourceType, resourceKey = strings.TrimSpace(resourceType), strings.TrimSpace(resourceKey)
	if resourceType == "" || resourceKey == "" {
		return changeplanprojection.BusinessSystemResourceDetail{}, false, nil
	}
	if s.snapshotProjection.metadataDefinitions == nil {
		return changeplanprojection.BusinessSystemResourceDetail{}, false, businessSystemInternalError("get Metadata definition", nil)
	}
	definition, found, err := s.snapshotProjection.metadataDefinitions.Get(ctx, resourceType, resourceKey)
	if err != nil {
		return changeplanprojection.BusinessSystemResourceDetail{}, false, err
	}
	if !found {
		return changeplanprojection.BusinessSystemResourceDetail{}, false, nil
	}
	source := businessSystemResourceSource(definition)
	detail := changeplanprojection.ProjectBusinessSystemResourceDetail("", source, definition.Payload)
	return detail, true, nil
}

func (s *BusinessSystemApplicationService) SnapshotRuntimeStateIndex(ctx context.Context, principal principalmodel.Principal) (changeplanprojection.BusinessSystemRuntimeStateIndex, error) {
	if err := authorizeBusinessSystemSnapshotDetail(principal); err != nil {
		return changeplanprojection.BusinessSystemRuntimeStateIndex{}, err
	}
	state, err := s.RuntimeStateSnapshot(ctx, principal)
	if err != nil {
		return changeplanprojection.BusinessSystemRuntimeStateIndex{}, err
	}
	return changeplanprojection.ProjectBusinessSystemRuntimeStateIndex(state), nil
}

func authorizeBusinessSystemSnapshotDetail(principal principalmodel.Principal) error {
	if !principal.Known || !principal.HasExactPermission(ActionBusinessSystemSnapshot) {
		return businessSystemForbidden("auth.permission_denied")
	}
	return nil
}

func (s *BusinessSystemApplicationService) businessResourceSources(ctx context.Context, principal principalmodel.Principal) ([]changeplanprojection.SystemResourceSource, error) {
	_ = principal
	if s.snapshotProjection.metadataDefinitions == nil {
		return nil, businessSystemInternalError("list Metadata definitions", nil)
	}
	definitions, err := s.snapshotProjection.metadataDefinitions.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, resourceType := range appschemavalidation.ApplicationSchemaBusinessResourceTypes() {
		allowed[resourceType] = true
	}
	items := make([]changeplanprojection.SystemResourceSource, 0, len(definitions.Definitions))
	for _, definition := range definitions.Definitions {
		if allowed[definition.ResourceType] {
			items = append(items, businessSystemResourceSource(definition))
		}
	}
	return items, nil
}

func businessSystemResourceSource(definition metadatasdk.Definition) changeplanprojection.SystemResourceSource {
	return changeplanprojection.SystemResourceSource{
		ResourceType: definition.ResourceType, ResourceKey: definition.ResourceKey, ObjectKey: definition.ObjectKey,
		Name: definition.Name, SchemaVersion: definition.SchemaVersion, SchemaHash: definition.SchemaHash,
		SourceKind: businessSystemValueOrDefault(strings.TrimSpace(definition.SourceKind), "unknown"), SourceID: definition.SourceID,
		Disabled: strings.TrimSpace(definition.DisabledAt) != "",
	}
}

var businessSnapshotGovernanceCategories = []string{"object_record_counts", "resource_sources", "runtime_state.automation", "runtime_state.integrations", "runtime_state.reports", "runtime_state.scheduler"}

func baseBusinessSnapshotVisibility() map[string]string {
	return map[string]string{"schema": "visible"}
}

func (s *BusinessSystemApplicationService) addBusinessAdministratorSnapshotFacts(ctx context.Context, snapshot *changeplanprojection.BusinessSystemSnapshot, principal principalmodel.Principal) error {
	sources, err := s.businessResourceSources(ctx, principal)
	if err != nil {
		return err
	}
	seeds := []businessseedmodel.BusinessSeedProvenance{}
	if s.evidence != nil {
		seeds, err = s.evidence.ListSeedProvenance(ctx)
		if err != nil {
			return businessSystemInternalError("list domain seed provenance", err)
		}
	}
	runtimeState, err := s.RuntimeStateSnapshot(ctx, principal)
	if err != nil {
		return err
	}
	recordCounts, err := s.businessObjectRecordCounts(ctx, principal)
	if err != nil {
		return err
	}
	snapshot.ResourceSources, snapshot.SeedRecords, snapshot.RuntimeState, snapshot.ObjectRecordCounts = sources, seeds, runtimeState, recordCounts
	for _, category := range businessSnapshotGovernanceCategories {
		snapshot.ResourceVisibility[category] = "visible"
	}
	return nil
}

func (s *BusinessSystemApplicationService) businessObjectRecordCounts(ctx context.Context, principal principalmodel.Principal) (map[string]int, error) {
	counts := map[string]int{}
	for _, object := range s.snapshotProjection.schemaForPrincipal(ctx, principal).Objects {
		if businessSystemRuntimeOwnedObject(object) {
			continue
		}
		page, err := s.runtimeProjection.listRecords(ctx, object.Key, recordmodel.RecordListQuery{Page: 1, PageSize: 1}, principal)
		if err != nil {
			return nil, err
		}
		counts[object.Key] = page.Total
	}
	return counts, nil
}

func businessSystemRuntimeOwnedObject(object definitionmodel.ObjectSchema) bool {
	for _, key := range []string{"runtime_owned", "system_object"} {
		if owned, _ := object.Config[key].(bool); owned {
			return true
		}
	}
	return false
}

func (s *BusinessSystemApplicationService) addLimitedBusinessSnapshotFacts(ctx context.Context, snapshot *changeplanprojection.BusinessSystemSnapshot, principal principalmodel.Principal) {
	for _, category := range businessSnapshotGovernanceCategories {
		snapshot.ResourceVisibility[category] = "hidden"
		snapshot.HiddenResourceCategories = append(snapshot.HiddenResourceCategories, category)
	}
}

func removeBusinessSnapshotCategory(categories []string, target string) []string {
	result := categories[:0]
	for _, category := range categories {
		if category != target {
			result = append(result, category)
		}
	}
	return result
}

func businessSystemForbidden(code string) error {
	return apperror.New(apperror.KindForbidden, code, nil, nil)
}

func businessSystemInternalError(operation string, err error) error {
	return apperror.New(apperror.KindInternal, "backend.internal", err, map[string]string{"operation": operation})
}

func businessSystemValueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func (s *RuntimeAuthoringValidationApplicationService) VerifyDelivery(ctx context.Context, principal principalmodel.Principal, evidence changeplanmodel.RuntimeAuthoringDeliveryEvidence) (changeplanmodel.RuntimeAuthoringDeliveryReport, error) {
	validation, err := s.ValidateWithCoverage(ctx, principal, &evidence.Coverage)
	if err != nil {
		return changeplanmodel.RuntimeAuthoringDeliveryReport{}, err
	}
	trustedEvidence, receiptIssues := s.runtimeVerifiedDeliveryEvidence(evidence, validation.Binding, operationscontract.BuilderTaskID(ctx))
	report := changeplanvalidation.ValidateRuntimeAuthoringDelivery(trustedEvidence, validation.Binding, validation.Valid)
	report.Checks["runtime_evidence"] = "ok"
	if len(receiptIssues) > 0 {
		report.Checks["runtime_evidence"] = "invalid"
		for _, issue := range receiptIssues {
			report.Issues = append(report.Issues, "runtime_evidence."+issue)
		}
		report.Valid = false
		report.Status = "invalid"
	}
	return report, nil
}

func (s *RuntimeAuthoringValidationApplicationService) runtimeVerifiedDeliveryEvidence(evidence changeplanmodel.RuntimeAuthoringDeliveryEvidence, binding changeplanmodel.RuntimeAuthoringEvidenceBinding, builderTaskID string) (changeplanmodel.RuntimeAuthoringDeliveryEvidence, []string) {
	trusted := changeplanmodel.RuntimeAuthoringDeliveryEvidence{
		Version: changeplanmodel.RuntimeAuthoringDeliveryEvidenceVersion,
		Binding: binding, Coverage: evidence.Coverage,
		Scenarios: []changeplanmodel.RuntimeAuthoringScenarioEvidence{},
	}
	if s == nil || s.dependencies.ScenarioReceipts == nil {
		return trusted, []string{"receipt_verifier_unavailable"}
	}
	if strings.TrimSpace(builderTaskID) == "" {
		return trusted, []string{"builder_task_required"}
	}
	type scenarioBuild struct {
		evidence       changeplanmodel.RuntimeAuthoringScenarioEvidence
		categories     map[string]bool
		beforeObserved bool
		afterObserved  bool
	}
	newBuild := func(scenarioID string) *scenarioBuild {
		return &scenarioBuild{evidence: changeplanmodel.RuntimeAuthoringScenarioEvidence{
			Version:    changeplanmodel.RuntimeAuthoringScenarioEvidenceVersion,
			ScenarioID: strings.TrimSpace(scenarioID), Categories: []string{}, Steps: []changeplanmodel.RuntimeAuthoringScenarioStepEvidence{},
		}, categories: map[string]bool{}}
	}
	issues := []string{}
	seenReceipts, seenSteps := map[string]bool{}, map[string]bool{}
	sessionID := ""
	verifyReceipt := func(token, prefix string, sessionRequired bool) (RuntimeAuthoringScenarioStepReceipt, bool) {
		token = strings.TrimSpace(token)
		if token == "" {
			issues = append(issues, prefix+".receipt_required")
			return RuntimeAuthoringScenarioStepReceipt{}, false
		}
		if seenReceipts[token] {
			issues = append(issues, prefix+".receipt_reused")
			return RuntimeAuthoringScenarioStepReceipt{}, false
		}
		seenReceipts[token] = true
		receipt, verifyErr := s.dependencies.ScenarioReceipts.Verify(token, builderTaskID, binding)
		if verifyErr != nil {
			issues = append(issues, prefix+".receipt_invalid")
			return RuntimeAuthoringScenarioStepReceipt{}, false
		}
		if sessionRequired && (receipt.SessionID == "" || receipt.StepID == "") {
			issues = append(issues, prefix+".evidence_session_required")
			return RuntimeAuthoringScenarioStepReceipt{}, false
		}
		if receipt.StepID != "" {
			if seenSteps[receipt.StepID] {
				issues = append(issues, prefix+".step_reused")
				return RuntimeAuthoringScenarioStepReceipt{}, false
			}
			seenSteps[receipt.StepID] = true
		}
		if receipt.SessionID != "" {
			if sessionID == "" {
				sessionID = receipt.SessionID
			} else if sessionID != receipt.SessionID {
				issues = append(issues, prefix+".session_mismatch")
				return RuntimeAuthoringScenarioStepReceipt{}, false
			}
		}
		return receipt, true
	}
	appendReceipt := func(build *scenarioBuild, receipt RuntimeAuthoringScenarioStepReceipt, token, prefix string) {
		build.evidence.Steps = append(build.evidence.Steps, changeplanmodel.RuntimeAuthoringScenarioStepEvidence{
			SessionID: receipt.SessionID, StepID: receipt.StepID,
			Label: receipt.Label, Observation: receipt.Observation, Method: receipt.Method, Path: receipt.Path,
			ExpectedStatus: append([]int(nil), receipt.ExpectedStatus...), ActualStatus: receipt.ActualStatus,
			RequestHash: receipt.RequestHash, ResponseHash: receipt.ResponseHash,
			IdempotencyKey: receipt.IdempotencyKey, IdempotencyReplayed: receipt.IdempotencyReplayed,
			Passed: runtimeAuthoringObservedStatusExpected(receipt.ActualStatus, receipt.ExpectedStatus), RuntimeReceipt: token,
		})
		for _, category := range receipt.Categories {
			build.categories[category] = true
		}
		switch receipt.Observation {
		case "before_state":
			if build.beforeObserved {
				issues = append(issues, prefix+".before_state_duplicate")
			}
			build.beforeObserved = true
			build.evidence.BeforeStateHash = receipt.ResponseHash
		case "after_state":
			if build.afterObserved {
				issues = append(issues, prefix+".after_state_duplicate")
			}
			build.afterObserved = true
			build.evidence.AfterStateHash = receipt.ResponseHash
		}
	}
	builds := []*scenarioBuild{}
	if len(evidence.Receipts) > 0 {
		if len(evidence.Scenarios) > 0 {
			issues = append(issues, "mixed_evidence_formats")
		}
		byScenario := map[string]*scenarioBuild{}
		for receiptIndex, token := range evidence.Receipts {
			prefix := "receipts[" + strconv.Itoa(receiptIndex) + "]"
			receipt, verified := verifyReceipt(token, prefix, true)
			if !verified {
				continue
			}
			build := byScenario[receipt.ScenarioID]
			if build == nil {
				build = newBuild(receipt.ScenarioID)
				byScenario[receipt.ScenarioID] = build
				builds = append(builds, build)
			}
			appendReceipt(build, receipt, token, prefix)
		}
	} else {
		for scenarioIndex, submittedScenario := range evidence.Scenarios {
			build := newBuild(submittedScenario.ScenarioID)
			builds = append(builds, build)
			if strings.TrimSpace(submittedScenario.Version) != changeplanmodel.RuntimeAuthoringScenarioEvidenceVersion {
				issues = append(issues, "scenarios["+strconv.Itoa(scenarioIndex)+"].version_invalid")
			}
			for stepIndex, submittedStep := range submittedScenario.Steps {
				prefix := "scenarios[" + strconv.Itoa(scenarioIndex) + "].steps[" + strconv.Itoa(stepIndex) + "]"
				receipt, verified := verifyReceipt(submittedStep.RuntimeReceipt, prefix, false)
				if !verified {
					continue
				}
				if receipt.ScenarioID != build.evidence.ScenarioID {
					issues = append(issues, prefix+".scenario_mismatch")
					continue
				}
				appendReceipt(build, receipt, submittedStep.RuntimeReceipt, prefix)
			}
		}
	}
	for _, build := range builds {
		for _, category := range changeplanmodel.RuntimeAuthoringRequiredScenarioCategories {
			if build.categories[category] {
				build.evidence.Categories = append(build.evidence.Categories, category)
			}
		}
		build.evidence.Passed = len(build.evidence.Steps) > 0
		for _, step := range build.evidence.Steps {
			if !step.Passed {
				build.evidence.Passed = false
			}
		}
		trusted.Scenarios = append(trusted.Scenarios, build.evidence)
	}
	return trusted, issues
}

func runtimeAuthoringObservedStatusExpected(actual int, expected []int) bool {
	for _, status := range expected {
		if actual == status {
			return true
		}
	}
	return false
}

func runtimeAuthoringEvidenceBinding(snapshot changeplanprojection.BusinessSystemSnapshot, configurationHash string) changeplanmodel.RuntimeAuthoringEvidenceBinding {
	binding := changeplanmodel.RuntimeAuthoringEvidenceBinding{
		RuntimeVersion: businessSystemValueOrDefault(snapshot.RuntimeVersion, snapshot.RuntimeMetadata.RuntimeVersion),
		ContractHash:   snapshot.AuthoringContractHash, InstanceHash: snapshot.SnapshotHash, SnapshotHash: configurationHash, ResourceHashes: map[string]string{},
	}
	for _, resource := range snapshot.ResourceSources {
		if resource.Disabled {
			continue
		}
		hash := strings.TrimSpace(resource.SchemaHash)
		if hash == "" {
			raw, _ := json.Marshal(resource)
			sum := sha256.Sum256(raw)
			hash = hex.EncodeToString(sum[:])
		}
		binding.ResourceHashes[strings.TrimSpace(resource.ResourceType)+":"+strings.TrimSpace(resource.ResourceKey)] = hash
	}
	return binding
}

func runtimeAuthoringCoverageHash(ledger *changeplanmodel.RuntimeAuthoringCoverageLedger) string {
	if ledger == nil {
		return ""
	}
	raw, _ := json.Marshal(ledger)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func businessSystemPrincipalWorkspaceID(principal principalmodel.Principal) string {
	return auditapplication.AuditPrincipalWorkspaceID(principal)
}

func businessSystemRuntimeAuthoringCapabilities() capabilitycontract.CapabilityRuntimeAuthoringContract {
	return capabilityapplication.RuntimeAuthoringCapabilities()
}
