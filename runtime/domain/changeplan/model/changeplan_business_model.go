package changeplanmodel

import integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

const (
	RuntimeAuthoringCoverageLedgerVersion   = "runtime-authoring-coverage-v1"
	RuntimeAuthoringScenarioEvidenceVersion = "runtime-authoring-scenario-evidence-v1"
	RuntimeAuthoringDeliveryEvidenceVersion = "runtime-authoring-delivery-evidence-v1"
)

var RuntimeAuthoringRequiredScenarioCategories = []string{
	"success", "permission_denied", "precondition_rejected", "atomic_rollback",
	"idempotent_replay", "audit", "event", "outbox",
}

type RuntimeAuthoringCoverageLedger struct {
	Version      string                                `json:"version"`
	Requirements []RuntimeAuthoringCoverageRequirement `json:"requirements"`
}

type RuntimeAuthoringCoverageRequirement struct {
	RequirementID  string                             `json:"requirement_id"`
	CapabilityKeys []string                           `json:"capability_keys"`
	Resources      []RuntimeAuthoringCoverageResource `json:"resources"`
	ScenarioIDs    []string                           `json:"scenario_ids"`
}

type RuntimeAuthoringCoverageResource struct {
	ResourceType string `json:"resource_type"`
	ResourceKey  string `json:"resource_key"`
}

type RuntimeAuthoringCoverageReport struct {
	Version              string                                      `json:"version"`
	Status               string                                      `json:"status"`
	RequirementCount     int                                         `json:"requirement_count"`
	CoveredCount         int                                         `json:"covered_count"`
	Issues               []string                                    `json:"issues"`
	Entries              []RuntimeAuthoringCoverageRequirementReport `json:"entries"`
	SourcelessResources  []RuntimeAuthoringCoverageResource          `json:"sourceless_resources"`
	UnreachableResources []RuntimeAuthoringCoverageResource          `json:"unreachable_resources"`
}

type RuntimeAuthoringCoverageRequirementReport struct {
	RequirementID string   `json:"requirement_id"`
	Status        string   `json:"status"`
	Issues        []string `json:"issues"`
}

type RuntimeAuthoringEvidenceBinding struct {
	RuntimeVersion string            `json:"runtime_version"`
	ContractHash   string            `json:"contract_hash"`
	InstanceHash   string            `json:"instance_hash"`
	SnapshotHash   string            `json:"snapshot_hash"`
	CoverageHash   string            `json:"coverage_hash"`
	ResourceHashes map[string]string `json:"resource_hashes"`
}

type RuntimeAuthoringScenarioEvidence struct {
	Version         string                                 `json:"version"`
	ScenarioID      string                                 `json:"scenario_id"`
	Categories      []string                               `json:"categories"`
	Passed          bool                                   `json:"passed"`
	BeforeStateHash string                                 `json:"before_state_hash,omitempty"`
	AfterStateHash  string                                 `json:"after_state_hash,omitempty"`
	Steps           []RuntimeAuthoringScenarioStepEvidence `json:"steps"`
}

type RuntimeAuthoringScenarioStepEvidence struct {
	Label               string `json:"label"`
	Method              string `json:"method"`
	Path                string `json:"path"`
	ExpectedStatus      []int  `json:"expected_status"`
	ActualStatus        int    `json:"actual_status"`
	RequestHash         string `json:"request_hash"`
	ResponseHash        string `json:"response_hash"`
	IdempotencyKey      string `json:"idempotency_key,omitempty"`
	IdempotencyReplayed bool   `json:"idempotency_replayed,omitempty"`
	Passed              bool   `json:"passed"`
}

type RuntimeAuthoringDeliveryEvidence struct {
	Version   string                             `json:"version"`
	Binding   RuntimeAuthoringEvidenceBinding    `json:"binding"`
	Coverage  RuntimeAuthoringCoverageLedger     `json:"coverage"`
	Scenarios []RuntimeAuthoringScenarioEvidence `json:"scenarios"`
}

type RuntimeAuthoringDeliveryReport struct {
	Version      string                          `json:"version"`
	Valid        bool                            `json:"valid"`
	Status       string                          `json:"status"`
	EvidenceHash string                          `json:"evidence_hash"`
	Binding      RuntimeAuthoringEvidenceBinding `json:"binding"`
	Checks       map[string]string               `json:"checks"`
	Issues       []string                        `json:"issues"`
}

type Snapshot struct {
	SnapshotHash             string
	RuntimeVersion           string
	AuthoringContractVersion string
	AuthoringContractHash    string
	FrontendCapabilities     FrontendCapabilities
	HiddenResourceCategories []string
	ResourceSources          []ResourceSource
	ObjectRecordCounts       map[string]int
	RuntimeState             RuntimeState
	CapabilityKeys           []string
}

func (snapshot Snapshot) ChangePlanSnapshot() Snapshot { return snapshot }

type FrontendCapabilities struct {
	Revision               int64                  `json:"revision"`
	UpdatedAt              string                 `json:"updated_at,omitempty"`
	Status                 string                 `json:"status"`
	ManifestHash           string                 `json:"manifest_hash,omitempty"`
	Manifest               *FrontendManifest      `json:"manifest,omitempty"`
	MissingFrontendSupport []FrontendRequirement  `json:"missing_frontend_support"`
	StaleFrontendSupport   []FrontendSupportEntry `json:"stale_frontend_support"`
}

type FrontendManifest struct {
	ManifestVersion         string                      `json:"manifest_version"`
	FrontendVersion         string                      `json:"frontend_version"`
	RuntimeContractVersions []string                    `json:"runtime_contract_versions"`
	DeploymentEvidence      *FrontendDeploymentEvidence `json:"deployment_evidence,omitempty"`
	Entries                 []FrontendSupportEntry      `json:"entries"`
}

type FrontendDeploymentEvidence struct {
	AuditContractVersion string `json:"audit_contract_version"`
	DesignContractHash   string `json:"design_contract_hash"`
	RouteRegistryHash    string `json:"route_registry_hash"`
	FrontendSourceHash   string `json:"frontend_source_hash"`
	AuditArtifactHash    string `json:"audit_artifact_hash"`
}

type FrontendRequirement struct {
	CapabilityKey string `json:"capability_key"`
	SupportKey    string `json:"support_key"`
}

type FrontendSupportEntry struct {
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

type ResourceSource struct {
	ResourceType string
	ResourceKey  string
	SchemaHash   string
	SourceKind   string
	Disabled     bool
}

type RuntimeState struct {
	RunningWorkflowProcesses []WorkflowProcess
	Connectors               []integrationmodel.ConnectorSchema
	Connections              []IntegrationConnection
}

type WorkflowProcess struct {
	WorkflowKey string
	Status      string
}

type IntegrationConnection struct {
	Key   string
	Ready bool
}
