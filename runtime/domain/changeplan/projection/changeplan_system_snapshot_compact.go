package projection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

const (
	BusinessSystemSnapshotIndexVersion         = "domain-system-snapshot-index-v2"
	BusinessSystemResourcePageVersion          = "domain-system-resource-page-v1"
	BusinessSystemRuntimeStateIndexVersion     = "domain-system-runtime-state-index-v1"
	BusinessSystemDefaultResourcePageSize      = 25
	BusinessSystemMaximumResourcePageSize      = 100
	businessSystemSnapshotEndpoint             = "/authoring/snapshot"
	businessSystemResourcePageEndpointTemplate = "/authoring/snapshot?projection=resources&resource_type={resource_type}&cursor={cursor}&limit={limit}"
	businessSystemResourceEndpointTemplate     = "/authoring/snapshot?projection=resource&resource_type={resource_type}&resource_key={resource_key}"
)

// RuntimeNativeMetadataIndex is the scalar Runtime identity needed to detect
// drift. The full manifest is intentionally excluded from model-facing index
// responses and remains available through the explicit full projection.
type RuntimeNativeMetadataIndex struct {
	ModelVersion             string `json:"model_version"`
	Status                   string `json:"status"`
	ServiceKind              string `json:"service_kind"`
	RuntimeVersion           string `json:"runtime_version"`
	TemplateID               string `json:"template_id,omitempty"`
	TemplateVersion          string `json:"template_version,omitempty"`
	ManifestHash             string `json:"manifest_hash,omitempty"`
	SourceBlueprintID        string `json:"source_blueprint_id,omitempty"`
	APIContractVersion       string `json:"api_contract_version"`
	APIContractHash          string `json:"api_contract_hash"`
	AuthoringContractVersion string `json:"authoring_contract_version"`
	AuthoringContractHash    string `json:"authoring_contract_hash"`
}

type BusinessSystemRuntimeStateCounts struct {
	RunningWorkflowProcesses  int `json:"running_workflow_processes"`
	AutomationRules           int `json:"automation_rules"`
	RecentAutomationRuns      int `json:"recent_automation_runs"`
	SchedulerDefinitions      int `json:"scheduler_definitions"`
	Reports                   int `json:"reports"`
	Connectors                int `json:"connectors"`
	Connections               int `json:"connections"`
	RecentPublicationHandoffs int `json:"recent_publication_handoffs"`
}

// BusinessSystemSnapshotLinks makes progressive discovery explicit. Keeping
// the traversal contract in the index lets a client discard the workspace-wide
// resource list without having to infer query parameter names or fall back to
// the legacy full projection.
type BusinessSystemSnapshotLinks struct {
	ResourcePages     string `json:"resource_pages"`
	ResourceDetail    string `json:"resource_detail"`
	RuntimeStateIndex string `json:"runtime_state_index"`
	FullSnapshot      string `json:"full_snapshot"`
}

type BusinessSystemResourceCollection struct {
	ResourceType   string `json:"resource_type"`
	ResourceCount  int    `json:"resource_count"`
	CollectionHash string `json:"collection_hash"`
}

// BusinessSystemSnapshotIndex is the default HTTP projection. It carries
// resource-category counts and hashes, never workspace-wide resource identities,
// complete definitions, or operational records. Links describe the bounded
// follow-up calls so the smaller projection remains losslessly discoverable.
type BusinessSystemSnapshotIndex struct {
	Version                  string                             `json:"version"`
	SnapshotHash             string                             `json:"snapshot_hash"`
	RuntimeVersion           string                             `json:"runtime_version"`
	AuthoringContractVersion string                             `json:"authoring_contract_version"`
	AuthoringContractHash    string                             `json:"authoring_contract_hash"`
	SchemaHash               string                             `json:"schema_hash"`
	RuntimeMetadata          RuntimeNativeMetadataIndex         `json:"runtime_metadata"`
	ResourceCount            int                                `json:"resource_count"`
	ResourceCounts           map[string]int                     `json:"resource_counts"`
	ResourceCollections      []BusinessSystemResourceCollection `json:"resource_collections"`
	CapabilityCount          int                                `json:"capability_count"`
	SeedRecordCount          int                                `json:"seed_record_count"`
	HiddenResourceCategories []string                           `json:"hidden_resource_categories"`
	ResourceVisibility       map[string]string                  `json:"resource_visibility"`
	Links                    BusinessSystemSnapshotLinks        `json:"links"`
}

