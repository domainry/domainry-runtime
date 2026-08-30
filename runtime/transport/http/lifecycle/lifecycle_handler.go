package lifecycle

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	requestcontext "github.com/domainry/domainry-foundation/requestcontext"
	lifecyclemodel "github.com/domainry/domainry-lifecycle/model"
	lifecycleapplication "github.com/domainry/domainry-runtime/runtime/application/lifecycle"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type OperationRunner interface {
	ExecuteOwnerOperation(context.Context, operationsapplication.OperationsOwnerExecutionRequest, principalmodel.Principal, func(context.Context) (any, error)) (operationsapplication.OperationsOwnerExecutionResult, error)
}

type Dependencies struct {
	Service           *lifecycleapplication.LifecycleApplicationService
	Operations        OperationRunner
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	DecodeJSON        func(http.ResponseWriter, *http.Request, any) bool
	Authenticated     func(http.HandlerFunc) http.HandlerFunc
}

type Handler struct {
	service           *lifecycleapplication.LifecycleApplicationService
	operations        OperationRunner
	principal         func(*http.Request) principalmodel.Principal
	writeJSON         func(http.ResponseWriter, int, any)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
	decodeJSON        func(http.ResponseWriter, *http.Request, any) bool
	authenticated     func(http.HandlerFunc) http.HandlerFunc
}

func NewHandler(deps Dependencies) *Handler {
	return &Handler{service: deps.Service, operations: deps.Operations, principal: deps.Principal, writeJSON: deps.WriteJSON, writeServiceError: deps.WriteServiceError, decodeJSON: deps.DecodeJSON, authenticated: deps.Authenticated}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	routes := []struct {
		pattern string
		handler http.HandlerFunc
	}{
		{"GET /operations/lifecycle/policies", h.policies}, {"POST /operations/lifecycle/policies", h.publishPolicy},
		{"POST /operations/lifecycle/legal-holds", h.createLegalHold}, {"POST /operations/lifecycle/legal-holds/{holdID}/end", h.endLegalHold},
		{"GET /operations/lifecycle/cleanup/preview", h.cleanupPreview}, {"POST /operations/lifecycle/cleanup/jobs", h.createCleanupJob},
		{"POST /operations/lifecycle/cleanup/jobs/{jobID}/run", h.runCleanupJob}, {"GET /operations/lifecycle/metrics", h.metrics},
		{"GET /operations/lifecycle/archive", h.archiveEntries}, {"POST /operations/lifecycle/subjects", h.createSubjectRequest},
		{"POST /operations/lifecycle/subjects/{requestID}/verify", h.verifySubjectRequest}, {"POST /operations/lifecycle/subjects/{requestID}/preview", h.previewSubjectRequest},
		{"POST /operations/lifecycle/subjects/{requestID}/approve", h.approveSubjectRequest}, {"POST /operations/lifecycle/subjects/{requestID}/execute", h.executeSubjectRequest},
		{"GET /operations/lifecycle/subjects/{requestID}/download", h.downloadSubjectExport}, {"GET /operations/lifecycle/external-erasures", h.externalErasures},
		{"POST /operations/lifecycle/external-erasures/{erasureID}/reconcile", h.reconcileExternalErasure}, {"POST /operations/lifecycle/deletions/replay", h.replayDeletions},
	}
	for _, route := range routes {
		mux.HandleFunc(route.pattern, h.authenticated(route.handler))
	}
}

