package projection

import recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"

import (
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"

	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	"sort"
)

const (
	BusinessSystemSnapshotVersion     = "domain-system-snapshot-v2"
	RuntimeNativeMetadataModelVersion = "runtime-native-metadata-v1"
)

type RuntimeNativeMetadataModel struct {
	ModelVersion             string                        `json:"model_version"`
	Status                   string                        `json:"status"`
	ServiceKind              string                        `json:"service_kind"`
	RuntimeVersion           string                        `json:"runtime_version"`
	TemplateID               string                        `json:"template_id,omitempty"`
	TemplateVersion          string                        `json:"template_version,omitempty"`
	ManifestHash             string                        `json:"manifest_hash,omitempty"`
	SnapshotHash             string                        `json:"snapshot_hash"`
	SourceBlueprintID        string                        `json:"source_blueprint_id,omitempty"`
	APIContractVersion       string                        `json:"api_contract_version"`
	APIContractHash          string                        `json:"api_contract_hash"`
	AuthoringContractVersion string                        `json:"authoring_contract_version"`
	AuthoringContractHash    string                        `json:"authoring_contract_hash"`
	Manifest                 *manifestmodel.ManifestSchema `json:"manifest"`
}

type SystemResourceSource struct {
	ResourceType  string `json:"resource_type"`
	ResourceKey   string `json:"resource_key"`
	ObjectKey     string `json:"object_key,omitempty"`
	Name          string `json:"name,omitempty"`
	SchemaVersion string `json:"schema_version,omitempty"`
	SchemaHash    string `json:"schema_hash,omitempty"`
	SourceKind    string `json:"source_kind,omitempty"`
	SourceID      string `json:"source_id,omitempty"`
	Disabled      bool   `json:"disabled"`
}

type BusinessSystemSnapshot struct {
	SnapshotVersion          string                                         `json:"snapshot_version"`
	SnapshotHash             string                                         `json:"snapshot_hash"`
	RuntimeMetadata          RuntimeNativeMetadataModel                     `json:"runtime_metadata"`
	RuntimeVersion           string                                         `json:"runtime_version"`
	AuthoringContractVersion string                                         `json:"authoring_contract_version"`
	AuthoringContractHash    string                                         `json:"authoring_contract_hash"`
	SchemaHash               string                                         `json:"schema_hash"`
	Schema                   metadatamodel.ApplicationSchemaSnapshot        `json:"schema"`
	EffectivePermissions     recordcontract.RecordFeaturePermissionSnapshot `json:"effective_permissions"`
	RuntimeState             BusinessRuntimeStateSnapshot                   `json:"runtime_state"`
	FrontendCapabilities     changeplanmodel.FrontendCapabilities           `json:"frontend_capabilities"`
	ResourceSources          []SystemResourceSource                         `json:"resource_sources"`
	SeedRecords              []businessseedmodel.BusinessSeedProvenance     `json:"seed_records"`
	ObjectRecordCounts       map[string]int                                 `json:"object_record_counts"`
	HiddenResourceCategories []string                                       `json:"hidden_resource_categories"`
	ResourceVisibility       map[string]string                              `json:"resource_visibility"`
	CapabilityKeys           []string                                       `json:"-"`
}

func (snapshot BusinessSystemSnapshot) ChangePlanSnapshot() changeplanmodel.Snapshot {
	capabilityKeys := append([]string(nil), snapshot.CapabilityKeys...)
	resources := make([]changeplanmodel.ResourceSource, 0, len(snapshot.ResourceSources))
	for _, source := range snapshot.ResourceSources {
		resources = append(resources, changeplanmodel.ResourceSource{ResourceType: source.ResourceType, ResourceKey: source.ResourceKey, SchemaHash: source.SchemaHash, SourceKind: source.SourceKind, Disabled: source.Disabled})
	}
	processes := make([]changeplanmodel.WorkflowProcess, 0, len(snapshot.RuntimeState.RunningWorkflowProcesses))
	for _, process := range snapshot.RuntimeState.RunningWorkflowProcesses {
		processes = append(processes, changeplanmodel.WorkflowProcess{WorkflowKey: process.WorkflowKey, Status: process.Status})
	}
	connections := make([]changeplanmodel.IntegrationConnection, 0, len(snapshot.RuntimeState.Connections))
	for _, connection := range snapshot.RuntimeState.Connections {
		connections = append(connections, changeplanmodel.IntegrationConnection{Key: connection.Key, Ready: connection.Ready})
	}
	return changeplanmodel.Snapshot{
		SnapshotHash: snapshot.SnapshotHash, RuntimeVersion: snapshot.RuntimeVersion,
		AuthoringContractVersion: snapshot.AuthoringContractVersion, AuthoringContractHash: snapshot.AuthoringContractHash,
		FrontendCapabilities: snapshot.FrontendCapabilities, HiddenResourceCategories: append([]string(nil), snapshot.HiddenResourceCategories...),
		ResourceSources: resources, ObjectRecordCounts: snapshot.ObjectRecordCounts, CapabilityKeys: capabilityKeys,
		RuntimeState: changeplanmodel.RuntimeState{RunningWorkflowProcesses: processes, Connectors: snapshot.RuntimeState.Connectors, Connections: connections},
	}
}

