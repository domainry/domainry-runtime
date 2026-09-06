package actionmodel

import principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

type ActionSource string

const (
	ActionSourceHTTP        ActionSource = "http"
	ActionSourceWorkflow    ActionSource = "workflow"
	ActionSourceAutomation  ActionSource = "automation"
	ActionSourceRecordTimer ActionSource = "record_timer"
	ActionSourceIntegration ActionSource = "integration"
	ActionSourceAgent       ActionSource = "agent"
	ActionSourceNested      ActionSource = "nested_action"
	ActionSourceBulk        ActionSource = "bulk"
)

type ActionInvocation struct {
	ActionKey            string
	ObjectKey            string
	RecordID             string
	Input                map[string]any
	Principal            principalmodel.Principal
	Actor                principalmodel.Principal
	RunAs                principalmodel.Principal
	Source               ActionSource
	ProcessID            string
	NodeID               string
	RequestID            string
	IdempotencyKey       string
	TargetOrganizationID string
	AssuranceToken       string
	AssuranceEvidence    map[string]string
	AssuranceValidated   bool
}

type ActionInvocationResult struct {
	InvocationID   string              `json:"invocation_id"`
	Status         string              `json:"status"`
	Source         ActionSource        `json:"source"`
	Output         map[string]any      `json:"output,omitempty"`
	ErrorCode      string              `json:"error_code,omitempty"`
	Retryable      bool                `json:"retryable,omitempty"`
	AuditEvent     string              `json:"audit_event,omitempty"`
	OutboxIDs      []string            `json:"outbox_ids,omitempty"`
	RecordVersions map[string]string   `json:"record_versions,omitempty"`
	AuditEvidence  map[string]string   `json:"audit_evidence,omitempty"`
	Record         *ActionResult       `json:"record,omitempty"`
	Object         *ActionObjectResult `json:"object,omitempty"`
	NoStore        bool                `json:"-"`
}
