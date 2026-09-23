package operationsmodel

type OperationsExecutionScope string

const (
	OperationsExecutionWorkspace OperationsExecutionScope = "workspace"
	OperationsExecutionSystem    OperationsExecutionScope = "system"
)

type OperationsRetentionClass string

const (
	OperationsRetentionTechnical  OperationsRetentionClass = "technical_ttl"
	OperationsRetentionLegalAudit OperationsRetentionClass = "legal_audit_retention"
)

// OperationsRetentionPolicy is part of the closed operation-kind
// registration. Seconds are used on the wire so callers do not have to infer
// the unit used by time.Duration's JSON representation.
type OperationsRetentionPolicy struct {
	PolicyKey                 string                   `json:"policy_key"`
	Class                     OperationsRetentionClass `json:"class"`
	SucceededRetentionSeconds int64                    `json:"succeeded_retention_seconds"`
	FailedRetentionSeconds    int64                    `json:"failed_retention_seconds"`
	MinimumRetentionSeconds   int64                    `json:"minimum_retention_seconds"`
}

type OperationsDefinition struct {
	Kind             string                    `json:"kind"`
	Owner            string                    `json:"owner"`
	ActionKey        string                    `json:"action_key"`
	ExecutionScope   OperationsExecutionScope  `json:"execution_scope"`
	ResourceType     string                    `json:"resource_type"`
	Preconditions    []string                  `json:"preconditions"`
	Idempotency      string                    `json:"idempotency"`
	AuditEvent       string                    `json:"audit_event"`
	ReceiptType      string                    `json:"receipt_type"`
	FailureSemantics []OperationsFailureClass  `json:"failure_semantics"`
	LongRunning      bool                      `json:"long_running"`
	Retention        OperationsRetentionPolicy `json:"retention"`
}
