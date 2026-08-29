package businesssystem

import deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
import recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"

// This file assembles the cross-owner business-system projection.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/audit"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	changeplanvalidation "github.com/domainry/domainry-runtime/runtime/domain/changeplan/validation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadataservice "github.com/domainry/domainry-runtime/runtime/domain/metadata/service"
	metadatavalidation "github.com/domainry/domainry-runtime/runtime/domain/metadata/validation"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"

	apperror "github.com/domainry/domainry-foundation/apperror"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

type BusinessSystemApplicationDependencies struct {
	FeaturePermissions  func(context.Context, principalmodel.Principal) (recordcontract.RecordFeaturePermissionSnapshot, error)
	SchemaForPrincipal  func(context.Context, principalmodel.Principal) metadatamodel.ApplicationSchemaSnapshot
	MetadataDefinitions func(context.Context, string, string, principalmodel.Principal) ([]metadatamodel.MetadataDefinition, error)
	FrontendSnapshot    func(context.Context, principalmodel.Principal) (deploymentmodel.FrontendCapabilitySnapshot, error)
	Evidence            changeplanrepository.ChangePlanEvidenceRepository
	Runtime             BusinessSystemRuntimeProjectionDependencies
}

// businessSystemSnapshotPorts contains only the owner reads required to build
// the governed business-system snapshot.
type businessSystemSnapshotPorts struct {
	featurePermissions  func(context.Context, principalmodel.Principal) (recordcontract.RecordFeaturePermissionSnapshot, error)
	schemaForPrincipal  func(context.Context, principalmodel.Principal) metadatamodel.ApplicationSchemaSnapshot
	metadataDefinitions func(context.Context, string, string, principalmodel.Principal) ([]metadatamodel.MetadataDefinition, error)
	frontendSnapshot    func(context.Context, principalmodel.Principal) (deploymentmodel.FrontendCapabilitySnapshot, error)
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
			metadataDefinitions: dependencies.MetadataDefinitions,
			frontendSnapshot:    dependencies.FrontendSnapshot,
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
		FrontendCapabilities:     changeplanmodel.FrontendCapabilities{Status: "unknown", MissingFrontendSupport: []changeplanmodel.FrontendRequirement{}, StaleFrontendSupport: []changeplanmodel.FrontendSupportEntry{}},
		SeedRecords:              []businessseedmodel.BusinessSeedProvenance{},
		ObjectRecordCounts:       map[string]int{},
		HiddenResourceCategories: []string{},
		ResourceVisibility:       baseBusinessSnapshotVisibility(),
		CapabilityKeys:           capabilityKeys,
	}
	if principal.HasPermission("workspace.admin") {
		if err := s.addBusinessAdministratorSnapshotFacts(ctx, &snapshot, principal); err != nil {
			return changeplanprojection.BusinessSystemSnapshot{}, err
		}
	} else {
		s.addLimitedBusinessSnapshotFacts(ctx, &snapshot, principal)
	}
	// Other snapshot projections can traverse shared runtime schema values while
	// this aggregate is assembled. Seal the nested schema hash from the exact
	// projection that will be returned before calculating the system hash.
	snapshot.Schema.SchemaHash = metadataservice.SchemaSnapshotHash(snapshot.Schema)
	snapshot.Schema.SnapshotVersion = snapshot.Schema.SchemaHash
	snapshot.SchemaHash = snapshot.Schema.SchemaHash
	snapshot.Finalize()
	return snapshot, nil
}

func (s *BusinessSystemApplicationService) businessResourceSources(ctx context.Context, principal principalmodel.Principal) ([]changeplanprojection.SystemResourceSource, error) {
	items := []changeplanprojection.SystemResourceSource{}
	for _, resourceType := range metadatavalidation.MetadataBusinessResourceTypes() {
		definitions, err := s.snapshotProjection.metadataDefinitions(ctx, resourceType, businessSystemPrincipalWorkspaceID(principal), principal)
		if err != nil {
			return nil, err
		}
		for _, definition := range definitions {
			items = append(items, changeplanprojection.SystemResourceSource{
				ResourceType: definition.ResourceType, ResourceKey: definition.ResourceKey, ObjectKey: definition.ObjectKey,
				Name: definition.Name, SchemaVersion: definition.SchemaVersion, SchemaHash: definition.SchemaHash,
				SourceKind: businessSystemValueOrDefault(strings.TrimSpace(definition.SourceKind), "unknown"), SourceID: definition.SourceID,
				Disabled: strings.TrimSpace(definition.DisabledAt) != "",
			})
		}
	}
	return items, nil
}

