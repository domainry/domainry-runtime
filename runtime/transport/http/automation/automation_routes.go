package automation

import "net/http"

func (h *AutomationHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /automation-rules", h.listAutomationRules)
	mux.HandleFunc("GET /automation-rules/capabilities", h.automationCapabilities)
	mux.HandleFunc("GET /automation-rules/executions", h.listAutomationExecutions)
	mux.HandleFunc("POST /automation-rules/validate", h.validateAutomationRule)
	mux.HandleFunc("POST /automation-rules/authoring-fragments/{capabilityKey}/validate", h.validateAutomationAuthoringFragment)
	mux.HandleFunc("POST /automation-rules/simulate", h.simulateAutomationRule)
	mux.HandleFunc("GET /automation-rules/{ruleKey}", h.getAutomationRule)
	mux.HandleFunc("POST /automation-rules/{ruleKey}/simulate", h.simulateAutomationRule)
}