func (h *Handler) policies(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListPolicies(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}
func (h *Handler) publishPolicy(w http.ResponseWriter, r *http.Request) {
	var input lifecyclemodel.PolicyVersion
	if !h.decodeJSON(w, r, &input) {
		return
	}
	if input.Status == "" {
		input.Status = lifecyclemodel.PolicyStatusPublished
	}
	if input.PublishedAt.IsZero() {
		input.PublishedAt = time.Now().UTC()
	}
	result, err := h.service.PublishPolicy(r.Context(), input, h.principal(r))
	h.writeResult(w, r, result, err, http.StatusCreated)
}
func (h *Handler) createLegalHold(w http.ResponseWriter, r *http.Request) {
	var input lifecyclemodel.LegalHold
	if !h.decodeJSON(w, r, &input) {
		return
	}
	input.WorkspaceID = h.principal(r).WorkspaceID
	result, err := h.service.CreateLegalHold(r.Context(), input, h.principal(r))
	h.writeResult(w, r, result, err, http.StatusCreated)
}
func (h *Handler) endLegalHold(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Authority string    `json:"authority"`
		Evidence  string    `json:"evidence"`
		EndedAt   time.Time `json:"ended_at"`
	}
	if !h.decodeJSON(w, r, &input) {
		return
	}
	p := h.principal(r)
	result, err := h.service.EndLegalHold(r.Context(), p.WorkspaceID, r.PathValue("holdID"), input.Authority, input.Evidence, input.EndedAt, p)
	h.writeResult(w, r, result, err, http.StatusOK)
}
func (h *Handler) cleanupPreview(w http.ResponseWriter, r *http.Request) {
	p := h.principal(r)
	result, err := h.service.PreviewCleanup(r.Context(), p.WorkspaceID, strings.TrimSpace(r.URL.Query().Get("policy_key")), p, time.Now().UTC())
	h.writeResult(w, r, result, err, http.StatusOK)
}
func (h *Handler) createCleanupJob(w http.ResponseWriter, r *http.Request) {
	var input lifecyclemodel.CleanupJob
	if !h.decodeJSON(w, r, &input) {
		return
	}
	input.WorkspaceID = h.principal(r).WorkspaceID
	result, err := h.service.CreateCleanupJob(r.Context(), input, h.principal(r))
	h.writeResult(w, r, result, err, http.StatusAccepted)
}
func (h *Handler) runCleanupJob(w http.ResponseWriter, r *http.Request) {
	p := h.principal(r)
	batch, _ := strconv.Atoi(r.URL.Query().Get("batch_size"))
	jobID := strings.TrimSpace(r.PathValue("jobID"))
	run := func(ctx context.Context) (any, error) {
		return h.service.ProcessCleanupJob(ctx, p.WorkspaceID, jobID, "http-"+requestcontext.NewRequestID(), 2*time.Minute, batch, time.Now().UTC(), p)
	}
	if h.operations == nil {
		result, err := run(r.Context())
		h.writeResult(w, r, result, err, http.StatusOK)
		return
	}
	result, err := h.operations.ExecuteOwnerOperation(r.Context(), operationsapplication.OperationsOwnerExecutionRequest{Kind: "retention.cleanup", ResourceType: "retention_policy", ResourceID: jobID, Key: strings.TrimSpace(r.Header.Get("Idempotency-Key")), Reason: operationReason(r, "operator requested retention cleanup"), Reference: strings.TrimSpace(r.Header.Get("X-Operation-Reference")), Payload: map[string]any{"job_id": jobID, "batch_size": batch}}, p, run)
	writeOperationHeaders(w, result)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result.Value)
}
func (h *Handler) metrics(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.Metrics(r.Context(), h.principal(r), time.Now().UTC())
	h.writeResult(w, r, result, err, http.StatusOK)
}
func (h *Handler) archiveEntries(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	result, err := h.service.ListArchiveEntries(r.Context(), r.URL.Query().Get("source_table"), limit, h.principal(r))
	h.writeResult(w, r, result, err, http.StatusOK)
}
func (h *Handler) createSubjectRequest(w http.ResponseWriter, r *http.Request) {
	var input lifecyclemodel.SubjectRequest
	if !h.decodeJSON(w, r, &input) {
		return
	}
	input.WorkspaceID = h.principal(r).WorkspaceID
	result, err := h.service.CreateSubjectRequest(r.Context(), input, h.principal(r))
	h.writeSubject(w, r, result, err, http.StatusAccepted)
}
func (h *Handler) replayDeletions(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	p := h.principal(r)
	count, err := h.service.ReplayRegisteredDeletions(r.Context(), p.WorkspaceID, limit, p)
	h.writeResult(w, r, map[string]any{"replayed": count}, err, http.StatusOK)
}
func (h *Handler) verifySubjectRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SecondFactorRef string `json:"second_factor_ref"`
	}
	if !h.decodeJSON(w, r, &input) {
		return
	}
	p := h.principal(r)
	result, err := h.service.VerifySubjectRequest(r.Context(), p.WorkspaceID, r.PathValue("requestID"), input.SecondFactorRef, p)
	h.writeSubject(w, r, result, err, http.StatusOK)
}
func (h *Handler) previewSubjectRequest(w http.ResponseWriter, r *http.Request) {
	p := h.principal(r)
	result, err := h.service.PreviewSubjectRequest(r.Context(), p.WorkspaceID, r.PathValue("requestID"), p)
	h.writeSubject(w, r, result, err, http.StatusOK)
}
func (h *Handler) approveSubjectRequest(w http.ResponseWriter, r *http.Request) {
	p := h.principal(r)
	result, err := h.service.ApproveSubjectRequest(r.Context(), p.WorkspaceID, r.PathValue("requestID"), p)
	h.writeSubject(w, r, result, err, http.StatusOK)
}
func (h *Handler) executeSubjectRequest(w http.ResponseWriter, r *http.Request) {
	p := h.principal(r)
	result, err := h.service.ExecuteSubjectRequest(r.Context(), p.WorkspaceID, r.PathValue("requestID"), p)
	h.writeSubject(w, r, result, err, http.StatusOK)
}
func (h *Handler) downloadSubjectExport(w http.ResponseWriter, r *http.Request) {
	p := h.principal(r)
	result, err := h.service.DownloadSubjectExport(r.Context(), p.WorkspaceID, r.PathValue("requestID"), p, time.Now().UTC())
	h.writeResult(w, r, map[string]any{"data": result}, err, http.StatusOK)
}
func (h *Handler) externalErasures(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.ListExternalErasures(r.Context(), r.URL.Query().Get("request_id"), h.principal(r))
	h.writeResult(w, r, map[string]any{"items": result, "count": len(result)}, err, http.StatusOK)
}
func (h *Handler) reconcileExternalErasure(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Evidence string `json:"evidence"`
	}
	if !h.decodeJSON(w, r, &input) {
		return
	}
	result, err := h.service.ReconcileExternalErasure(r.Context(), r.PathValue("erasureID"), input.Evidence, h.principal(r), time.Now().UTC())
	h.writeResult(w, r, result, err, http.StatusOK)
}
func (h *Handler) writeSubject(w http.ResponseWriter, r *http.Request, result lifecyclemodel.SubjectRequest, err error, status int) {
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	result.SubjectID, result.ResolvedIdentity, result.SecondFactorRef, result.ResultReference = "", "", "", ""
	result.ImpactPreview = nil
	h.writeJSON(w, status, result)
}
func (h *Handler) writeResult(w http.ResponseWriter, r *http.Request, result any, err error, status int) {
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, status, result)
}
func operationReason(r *http.Request, fallback string) string {
	if reason := strings.TrimSpace(r.Header.Get("X-Operation-Reason")); reason != "" {
		return reason
	}
	return fallback
}
func writeOperationHeaders(w http.ResponseWriter, result operationsapplication.OperationsOwnerExecutionResult) {
	if strings.TrimSpace(result.Receipt.Command.ID) == "" {
		return
	}
	w.Header().Set("Operation-ID", result.Receipt.Command.ID)
	w.Header().Set("Operation-Location", result.Receipt.StatusURL)
	w.Header().Set("Location", result.Receipt.StatusURL)
	if result.Replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
}
