package operationsmodel

import "time"

type OperationsDiagnosticsRequest struct {
	WorkspaceID string
	InstanceID  string
	Sections    []string
	Page        int
	PageSize    int
	Now         time.Time
}

type OperationsDiagnosticsSection struct {
	Status     string           `json:"status"`
	Summary    map[string]any   `json:"summary,omitempty"`
	Items      []map[string]any `json:"items,omitempty"`
	NextPage   int              `json:"next_page,omitempty"`
	ErrorCode  string           `json:"error_code,omitempty"`
	RunbookURL string           `json:"runbook_url,omitempty"`
}

type OperationsDiagnosticsSnapshot struct {
	CapturedAt time.Time                               `json:"captured_at"`
	Redacted   bool                                    `json:"redacted"`
	Page       int                                     `json:"page"`
	PageSize   int                                     `json:"page_size"`
	CostUnits  int                                     `json:"cost_units"`
	Sections   map[string]OperationsDiagnosticsSection `json:"sections"`
}