type BusinessSystemResourcePage struct {
	Version        string                      `json:"version"`
	ResourceType   string                      `json:"resource_type"`
	CollectionHash string                      `json:"collection_hash"`
	Total          int                         `json:"total"`
	Limit          int                         `json:"limit"`
	Items          []SystemResourceSource      `json:"items"`
	NextCursor     string                      `json:"next_cursor,omitempty"`
	Links          BusinessSystemSnapshotLinks `json:"links"`
}

type BusinessSystemRuntimeStateIndex struct {
	Version   string                           `json:"version"`
	StateHash string                           `json:"state_hash"`
	Counts    BusinessSystemRuntimeStateCounts `json:"counts"`
	Links     BusinessSystemSnapshotLinks      `json:"links"`
}

type BusinessSystemResourceDetail struct {
	Version      string                `json:"version"`
	SnapshotHash string                `json:"snapshot_hash"`
	ResourceType string                `json:"resource_type"`
	ResourceKey  string                `json:"resource_key"`
	ResourceHash string                `json:"resource_hash"`
	Source       *SystemResourceSource `json:"source,omitempty"`
	Definition   json.RawMessage       `json:"definition,omitempty"`
}

func ProjectBusinessSystemResourceDetail(snapshotHash string, source SystemResourceSource, definition json.RawMessage) BusinessSystemResourceDetail {
	definition = append(json.RawMessage(nil), definition...)
	resourceHash := strings.TrimSpace(source.SchemaHash)
	if resourceHash == "" && len(definition) > 0 {
		sum := sha256.Sum256(definition)
		resourceHash = hex.EncodeToString(sum[:])
	}
	return BusinessSystemResourceDetail{
		Version: BusinessSystemSnapshotIndexVersion, SnapshotHash: strings.TrimSpace(snapshotHash),
		ResourceType: strings.TrimSpace(source.ResourceType), ResourceKey: strings.TrimSpace(source.ResourceKey),
		ResourceHash: resourceHash, Source: &source, Definition: definition,
	}
}

func (snapshot BusinessSystemSnapshot) CompactIndex() BusinessSystemSnapshotIndex {
	resources := sortedSystemResourceSources(snapshot.ResourceSources)
	resourceCounts := map[string]int{}
	for _, resource := range resources {
		resourceCounts[resource.ResourceType]++
	}
	resourceCollections := make([]BusinessSystemResourceCollection, 0, len(resourceCounts))
	for resourceType, count := range resourceCounts {
		resourceCollections = append(resourceCollections, BusinessSystemResourceCollection{
			ResourceType: resourceType, ResourceCount: count,
			CollectionHash: businessSystemResourceCollectionHash(resources, resourceType),
		})
	}
	sort.Slice(resourceCollections, func(i, j int) bool { return resourceCollections[i].ResourceType < resourceCollections[j].ResourceType })
	metadata := snapshot.RuntimeMetadata
	result := BusinessSystemSnapshotIndex{
		Version:        BusinessSystemSnapshotIndexVersion,
		RuntimeVersion: snapshot.RuntimeVersion, AuthoringContractVersion: snapshot.AuthoringContractVersion,
		AuthoringContractHash: snapshot.AuthoringContractHash, SchemaHash: snapshot.SchemaHash,
		RuntimeMetadata: RuntimeNativeMetadataIndex{
			ModelVersion: metadata.ModelVersion, Status: metadata.Status, ServiceKind: metadata.ServiceKind,
			RuntimeVersion: metadata.RuntimeVersion, TemplateID: metadata.TemplateID, TemplateVersion: metadata.TemplateVersion,
			ManifestHash: metadata.ManifestHash, SourceBlueprintID: metadata.SourceBlueprintID,
			APIContractVersion: metadata.APIContractVersion, APIContractHash: metadata.APIContractHash,
			AuthoringContractVersion: metadata.AuthoringContractVersion, AuthoringContractHash: metadata.AuthoringContractHash,
		},
		ResourceCount: len(resources), ResourceCounts: resourceCounts, ResourceCollections: resourceCollections,
		CapabilityCount:          len(snapshot.CapabilityKeys),
		SeedRecordCount:          len(snapshot.SeedRecords),
		HiddenResourceCategories: append([]string(nil), snapshot.HiddenResourceCategories...),
		ResourceVisibility:       cloneStringMap(snapshot.ResourceVisibility),
		Links:                    businessSystemSnapshotLinks(),
	}
	result.SnapshotHash = businessSystemSnapshotIndexHash(result)
	return result
}

func (index BusinessSystemSnapshotIndex) WithRuntimeMetadata(metadata RuntimeNativeMetadataModel) BusinessSystemSnapshotIndex {
	index.RuntimeMetadata = runtimeNativeMetadataIndex(metadata)
	index.SnapshotHash = businessSystemSnapshotIndexHash(index)
	return index
}

