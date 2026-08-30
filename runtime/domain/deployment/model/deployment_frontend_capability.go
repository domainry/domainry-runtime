package deploymentmodel

const FrontendCapabilityManifestVersion = "frontend-capability-support-v1"

type FrontendCapabilityManifest struct {
	ManifestVersion         string                           `json:"manifest_version"`
	FrontendVersion         string                           `json:"frontend_version"`
	RuntimeContractVersions []string                         `json:"runtime_contract_versions"`
	DeploymentEvidence      *FrontendDeploymentEvidence      `json:"deployment_evidence,omitempty"`
	Entries                 []FrontendCapabilitySupportEntry `json:"entries"`
}

type FrontendDeploymentEvidence struct {
	AuditContractVersion string `json:"audit_contract_version"`
	DesignContractHash   string `json:"design_contract_hash"`
	RouteRegistryHash    string `json:"route_registry_hash"`
	FrontendSourceHash   string `json:"frontend_source_hash"`
	AuditArtifactHash    string `json:"audit_artifact_hash"`
}

type FrontendCapabilitySupportEntry struct {
	SupportKey          string   `json:"support_key"`
	CapabilityKeys      []string `json:"capability_keys"`
	Route               string   `json:"route"`
	RequiredPermissions []string `json:"required_permissions"`
	FeatureModule       string   `json:"feature_module"`
	AcceptanceTests     []string `json:"acceptance_tests"`
	ActorRoles          []string `json:"actor_roles,omitempty"`
	BusinessObjects     []string `json:"business_objects,omitempty"`
	ImplementedActions  []string `json:"implemented_actions,omitempty"`
	ReportKeys          []string `json:"report_keys,omitempty"`
	FieldKeys           []string `json:"field_keys,omitempty"`
	AcceptanceClaims    []string `json:"acceptance_claims,omitempty"`
}

type FrontendCapabilitySnapshot struct {
	Revision               int64                            `json:"revision"`
	UpdatedAt              string                           `json:"updated_at,omitempty"`
	Status                 string                           `json:"status"`
	RuntimeContractVersion string                           `json:"runtime_contract_version"`
	ContractCompatible     bool                             `json:"contract_compatible"`
	SchemaHash             string                           `json:"schema_hash,omitempty"`
	SchemaSnapshotVersion  string                           `json:"schema_snapshot_version,omitempty"`
	RuntimeCapabilityCount int                              `json:"runtime_capability_count"`
	FrontendSupportCount   int                              `json:"frontend_support_count"`
	ManifestHash           string                           `json:"manifest_hash,omitempty"`
	Manifest               *FrontendCapabilityManifest      `json:"manifest,omitempty"`
	MissingFrontendSupport []FrontendCapabilityRequirement  `json:"missing_frontend_support"`
	StaleFrontendSupport   []FrontendCapabilitySupportEntry `json:"stale_frontend_support"`
}

type FrontendCapabilityRequirement struct {
	CapabilityKey string `json:"capability_key"`
	SupportKey    string `json:"support_key"`
}

type FrontendCapabilityDefinition struct {
	Key                string
	FrontendSupportKey string
	Permissions        []string
}

type FrontendCapabilityManifestValidationResult struct {
	Valid              bool                                     `json:"valid"`
	NormalizedManifest FrontendCapabilityManifest               `json:"normalized_manifest"`
	Issues             []FrontendCapabilityUsageValidationIssue `json:"issues"`
	ContractVersion    string                                   `json:"contract_version"`
}

type FrontendCapabilityUsageValidationIssue struct {
	Severity        string            `json:"severity"`
	EntrySupportKey string            `json:"entry_support_key,omitempty"`
	FieldPath       string            `json:"field_path"`
	ErrorCode       string            `json:"error_code"`
	MessageKey      string            `json:"message_key"`
	CapabilityKey   string            `json:"capability_key,omitempty"`
	ContractVersion string            `json:"contract_version"`
	Params          map[string]string `json:"params,omitempty"`
}