func (snapshot BusinessSystemSnapshot) WithRuntimeMetadata(metadata RuntimeNativeMetadataModel) BusinessSystemSnapshot {
	snapshot.RuntimeMetadata = metadata
	snapshot.Finalize()
	return snapshot
}

func (snapshot *BusinessSystemSnapshot) addResourceSource(resourceType, resourceKey, name, sourceKind string) {
	snapshot.ResourceSources = AddSystemResourceSource(snapshot.ResourceSources, resourceType, resourceKey, name, sourceKind)
}

func (snapshot *BusinessSystemSnapshot) Finalize() {
	if snapshot.ResourceSources == nil {
		snapshot.ResourceSources = []SystemResourceSource{}
	}
	if snapshot.SeedRecords == nil {
		snapshot.SeedRecords = []businessseedmodel.BusinessSeedProvenance{}
	}
	if snapshot.HiddenResourceCategories == nil {
		snapshot.HiddenResourceCategories = []string{}
	}
	if snapshot.ObjectRecordCounts == nil {
		snapshot.ObjectRecordCounts = map[string]int{}
	}
	snapshot.RuntimeState.Normalize()
	SortSystemSnapshotCollections(snapshot.ResourceSources, snapshot.SeedRecords, snapshot.HiddenResourceCategories)
	snapshot.SnapshotHash = ""
	snapshot.SnapshotHash = SystemSnapshotHash(snapshot)
}

func AddSystemResourceSource(sources []SystemResourceSource, resourceType, resourceKey, name, sourceKind string) []SystemResourceSource {
	for _, source := range sources {
		if source.ResourceType == resourceType && source.ResourceKey == resourceKey {
			return sources
		}
	}
	return append(sources, SystemResourceSource{ResourceType: resourceType, ResourceKey: resourceKey, Name: name, SourceKind: sourceKind, SourceID: sourceKind})
}

func SortSystemSnapshotCollections(sources []SystemResourceSource, seeds []businessseedmodel.BusinessSeedProvenance, hidden []string) {
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].ResourceType == sources[j].ResourceType {
			return sources[i].ResourceKey < sources[j].ResourceKey
		}
		return sources[i].ResourceType < sources[j].ResourceType
	})
	sort.Slice(seeds, func(i, j int) bool {
		return seeds[i].ObjectKey+"\x00"+seeds[i].SeedKey < seeds[j].ObjectKey+"\x00"+seeds[j].SeedKey
	})
	sort.Strings(hidden)
}

func SystemSnapshotHash(snapshot any) string {
	switch typed := snapshot.(type) {
	case BusinessSystemSnapshot:
		typed.SnapshotHash = ""
		// EffectivePermissions is a projection for the requesting principal.
		// Maker-checker review compares actor-independent Runtime state.
		typed.EffectivePermissions = recordcontract.RecordFeaturePermissionSnapshot{}
		// Idempotency backlog and cleanup counters are operational telemetry.
		// Saving the change-plan itself creates idempotency activity; hashing
		// that telemetry would make every freshly saved plan immediately stale.
		typed.RuntimeState.Idempotency = deploymentmodel.IdempotencyOperationalStatus{}
		snapshot = typed
	case *BusinessSystemSnapshot:
		copy := *typed
		copy.SnapshotHash = ""
		copy.EffectivePermissions = recordcontract.RecordFeaturePermissionSnapshot{}
		copy.RuntimeState.Idempotency = deploymentmodel.IdempotencyOperationalStatus{}
		snapshot = copy
	}
	payload, _ := json.Marshal(snapshot)
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}
