package deployment

import deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	deploymentrepository "github.com/domainry/domainry-runtime/runtime/domain/deployment/repository"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type FrontendBusinessBindings struct {
	Objects               map[string]bool
	Views                 map[string]bool
	Actions               map[string]bool
	Reports               map[string]bool
	Fields                map[string]bool
	SchemaHash            string
	SchemaSnapshotVersion string
}

type FrontendCapabilityDependencies struct {
	Repository       deploymentrepository.DeploymentFrontendCapabilityRepository
	ContractVersion  string
	Capabilities     func() []deploymentmodel.FrontendCapabilityDefinition
	BusinessBindings func(context.Context) FrontendBusinessBindings
}

// DeploymentFrontendCapabilityApplicationService owns the target frontend support registration and
// compatibility projection. It is independent from schema/runtime mutable maps.
// DeploymentFrontendCapabilityApplicationService evaluates deployed frontend capabilities.
type DeploymentFrontendCapabilityApplicationService struct {
	repository       deploymentrepository.DeploymentFrontendCapabilityRepository
	fallback         deploymentrepository.DeploymentFrontendCapabilityRepository
	contractVersion  string
	capabilities     func() []deploymentmodel.FrontendCapabilityDefinition
	businessBindings func(context.Context) FrontendBusinessBindings
}

func NewDeploymentFrontendCapabilityApplicationService(dependencies ...FrontendCapabilityDependencies) *DeploymentFrontendCapabilityApplicationService {
	deps := FrontendCapabilityDependencies{}
	if len(dependencies) > 0 {
		deps = dependencies[0]
	}
	if deps.ContractVersion == "" {
		deps.ContractVersion = "runtime-authoring-v1"
	}
	if deps.Capabilities == nil {
		deps.Capabilities = func() []deploymentmodel.FrontendCapabilityDefinition { return nil }
	}
	return &DeploymentFrontendCapabilityApplicationService{repository: deps.Repository, fallback: newDeploymentFrontendCapabilityMemoryRepository(), contractVersion: deps.ContractVersion, capabilities: deps.Capabilities, businessBindings: deps.BusinessBindings}
}

// Configured reports whether the optional frontend capability persistence port
// is available. Cross-owner projections use this to omit absent evidence
// sources instead of turning an optional adapter into a runtime failure.
func (s *DeploymentFrontendCapabilityApplicationService) Configured() bool {
	return s != nil && s.repository != nil
}