var businessSnapshotGovernanceCategories = []string{"frontend_capabilities", "object_record_counts", "resource_sources", "runtime_state.automation", "runtime_state.integrations", "runtime_state.reports", "runtime_state.scheduler", "seed_records"}

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
	frontend, err := s.snapshotProjection.frontendSnapshot(ctx, principal)
	if err != nil {
		return err
	}
	snapshot.FrontendCapabilities = businessSystemFrontendCapabilities(frontend)
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
	return changeplanvalidation.ValidateRuntimeAuthoringDelivery(evidence, validation.Binding, validation.Valid), nil
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

func businessSystemFrontendCapabilities(snapshot deploymentmodel.FrontendCapabilitySnapshot) changeplanmodel.FrontendCapabilities {
	result := changeplanmodel.FrontendCapabilities{Revision: snapshot.Revision, UpdatedAt: snapshot.UpdatedAt, Status: snapshot.Status, ManifestHash: snapshot.ManifestHash}
	for _, requirement := range snapshot.MissingFrontendSupport {
		result.MissingFrontendSupport = append(result.MissingFrontendSupport, changeplanmodel.FrontendRequirement{CapabilityKey: requirement.CapabilityKey, SupportKey: requirement.SupportKey})
	}
	result.StaleFrontendSupport = businessSystemFrontendEntries(snapshot.StaleFrontendSupport)
	if snapshot.Manifest == nil {
		return result
	}
	result.Manifest = &changeplanmodel.FrontendManifest{
		ManifestVersion: snapshot.Manifest.ManifestVersion, FrontendVersion: snapshot.Manifest.FrontendVersion,
		RuntimeContractVersions: append([]string(nil), snapshot.Manifest.RuntimeContractVersions...), Entries: businessSystemFrontendEntries(snapshot.Manifest.Entries),
	}
	if evidence := snapshot.Manifest.DeploymentEvidence; evidence != nil {
		result.Manifest.DeploymentEvidence = &changeplanmodel.FrontendDeploymentEvidence{
			AuditContractVersion: evidence.AuditContractVersion, DesignContractHash: evidence.DesignContractHash,
			RouteRegistryHash: evidence.RouteRegistryHash, FrontendSourceHash: evidence.FrontendSourceHash, AuditArtifactHash: evidence.AuditArtifactHash,
		}
	}
	return result
}

func businessSystemFrontendEntries(source []deploymentmodel.FrontendCapabilitySupportEntry) []changeplanmodel.FrontendSupportEntry {
	entries := make([]changeplanmodel.FrontendSupportEntry, 0, len(source))
	for _, entry := range source {
		entries = append(entries, changeplanmodel.FrontendSupportEntry{
			SupportKey: entry.SupportKey, CapabilityKeys: append([]string(nil), entry.CapabilityKeys...), Route: entry.Route,
			RequiredPermissions: append([]string(nil), entry.RequiredPermissions...), FeatureModule: entry.FeatureModule,
			AcceptanceTests: append([]string(nil), entry.AcceptanceTests...), ActorRoles: append([]string(nil), entry.ActorRoles...),
			BusinessObjects: append([]string(nil), entry.BusinessObjects...), ViewKeys: append([]string(nil), entry.ViewKeys...),
			ImplementedActions: append([]string(nil), entry.ImplementedActions...), ReportKeys: append([]string(nil), entry.ReportKeys...),
			FieldKeys: append([]string(nil), entry.FieldKeys...), AcceptanceClaims: append([]string(nil), entry.AcceptanceClaims...),
		})
	}
	return entries
}
