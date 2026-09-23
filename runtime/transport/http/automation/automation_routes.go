package automation

import "net/http"

func (h *AutomationHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /automation/rules", h.listAutomationRules)
	mux.HandleFunc("GET /automation/execution-catalog", h.automationExecutionCatalog)
	mux.HandleFunc("GET /automation/executions", h.listAutomationExecutions)
	mux.HandleFunc("GET /automation/rules/{ruleKey}", h.getAutomationRule)
	mux.HandleFunc("POST /automation/rules/{ruleKey}/simulate", h.simulateAutomationRule)
}
