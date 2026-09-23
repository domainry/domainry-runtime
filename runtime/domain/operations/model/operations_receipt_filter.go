package operationsmodel

// OperationsReceiptFilter is the server-side query contract for the durable
// Runtime operation ledger. Filters are applied before the result limit.
type OperationsReceiptFilter struct {
	Status       OperationsStatus       `json:"status,omitempty"`
	FailureClass OperationsFailureClass `json:"failure_class,omitempty"`
	Owner        string                 `json:"owner,omitempty"`
	Kind         string                 `json:"kind,omitempty"`
	ParentID     string                 `json:"parent_operation_id,omitempty"`
	ResourceType string                 `json:"resource_type,omitempty"`
	ResourceID   string                 `json:"resource_id,omitempty"`
	RequestedBy  string                 `json:"requested_by,omitempty"`
	Correlation  string                 `json:"correlation,omitempty"`
	CreatedFrom  string                 `json:"created_from,omitempty"`
	CreatedTo    string                 `json:"created_to,omitempty"`
	Search       string                 `json:"search,omitempty"`
	Limit        int                    `json:"limit,omitempty"`
}

type OperationsReceiptPage struct {
	Items   []OperationsReceipt      `json:"items"`
	Count   int                      `json:"count"`
	Summary OperationsReceiptSummary `json:"summary"`
}

type OperationsReceiptSummary struct {
	Created            int `json:"created"`
	Started            int `json:"started"`
	Succeeded          int `json:"succeeded"`
	Failed             int `json:"failed"`
	ManualIntervention int `json:"manual_intervention"`
}
