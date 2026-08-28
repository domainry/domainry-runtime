package operationsmodel

type OperationsRunbookLink struct {
	ErrorCode   string   `json:"error_code"`
	Category    string   `json:"category"`
	URL         string   `json:"url"`
	NextActions []string `json:"next_actions"`
}
