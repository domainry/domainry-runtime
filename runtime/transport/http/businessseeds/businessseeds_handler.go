package businessseeds

import (
	"context"
	"net/http"
	"strings"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	operationshttp "github.com/domainry/domainry-runtime/runtime/transport/http/operations"
)

type BusinessSeedAuthoringService interface {
	Validate(context.Context, string, businessseedmodel.SeedRecordAuthoringRequest, principalmodel.Principal) (businessseedmodel.SeedRecordAuthoringResult, error)
	Apply(context.Context, string, businessseedmodel.SeedRecordAuthoringRequest, principalmodel.Principal) (businessseedmodel.SeedRecordAuthoringResult, error)
	Get(context.Context, string, principalmodel.Principal) (businessseedmodel.SeedRecordAuthoringResult, error)
	Versions(context.Context, string, principalmodel.Principal) ([]businessseedmodel.SeedRecordAuthoringResult, error)
	AuthorizeSeedRecordUpsert(principalmodel.Principal) error
	SeedRecordAuthoringHash(context.Context, string, principalmodel.Principal) (string, bool, error)
}

type BusinessSeedDependencies struct {
	Service           BusinessSeedAuthoringService
	Operations        *operationsapplication.OperationsApplicationService
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	DecodeJSON        func(http.ResponseWriter, *http.Request, any) bool
}

type BusinessSeedHandler struct {
	dependencies BusinessSeedDependencies
}

func NewBusinessSeedHandler(dependencies BusinessSeedDependencies) *BusinessSeedHandler {
	return &BusinessSeedHandler{dependencies: dependencies}
}

func (h *BusinessSeedHandler) get(w http.ResponseWriter, r *http.Request) {
	result, err := h.dependencies.Service.Get(r.Context(), strings.TrimSpace(r.PathValue("seedKey")), h.dependencies.Principal(r))
	if err != nil {
		h.dependencies.WriteServiceError(w, r, err)
		return
	}
	h.dependencies.WriteJSON(w, http.StatusOK, result)
}

func (h *BusinessSeedHandler) versions(w http.ResponseWriter, r *http.Request) {
	items, err := h.dependencies.Service.Versions(r.Context(), strings.TrimSpace(r.PathValue("seedKey")), h.dependencies.Principal(r))
	if err != nil {
		h.dependencies.WriteServiceError(w, r, err)
		return
	}
	h.dependencies.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items), "versioning": "immutable_singleton"})
}

func (h *BusinessSeedHandler) validate(w http.ResponseWriter, r *http.Request) {
	var request businessseedmodel.SeedRecordAuthoringRequest
	if !h.dependencies.DecodeJSON(w, r, &request) {
		return
	}
	result, err := h.dependencies.Service.Validate(r.Context(), strings.TrimSpace(r.PathValue("seedKey")), request, h.dependencies.Principal(r))
	if err != nil {
		h.dependencies.WriteServiceError(w, r, err)
		return
	}
	h.dependencies.WriteJSON(w, http.StatusOK, result)
}

func (h *BusinessSeedHandler) apply(w http.ResponseWriter, r *http.Request) {
	var request businessseedmodel.SeedRecordAuthoringRequest
	if !h.dependencies.DecodeJSON(w, r, &request) {
		return
	}
	seedKey, principal := strings.TrimSpace(r.PathValue("seedKey")), h.dependencies.Principal(r)
	if h.dependencies.Operations == nil {
		result, err := h.dependencies.Service.Apply(r.Context(), seedKey, request, principal)
		if err != nil {
			h.dependencies.WriteServiceError(w, r, err)
			return
		}
		h.dependencies.WriteJSON(w, http.StatusOK, result)
		return
	}
	result, err := h.dependencies.Operations.ExecuteDirectAuthoringUpsert(r.Context(), operationsapplication.DirectAuthoringUpsertRequest{
		CapabilityKey: "seed.record", ResourceID: seedKey,
		BuilderTaskID: strings.TrimSpace(r.Header.Get("Builder-Task-ID")), IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")), ExpectedResourceHash: strings.TrimSpace(r.Header.Get("Expected-Schema-Hash")), Payload: request,
	}, principal,
		func(context.Context) error { return h.dependencies.Service.AuthorizeSeedRecordUpsert(principal) },
		func(ctx context.Context) (string, bool, error) {
			return h.dependencies.Service.SeedRecordAuthoringHash(ctx, seedKey, principal)
		},
		func(ctx context.Context) (any, error) {
			return h.dependencies.Service.Apply(ctx, seedKey, request, principal)
		},
	)
	operationshttp.WriteOwnerReceiptHeaders(w, result)
	if err != nil {
		h.dependencies.WriteServiceError(w, r, err)
		return
	}
	h.dependencies.WriteJSON(w, http.StatusOK, result.Value)
}
