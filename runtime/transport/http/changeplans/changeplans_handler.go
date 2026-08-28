package changeplans

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/domainry/domainry-foundation/idempotency"
	changeplan "github.com/domainry/domainry-runtime/runtime/application/changeplan"
	changeplancontract "github.com/domainry/domainry-runtime/runtime/domain/changeplan/contract"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanpolicy "github.com/domainry/domainry-runtime/runtime/domain/changeplan/policy"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ChangePlansHandler struct {
	service           *changeplan.ChangePlanApplicationService
	principal         func(*http.Request) principalmodel.Principal
	snapshot          func(*http.Request) (changeplancontract.SnapshotSource, error)
	graph             func(*http.Request) (changeplancontract.ReferenceGraphSource, error)
	writeJSON         func(http.ResponseWriter, int, any)
	writeError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
	decodeJSON        func(http.ResponseWriter, *http.Request, any) bool
}

type ChangePlansDependencies struct {
	Service           *changeplan.ChangePlanApplicationService
	Principal         func(*http.Request) principalmodel.Principal
	Snapshot          func(*http.Request) (changeplancontract.SnapshotSource, error)
	Graph             func(*http.Request) (changeplancontract.ReferenceGraphSource, error)
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	DecodeJSON        func(http.ResponseWriter, *http.Request, any) bool
}

func NewChangePlansHandler(deps ChangePlansDependencies) *ChangePlansHandler {
	return &ChangePlansHandler{
		service: deps.Service, principal: deps.Principal, snapshot: deps.Snapshot, graph: deps.Graph,
		writeJSON: deps.WriteJSON, writeError: deps.WriteError,
		writeServiceError: deps.WriteServiceError, decodeJSON: deps.DecodeJSON,
	}
}

func (h *ChangePlansHandler) getDraft(w http.ResponseWriter, r *http.Request) {
	draft, err := h.service.Draft(r.Context(), strings.TrimSpace(r.PathValue("planID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, draft)
}

func (h *ChangePlansHandler) saveDraft(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ExpectedRevision int                                      `json:"expected_revision"`
		Plan             changeplanmodel.BusinessSystemChangePlan `json:"plan"`
	}
	if !h.decodeJSON(w, r, &request) {
		return
	}
	planID := strings.TrimSpace(r.PathValue("planID"))
	if request.Plan.PlanID == "" {
		request.Plan.PlanID = planID
	}
	if request.Plan.PlanID != planID {
		h.writeError(w, r, http.StatusBadRequest, "backend.change_plan.draft_identity_mismatch")
		return
	}
	draft, err := h.service.SaveDraft(r.Context(), request.Plan, request.ExpectedRevision, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, draft)
}

func (h *ChangePlansHandler) cloneCurrent(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ExpectedRevision int    `json:"expected_revision"`
		BusinessReason   string `json:"business_reason"`
	}
	if !h.decodeJSON(w, r, &request) {
		return
	}
	snapshot, err := h.snapshot(r)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	graph, err := h.graph(r)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	draft, err := h.service.CloneCurrentDraft(r.Context(), strings.TrimSpace(r.PathValue("planID")), request.BusinessReason, request.ExpectedRevision, snapshot, graph, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, draft)
}

func (h *ChangePlansHandler) simulateScenarios(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ExpectedRevision int `json:"expected_revision"`
	}
	if !h.decodeJSON(w, r, &request) {
		return
	}
	result, err := h.service.SimulateDraftScenarios(r.Context(), strings.TrimSpace(r.PathValue("planID")), request.ExpectedRevision, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *ChangePlansHandler) rollbackPolicy(w http.ResponseWriter, r *http.Request) {
	policy, err := changeplanpolicy.ChangePlanBusinessMaintenanceRollbackPolicy(h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, policy)
}

func (h *ChangePlansHandler) validate(w http.ResponseWriter, r *http.Request) {
	var plan changeplanmodel.BusinessSystemChangePlan
	if !h.decodeJSON(w, r, &plan) {
		return
	}
	plan.Reviewed, plan.ReviewedBy = false, ""
	snapshot, err := h.snapshot(r)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	graph, err := h.graph(r)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	result, err := h.service.Validate(r.Context(), plan, snapshot, graph, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *ChangePlansHandler) review(w http.ResponseWriter, r *http.Request) {
	h.transitionReview(w, r, false)
}

func (h *ChangePlansHandler) approve(w http.ResponseWriter, r *http.Request) {
	h.transitionReview(w, r, true)
}

func (h *ChangePlansHandler) exportPackage(w http.ResponseWriter, r *http.Request) {
	revision, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("revision")))
	if err != nil || revision <= 0 {
		h.writeError(w, r, http.StatusBadRequest, "backend.change_plan.draft_revision_required")
		return
	}
	result, err := h.service.ExportPackage(r.Context(), strings.TrimSpace(r.PathValue("planID")), revision, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+result.SourcePlanID+`.system-package.json"`)
	h.writeJSON(w, http.StatusOK, result)
}

func (h *ChangePlansHandler) importPackage(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ExpectedRevision int                                   `json:"expected_revision"`
		BusinessReason   string                                `json:"business_reason"`
		Package          changeplanmodel.BusinessSystemPackage `json:"package"`
	}
	if !h.decodeJSON(w, r, &request) {
		return
	}
	snapshot, err := h.snapshot(r)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	graph, err := h.graph(r)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	draft, err := h.service.ImportPackageDraft(r.Context(), strings.TrimSpace(r.PathValue("planID")), request.BusinessReason, request.ExpectedRevision, request.Package, snapshot, graph, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, draft)
}

func (h *ChangePlansHandler) transitionReview(w http.ResponseWriter, r *http.Request, approve bool) {
	var request struct {
		ExpectedRevision int `json:"expected_revision"`
	}
	if !h.decodeJSON(w, r, &request) {
		return
	}
	snapshot, err := h.snapshot(r)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	graph, err := h.graph(r)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	planID := strings.TrimSpace(r.PathValue("planID"))
	var draft changeplanmodel.BusinessChangePlanDraft
	var validation changeplanmodel.BusinessChangePlanValidation
	if approve {
		draft, validation, err = h.service.ApproveDraft(r.Context(), planID, request.ExpectedRevision, snapshot, graph, h.principal(r))
	} else {
		draft, validation, err = h.service.SubmitDraftForReview(r.Context(), planID, request.ExpectedRevision, snapshot, graph, h.principal(r))
	}
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"draft": draft, "validation": validation})
}

func (h *ChangePlansHandler) apply(w http.ResponseWriter, r *http.Request) {
	var request struct {
		PlanID           string `json:"plan_id"`
		ExpectedRevision int    `json:"expected_revision"`
		Confirmation     string `json:"confirmation"`
	}
	if !h.decodeJSON(w, r, &request) {
		return
	}
	operationKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if operationKey == "" {
		h.writeError(w, r, http.StatusBadRequest, idempotency.ErrorCodeMissingKey)
		return
	}
	snapshot, err := h.snapshot(r)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	graph, err := h.graph(r)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	result, replayed, err := h.service.PublishApprovedDraftIdempotent(r.Context(), request.PlanID, request.ExpectedRevision, request.Confirmation, operationKey, snapshot, graph, h.principal(r))
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	current, err := h.snapshot(r)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"result": result, "current_snapshot": current})
}
