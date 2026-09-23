package automation

import (
	"net/http"
	"strconv"
	"strings"

	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type AutomationHandler struct {
	commands          *automationapplication.AutomationApplicationService
	principal         func(*http.Request) principalmodel.Principal
	writeJSON         func(http.ResponseWriter, int, any)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
	decodeJSON        func(http.ResponseWriter, *http.Request, any) bool
}

type AutomationDependencies struct {
	Commands          *automationapplication.AutomationApplicationService
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	DecodeJSON        func(http.ResponseWriter, *http.Request, any) bool
}

func NewAutomationHandler(deps AutomationDependencies) *AutomationHandler {
	return &AutomationHandler{
		commands: deps.Commands, principal: deps.Principal, writeJSON: deps.WriteJSON,
		writeServiceError: deps.WriteServiceError, decodeJSON: deps.DecodeJSON,
	}
}

func (h *AutomationHandler) listAutomationExecutions(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(strings.TrimSpace(query.Get("limit")))
	history, err := h.commands.AutomationExecutions(r.Context(), automationmodel.AutomationExecutionFilter{
		RuleKey: query.Get("rule_key"), ObjectKey: query.Get("object_key"), RecordID: query.Get("record_id"),
		Phase: query.Get("phase"), Status: query.Get("status"), ConnectorKey: query.Get("connector_key"),
		From: query.Get("from"), To: query.Get("to"), Limit: limit,
	}, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, history)
}

func (h *AutomationHandler) listAutomationRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.commands.AutomationRules(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": rules, "count": len(rules)})
}

func (h *AutomationHandler) automationExecutionCatalog(w http.ResponseWriter, r *http.Request) {
	catalog, err := h.commands.AutomationExecutionCatalog(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, catalog)
}

func (h *AutomationHandler) getAutomationRule(w http.ResponseWriter, r *http.Request) {
	rule, err := h.commands.AutomationRule(r.Context(), strings.TrimSpace(r.PathValue("ruleKey")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, rule)
}

func (h *AutomationHandler) simulateAutomationRule(w http.ResponseWriter, r *http.Request) {
	var req automationcontract.AutomationSimulationRequest
	if !h.decodeJSON(w, r, &req) {
		return
	}
	result, err := h.commands.SimulateAutomationRule(r.Context(), strings.TrimSpace(r.PathValue("ruleKey")), req, h.principal(r))
	if err != nil {
		if strings.TrimSpace(result.RuleKey) != "" {
			h.writeJSON(w, http.StatusOK, result)
			return
		}
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
