package operationsmodel

type OperationsExecutionScope string

const (
	OperationsExecutionWorkspace OperationsExecutionScope = "workspace"
	OperationsExecutionSystem    OperationsExecutionScope = "system"
)

type OperationsDefinition struct {
	Kind             string                   `json:"kind"`
	Owner            string                   `json:"owner"`
	Permissions      []string                 `json:"permissions"`
	ExecutionScope   OperationsExecutionScope `json:"execution_scope"`
	ResourceType     string                   `json:"resource_type"`
	Preconditions    []string                 `json:"preconditions"`
	Idempotency      string                   `json:"idempotency"`
	AuditEvent       string                   `json:"audit_event"`
	ReceiptType      string                   `json:"receipt_type"`
	FailureSemantics []OperationsFailureClass `json:"failure_semantics"`
	LongRunning      bool                     `json:"long_running"`
}