func runtimeNativeMetadataIndex(metadata RuntimeNativeMetadataModel) RuntimeNativeMetadataIndex {
	return RuntimeNativeMetadataIndex{
		ModelVersion: metadata.ModelVersion, Status: metadata.Status, ServiceKind: metadata.ServiceKind,
		RuntimeVersion: metadata.RuntimeVersion, TemplateID: metadata.TemplateID, TemplateVersion: metadata.TemplateVersion,
		ManifestHash: metadata.ManifestHash, SourceBlueprintID: metadata.SourceBlueprintID,
		APIContractVersion: metadata.APIContractVersion, APIContractHash: metadata.APIContractHash,
		AuthoringContractVersion: metadata.AuthoringContractVersion, AuthoringContractHash: metadata.AuthoringContractHash,
	}
}

func businessSystemSnapshotIndexHash(index BusinessSystemSnapshotIndex) string {
	index.SnapshotHash = ""
	payload, _ := json.Marshal(index)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func businessSystemSnapshotLinks() BusinessSystemSnapshotLinks {
	return BusinessSystemSnapshotLinks{
		ResourcePages:     businessSystemResourcePageEndpointTemplate,
		ResourceDetail:    businessSystemResourceEndpointTemplate,
		RuntimeStateIndex: businessSystemSnapshotEndpoint + "?projection=runtime-index",
		FullSnapshot:      businessSystemSnapshotEndpoint + "?projection=full",
	}
}

func sortedSystemResourceSources(source []SystemResourceSource) []SystemResourceSource {
	resources := append([]SystemResourceSource(nil), source...)
	sort.Slice(resources, func(i, j int) bool {
		left, right := resources[i], resources[j]
		if left.ResourceType != right.ResourceType {
			return left.ResourceType < right.ResourceType
		}
		if left.ResourceKey != right.ResourceKey {
			return left.ResourceKey < right.ResourceKey
		}
		if left.ObjectKey != right.ObjectKey {
			return left.ObjectKey < right.ObjectKey
		}
		return left.SchemaHash < right.SchemaHash
	})
	return resources
}

func businessSystemResourceCollectionHash(resources []SystemResourceSource, resourceType string) string {
	selected := make([]SystemResourceSource, 0)
	for _, resource := range resources {
		if resource.ResourceType == resourceType {
			selected = append(selected, resource)
		}
	}
	payload, _ := json.Marshal(selected)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// ProjectBusinessSystemResourcePage provides a bounded, stable traversal of
// one resource category. The cursor is an opaque offset for this version; the
// collection hash lets clients reject pages assembled across a source change.
func ProjectBusinessSystemResourcePage(source []SystemResourceSource, resourceType, cursor string, limit int) (BusinessSystemResourcePage, bool) {
	resourceType, cursor = strings.TrimSpace(resourceType), strings.TrimSpace(cursor)
	if resourceType == "" {
		return BusinessSystemResourcePage{}, false
	}
	if limit == 0 {
		limit = BusinessSystemDefaultResourcePageSize
	}
	if limit < 1 || limit > BusinessSystemMaximumResourcePageSize {
		return BusinessSystemResourcePage{}, false
	}
	offset := 0
	if cursor != "" {
		parsed, err := strconv.Atoi(cursor)
		if err != nil || parsed < 0 {
			return BusinessSystemResourcePage{}, false
		}
		offset = parsed
	}
	resources := sortedSystemResourceSources(source)
	selected := make([]SystemResourceSource, 0)
	for _, resource := range resources {
		if resource.ResourceType == resourceType {
			selected = append(selected, resource)
		}
	}
	if offset > len(selected) {
		return BusinessSystemResourcePage{}, false
	}
	end := offset + limit
	if end > len(selected) {
		end = len(selected)
	}
	page := BusinessSystemResourcePage{
		Version: BusinessSystemResourcePageVersion, ResourceType: resourceType,
		CollectionHash: businessSystemResourceCollectionHash(resources, resourceType), Total: len(selected), Limit: limit,
		Items: append([]SystemResourceSource(nil), selected[offset:end]...), Links: businessSystemSnapshotLinks(),
	}
	if end < len(selected) {
		page.NextCursor = strconv.Itoa(end)
	}
	return page, true
}

func ProjectBusinessSystemRuntimeStateIndex(state BusinessRuntimeStateSnapshot) BusinessSystemRuntimeStateIndex {
	payload, _ := json.Marshal(state)
	sum := sha256.Sum256(payload)
	return BusinessSystemRuntimeStateIndex{
		Version: BusinessSystemRuntimeStateIndexVersion, StateHash: hex.EncodeToString(sum[:]),
		Counts: BusinessSystemRuntimeStateCounts{
			RunningWorkflowProcesses: len(state.RunningWorkflowProcesses), AutomationRules: len(state.AutomationRules),
			RecentAutomationRuns: len(state.RecentAutomationRuns), SchedulerDefinitions: len(state.Scheduler.Definitions),
			Reports: len(state.Reports), Connectors: len(state.Connectors), Connections: len(state.Connections),
			RecentPublicationHandoffs: len(state.RecentPublicationHandoffs),
		},
		Links: businessSystemSnapshotLinks(),
	}
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (snapshot BusinessSystemSnapshot) ResourceDetail(resourceType, resourceKey string) (BusinessSystemResourceDetail, bool) {
	resourceType, resourceKey = strings.TrimSpace(resourceType), strings.TrimSpace(resourceKey)
	if resourceType == "" || resourceKey == "" {
		return BusinessSystemResourceDetail{}, false
	}
	var source *SystemResourceSource
	for index := range snapshot.ResourceSources {
		candidate := snapshot.ResourceSources[index]
		if strings.TrimSpace(candidate.ResourceType) == resourceType && strings.TrimSpace(candidate.ResourceKey) == resourceKey {
			source = &candidate
			break
		}
	}
	definition, definitionFound := snapshot.resourceDefinition(resourceType, resourceKey, source)
	if source == nil && !definitionFound {
		return BusinessSystemResourceDetail{}, false
	}
	raw := json.RawMessage(nil)
	if definitionFound {
		raw, _ = json.Marshal(definition)
	}
	projectedSource := SystemResourceSource{ResourceType: resourceType, ResourceKey: resourceKey}
	if source != nil {
		projectedSource = *source
	}
	return ProjectBusinessSystemResourceDetail(snapshot.SnapshotHash, projectedSource, raw), true
}

func (snapshot BusinessSystemSnapshot) resourceDefinition(resourceType, resourceKey string, source *SystemResourceSource) (any, bool) {
	switch resourceType {
	case "object":
		for _, value := range snapshot.Schema.Objects {
			if strings.TrimSpace(value.Key) == resourceKey {
				return value, true
			}
		}
	case "field", "validation":
		objectKey := ""
		if source != nil {
			objectKey = strings.TrimSpace(source.ObjectKey)
		}
		memberKey := resourceKey
		if separator := strings.LastIndex(memberKey, "."); separator >= 0 {
			if objectKey == "" {
				objectKey = strings.TrimSpace(memberKey[:separator])
			}
			memberKey = strings.TrimSpace(memberKey[separator+1:])
		}
		for _, object := range snapshot.Schema.Objects {
			if objectKey != "" && strings.TrimSpace(object.Key) != objectKey {
				continue
			}
			if resourceType == "field" {
				for _, value := range object.Fields {
					if strings.TrimSpace(value.Key) == memberKey {
						return value, true
					}
				}
			} else {
				for _, value := range object.Validations {
					if strings.TrimSpace(value.Key) == memberKey {
						return value, true
					}
				}
			}
		}
	case "action":
		for _, value := range snapshot.Schema.Actions {
			if strings.TrimSpace(value.Key) == resourceKey {
				return value, true
			}
		}
	case "workflow":
		for _, value := range snapshot.Schema.Workflows {
			if strings.TrimSpace(value.Key) == resourceKey {
				return value, true
			}
		}
	case "automation_rule":
		for _, value := range snapshot.Schema.AutomationRules {
			if strings.TrimSpace(value.Key) == resourceKey {
				return value, true
			}
		}
	case "dictionary":
		for _, value := range snapshot.Schema.Dictionaries {
			if strings.TrimSpace(value.Key) == resourceKey {
				return value, true
			}
		}
	case "integration_event_mapping":
		for _, value := range snapshot.Schema.Integrations.EventMappings {
			if strings.TrimSpace(value.Key) == resourceKey {
				return value, true
			}
		}
	case "connector":
		for _, value := range snapshot.Schema.Integrations.Connectors {
			if strings.TrimSpace(value.Key) == resourceKey {
				return value, true
			}
		}
	case "report":
		for _, value := range snapshot.Schema.Reports {
			if strings.TrimSpace(value.Key) == resourceKey {
				return value, true
			}
		}
	case "identity_profile_binding":
		for _, value := range snapshot.Schema.IdentityProfileExtensions {
			if strings.TrimSpace(value.ObjectKey) == resourceKey {
				return value, true
			}
		}
	}
	return nil, false
}
