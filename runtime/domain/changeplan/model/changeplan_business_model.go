package changeplanmodel

import connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

const (
	RuntimeAuthoringCoverageLedgerVersion            = "runtime-authoring-coverage-v1"
	RuntimeAuthoringScenarioEvidenceVersion          = "runtime-authoring-scenario-evidence-v2"
	RuntimeAuthoringDeliveryEvidenceVersion          = "runtime-authoring-delivery-evidence-v2"
	RuntimeAuthoringStepReceiptVersion               = "runtime-authoring-step-receipt-v1"
	RuntimeAuthoringEvidenceCollectionVersion        = "runtime-authoring-evidence-collection-v1"
	RuntimeAuthoringEvidencePlanVersion              = "runtime-authoring-evidence-plan-v1"
	RuntimeAuthoringEvidenceSessionVersion           = "runtime-authoring-evidence-session-v1"
	RuntimeAuthoringEvidenceStepTokenVersion         = "runtime-authoring-evidence-step-v1"
	RuntimeAuthoringBuilderTaskHeader                = "Builder-Task-ID"
	RuntimeAuthoringScenarioIDHeader                 = "Runtime-Authoring-Scenario-ID"
	RuntimeAuthoringScenarioCategoriesHeader         = "Runtime-Authoring-Scenario-Categories"
	RuntimeAuthoringStepLabelHeader                  = "Runtime-Authoring-Step-Label"
	RuntimeAuthoringStepObservationHeader            = "Runtime-Authoring-Step-Observation"
	RuntimeAuthoringExpectedStatusHeader             = "Runtime-Authoring-Expected-Status"
	RuntimeAuthoringSnapshotHashHeader               = "Runtime-Authoring-Snapshot-Hash"
	RuntimeAuthoringCoverageHashHeader               = "Runtime-Authoring-Coverage-Hash"
	RuntimeAuthoringStepReceiptHeader                = "Runtime-Authoring-Step-Receipt"
	RuntimeAuthoringEvidenceErrorHeader              = "Runtime-Authoring-Evidence-Error"
	RuntimeAuthoringEvidenceStepTokenHeader          = "Runtime-Authoring-Evidence-Step-Token"
	RuntimeAuthoringEvidenceTrustPolicy              = "runtime_observed_hmac_receipts_only"
	RuntimeAuthoringEvidenceStreamingPathRestriction = "streaming endpoints do not support scenario evidence collection"
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

type RuntimeAuthoringEvidenceCollectionContract struct {
	Version                  string `json:"version"`
	TrustPolicy              string `json:"trust_policy"`
	EvidencePlanVersion      string `json:"evidence_plan_version"`
	EvidenceSessionVersion   string `json:"evidence_session_version"`
	EvidenceStepTokenVersion string `json:"evidence_step_token_version"`
	StepReceiptVersion       string `json:"step_receipt_version"`
	BuilderTaskHeader        string `json:"builder_task_header"`
	ScenarioIDHeader         string `json:"scenario_id_header"`
	ScenarioCategoriesHeader string `json:"scenario_categories_header"`
	StepLabelHeader          string `json:"step_label_header"`
	StepObservationHeader    string `json:"step_observation_header"`
	ExpectedStatusHeader     string `json:"expected_status_header"`
	SnapshotHashHeader       string `json:"snapshot_hash_header"`
	CoverageHashHeader       string `json:"coverage_hash_header"`
	StepReceiptHeader        string `json:"step_receipt_header"`
	EvidenceErrorHeader      string `json:"evidence_error_header"`
	EvidenceStepTokenHeader  string `json:"evidence_step_token_header"`
	StreamingPathRestriction string `json:"streaming_path_restriction"`
}

// RuntimeAuthoringEvidencePlan is registered once during validation. Builder
// can compile this plan from its requirement graph; individual scenario HTTP
// requests then carry only the signed step token issued by Runtime.
type RuntimeAuthoringEvidencePlan struct {
	Version   string                                 `json:"version"`
	Scenarios []RuntimeAuthoringEvidenceScenarioPlan `json:"scenarios"`
}

type RuntimeAuthoringEvidenceScenarioPlan struct {
	ScenarioID string                             `json:"scenario_id"`
	Categories []string                           `json:"categories"`
	Steps      []RuntimeAuthoringEvidenceStepPlan `json:"steps"`
}

type RuntimeAuthoringEvidenceStepPlan struct {
	StepID         string `json:"step_id"`
	Label          string `json:"label"`
	Observation    string `json:"observation,omitempty"`
	Method         string `json:"method"`
	Path           string `json:"path"`
	ExpectedStatus []int  `json:"expected_status"`
}

type RuntimeAuthoringEvidenceSession struct {
	Version      string                                `json:"version"`
	SessionID    string                                `json:"session_id"`
	SnapshotHash string                                `json:"snapshot_hash"`
	CoverageHash string                                `json:"coverage_hash"`
	Steps        []RuntimeAuthoringEvidenceSessionStep `json:"steps"`
}

type RuntimeAuthoringEvidenceSessionStep struct {
	ScenarioID string `json:"scenario_id"`
	StepID     string `json:"step_id"`
	Token      string `json:"token"`
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
	SessionID           string `json:"session_id,omitempty"`
	StepID              string `json:"step_id,omitempty"`
	Label               string `json:"label"`
	Observation         string `json:"observation,omitempty"`
	Method              string `json:"method"`
	Path                string `json:"path"`
	ExpectedStatus      []int  `json:"expected_status"`
	ActualStatus        int    `json:"actual_status"`
	RequestHash         string `json:"request_hash"`
	ResponseHash        string `json:"response_hash"`
	IdempotencyKey      string `json:"idempotency_key,omitempty"`
	IdempotencyReplayed bool   `json:"idempotency_replayed,omitempty"`
	Passed              bool   `json:"passed"`
	RuntimeReceipt      string `json:"runtime_receipt"`
}

type RuntimeAuthoringDeliveryEvidence struct {
	Version   string                             `json:"version"`
	Binding   RuntimeAuthoringEvidenceBinding    `json:"binding"`
	Coverage  RuntimeAuthoringCoverageLedger     `json:"coverage"`
	Scenarios []RuntimeAuthoringScenarioEvidence `json:"scenarios"`
	Receipts  []string                           `json:"receipts,omitempty"`
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
