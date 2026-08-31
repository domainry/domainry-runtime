package changeplanmodel

import connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

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
	HiddenResourceCategories []string
	ResourceSources          []ResourceSource
	ObjectRecordCounts       map[string]int
	RuntimeState             RuntimeState
	CapabilityKeys           []string
}

func (snapshot Snapshot) ChangePlanSnapshot() Snapshot { return snapshot }

type ResourceSource struct {
	ResourceType string
	ResourceKey  string
	SchemaHash   string
	SourceKind   string
	Disabled     bool
}

type RuntimeState struct {
	RunningWorkflowProcesses []WorkflowProcess
	Connectors               []connectormodel.ConnectorSchema
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