func (s *DeploymentFrontendCapabilityApplicationService) RegisterManifest(ctx context.Context, manifest deploymentmodel.FrontendCapabilityManifest, principal principalmodel.Principal) (deploymentmodel.FrontendCapabilitySnapshot, error) {
	if err := deploymentAuthorizeCommand(principal); err != nil {
		return deploymentmodel.FrontendCapabilitySnapshot{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return deploymentmodel.FrontendCapabilitySnapshot{}, forbidden("auth.permission_denied")
	}
	if s == nil {
		return deploymentmodel.FrontendCapabilitySnapshot{}, internalError("frontend capability repository is not configured", nil)
	}
	repository := s.repository
	if repository == nil {
		repository = s.fallback
	}
	validation := s.validateFrontendCapabilityManifestUsage(manifest)
	s.validateBusinessBindings(ctx, &validation)
	if err := firstFrontendCapabilityUsageError(validation.Issues); err != nil {
		return deploymentmodel.FrontendCapabilitySnapshot{}, err
	}
	normalized := validation.NormalizedManifest
	workspaceID := principalWorkspaceID(principal)
	payload, _ := json.Marshal(normalized)
	if current, exists, getErr := repository.Get(ctx, workspaceID); getErr != nil {
		return deploymentmodel.FrontendCapabilitySnapshot{}, internalError("load frontend capability manifest before registration", getErr)
	} else if exists && bytes.Equal(current.ManifestJSON, payload) {
		result := s.SnapshotManifest(normalized)
		result.Revision, result.UpdatedAt = current.Revision, current.UpdatedAt
		s.enrichSnapshot(ctx, &result)
		return result, nil
	}
	record, err := repository.Put(ctx, workspaceID, payload)
	if err != nil {
		return deploymentmodel.FrontendCapabilitySnapshot{}, internalError("persist frontend capability manifest", err)
	}
	result := s.SnapshotManifest(normalized)
	result.Revision, result.UpdatedAt = record.Revision, record.UpdatedAt
	s.enrichSnapshot(ctx, &result)
	return result, nil
}

func (s *DeploymentFrontendCapabilityApplicationService) Snapshot(ctx context.Context, principal principalmodel.Principal) (deploymentmodel.FrontendCapabilitySnapshot, error) {
	if err := deploymentAuthorizeQuery(principal); err != nil {
		return deploymentmodel.FrontendCapabilitySnapshot{}, err
	}
	if s == nil {
		return deploymentmodel.FrontendCapabilitySnapshot{}, internalError("frontend capability repository is not configured", nil)
	}
	repository := s.repository
	if repository == nil {
		repository = s.fallback
	}
	workspaceID := principalWorkspaceID(principal)
	record, exists, err := repository.Get(ctx, workspaceID)
	if err != nil {
		return deploymentmodel.FrontendCapabilitySnapshot{}, internalError("load frontend capability manifest", err)
	}
	if !exists {
		result := deploymentmodel.FrontendCapabilitySnapshot{Status: "unknown", MissingFrontendSupport: []deploymentmodel.FrontendCapabilityRequirement{}, StaleFrontendSupport: []deploymentmodel.FrontendCapabilitySupportEntry{}}
		s.enrichSnapshot(ctx, &result)
		return result, nil
	}
	var manifest deploymentmodel.FrontendCapabilityManifest
	if err := json.Unmarshal(record.ManifestJSON, &manifest); err != nil {
		return deploymentmodel.FrontendCapabilitySnapshot{}, internalError("decode frontend capability manifest", err)
	}
	result := s.SnapshotManifest(manifest)
	result.Revision, result.UpdatedAt = record.Revision, record.UpdatedAt
	s.enrichSnapshot(ctx, &result)
	return result, nil
}

func (s *DeploymentFrontendCapabilityApplicationService) ValidateManifestDefinition(manifest deploymentmodel.FrontendCapabilityManifest) (deploymentmodel.FrontendCapabilityManifest, error) {
	validation := s.validateFrontendCapabilityManifestUsage(manifest)
	if err := firstFrontendCapabilityUsageError(validation.Issues); err != nil {
		return manifest, err
	}
	return validation.NormalizedManifest, nil
}

func (s *DeploymentFrontendCapabilityApplicationService) SnapshotManifest(manifest deploymentmodel.FrontendCapabilityManifest) deploymentmodel.FrontendCapabilitySnapshot {
	known, expected := s.runtimeFrontendCapabilityRequirements()
	_ = known
	registered := map[string]bool{}
	for _, entry := range manifest.Entries {
		registered[entry.SupportKey] = true
	}
	result := deploymentmodel.FrontendCapabilitySnapshot{
		Status: "compatible", Manifest: &manifest, RuntimeContractVersion: s.contractVersion,
		RuntimeCapabilityCount: len(s.capabilities()), FrontendSupportCount: len(manifest.Entries),
		MissingFrontendSupport: []deploymentmodel.FrontendCapabilityRequirement{}, StaleFrontendSupport: []deploymentmodel.FrontendCapabilitySupportEntry{},
	}
	contractCompatible := false
	for _, version := range manifest.RuntimeContractVersions {
		if version == s.contractVersion {
			contractCompatible = true
		}
	}
	for capability, supportKey := range expected {
		if !registered[supportKey] {
			result.MissingFrontendSupport = append(result.MissingFrontendSupport, deploymentmodel.FrontendCapabilityRequirement{CapabilityKey: capability, SupportKey: supportKey})
		}
	}
	for _, entry := range manifest.Entries {
		valid := hasFrontendBusinessBindings(entry)
		for _, capability := range entry.CapabilityKeys {
			if expected[capability] == entry.SupportKey {
				valid = true
			}
		}
		if !valid {
			result.StaleFrontendSupport = append(result.StaleFrontendSupport, entry)
		}
	}
	sort.Slice(result.MissingFrontendSupport, func(i, j int) bool {
		return result.MissingFrontendSupport[i].CapabilityKey < result.MissingFrontendSupport[j].CapabilityKey
	})
	if !contractCompatible || len(result.MissingFrontendSupport) > 0 || len(result.StaleFrontendSupport) > 0 {
		result.Status = "gaps"
	}
	result.ContractCompatible = contractCompatible
	payload, _ := json.Marshal(manifest)
	hash := sha256.Sum256(payload)
	result.ManifestHash = hex.EncodeToString(hash[:])
	return result
}

func (s *DeploymentFrontendCapabilityApplicationService) enrichSnapshot(ctx context.Context, result *deploymentmodel.FrontendCapabilitySnapshot) {
	if s == nil || result == nil {
		return
	}
	result.RuntimeContractVersion = s.contractVersion
	result.RuntimeCapabilityCount = len(s.capabilities())
	if result.Manifest != nil {
		result.FrontendSupportCount = len(result.Manifest.Entries)
	}
	if s.businessBindings == nil {
		return
	}
	bindings := s.businessBindings(ctx)
	result.SchemaHash = strings.TrimSpace(bindings.SchemaHash)
	result.SchemaSnapshotVersion = strings.TrimSpace(bindings.SchemaSnapshotVersion)
}

func hasFrontendBusinessBindings(entry deploymentmodel.FrontendCapabilitySupportEntry) bool {
	return len(entry.ActorRoles) > 0 || len(entry.BusinessObjects) > 0 || len(entry.ViewKeys) > 0 ||
		len(entry.ImplementedActions) > 0 || len(entry.ReportKeys) > 0 || len(entry.FieldKeys) > 0 || len(entry.AcceptanceClaims) > 0
}

func (s *DeploymentFrontendCapabilityApplicationService) runtimeFrontendCapabilityRequirements() (map[string]bool, map[string]string) {
	known, expected := map[string]bool{}, map[string]string{}
	for _, capability := range s.capabilities() {
		known[capability.Key] = true
		if capability.FrontendSupportKey != "" {
			expected[capability.Key] = capability.FrontendSupportKey
		}
	}
	return known, expected
}

func stringIndex(index int) string { return strconv.Itoa(index) }

func principalWorkspaceID(principal principalmodel.Principal) string {
	return strings.TrimSpace(principal.WorkspaceID)
}

func valueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

type deploymentFrontendCapabilityMemoryRepository struct {
	mu     sync.RWMutex
	values map[string]deploymentmodel.DeploymentFrontendCapabilityState
}

func newDeploymentFrontendCapabilityMemoryRepository() deploymentrepository.DeploymentFrontendCapabilityRepository {
	return &deploymentFrontendCapabilityMemoryRepository{values: map[string]deploymentmodel.DeploymentFrontendCapabilityState{}}
}

func (r *deploymentFrontendCapabilityMemoryRepository) Get(ctx context.Context, workspaceID string) (deploymentmodel.DeploymentFrontendCapabilityState, bool, error) {
	if err := ctx.Err(); err != nil {
		return deploymentmodel.DeploymentFrontendCapabilityState{}, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.values[workspaceID]
	return value, ok, nil
}

func (r *deploymentFrontendCapabilityMemoryRepository) Put(ctx context.Context, workspaceID string, payload []byte) (deploymentmodel.DeploymentFrontendCapabilityState, error) {
	if err := ctx.Err(); err != nil {
		return deploymentmodel.DeploymentFrontendCapabilityState{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	value := r.values[workspaceID]
	value.WorkspaceID, value.Revision = workspaceID, value.Revision+1
	value.ManifestJSON, value.UpdatedAt = append([]byte(nil), payload...), time.Now().UTC().Format(time.RFC3339Nano)
	r.values[workspaceID] = value
	return value, nil
}
