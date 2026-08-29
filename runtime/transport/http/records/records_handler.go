package records

import (
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/audit"
	metadataapplication "github.com/domainry/domainry-runtime/runtime/application/metadata"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	actionpolicy "github.com/domainry/domainry-runtime/runtime/domain/action/policy"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"

	"net/http"
	"strings"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"

	"github.com/domainry/domainry-foundation/logging"
)

type RecordsHandler struct {
	queries            *recordapplication.RecordApplicationService
	actions            *actionapplication.ActionApplicationService
	audit              *auditapplication.AuditApplicationService
	permissions        *metadataapplication.MetadataSchemaApplicationService
	principal          func(*http.Request) principalmodel.Principal
	writeJSON          func(http.ResponseWriter, int, any)
	writeError         func(http.ResponseWriter, *http.Request, int, string, ...string)
	writeServiceError  func(http.ResponseWriter, *http.Request, error)
	decodeJSON         func(http.ResponseWriter, *http.Request, any) bool
	streamPollInterval time.Duration
	streamHeartbeat    time.Duration
}

type RecordsDependencies struct {
	Queries           *recordapplication.RecordApplicationService
	Actions           *actionapplication.ActionApplicationService
	Audit             *auditapplication.AuditApplicationService
	Permissions       *metadataapplication.MetadataSchemaApplicationService
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	DecodeJSON        func(http.ResponseWriter, *http.Request, any) bool
}

func NewRecordsHandler(deps RecordsDependencies) *RecordsHandler {
	return &RecordsHandler{
		queries: deps.Queries, actions: deps.Actions, audit: deps.Audit, permissions: deps.Permissions, principal: deps.Principal,
		writeJSON: deps.WriteJSON, writeError: deps.WriteError, writeServiceError: deps.WriteServiceError, decodeJSON: deps.DecodeJSON,
		streamPollInterval: 500 * time.Millisecond, streamHeartbeat: 15 * time.Second,
	}
}

func (h *RecordsHandler) UseQueries(queries *recordapplication.RecordApplicationService) {
	h.queries = queries
}

func (h *RecordsHandler) listRecords(w http.ResponseWriter, r *http.Request) {
	page, err := h.queries.ListRecords(r.Context(), strings.TrimSpace(r.PathValue("objectKey")), parseListQuery(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, page)
}

func (h *RecordsHandler) exportRecords(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	content, filename, err := h.queries.ExportRecordsWithOptions(r.Context(), strings.TrimSpace(r.PathValue("objectKey")), h.principal(r), recordapplication.RecordExportOptions{
		Fields:         splitQueryCSV(query.Get("fields")),
		Reason:         query.Get("reason"),
		MaskingPolicy:  query.Get("masking_policy"),
		FilterSummary:  query.Get("filter_summary"),
		Query:          parseListQuery(r),
		AssuranceToken: r.Header.Get("X-Assurance-Token"),
	})
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(content); err != nil {
		logging.FromContext(r.Context()).Error("write record export failed", logging.StableErrorFields(err)...)
	}
}

func (h *RecordsHandler) createRecord(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Data         map[string]any                 `json:"data"`
		Translations recordmodel.RecordTranslations `json:"translations,omitempty"`
	}
	if !h.decodeJSON(w, r, &req) {
		return
	}
	key, _ := recordsActionIdempotencyKey(r, "")
	if key == "" {
		// Collection CRUD requires the public Runtime operation contract's
		// idempotency key. Reject before dispatch with its formal contract code;
		// the record application service keeps
		// backend.idempotency.key_required for non-HTTP/internal callers.
		h.writeActionServiceError(w, r, apperror.New(apperror.KindBadRequest, "operations.idempotency_contract_required", nil, map[string]string{"use_case": "record.create"}))
		return
	}
	record, replayed, err := h.queries.CreateLocalizedRecordIdempotentResult(r.Context(), strings.TrimSpace(r.PathValue("objectKey")), req.Data, req.Translations, key, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	h.writeJSON(w, http.StatusCreated, record)
}

func (h *RecordsHandler) getRecord(w http.ResponseWriter, r *http.Request) {
	record, err := h.queries.GetRecordLocalized(r.Context(), strings.TrimSpace(r.PathValue("objectKey")), strings.TrimSpace(r.PathValue("recordID")), recordRequestLocale(r), recordFallbackLocale(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, record)
}

func (h *RecordsHandler) recordReferences(w http.ResponseWriter, r *http.Request) {
	summary, err := h.queries.RecordReferences(r.Context(), strings.TrimSpace(r.PathValue("objectKey")), strings.TrimSpace(r.PathValue("recordID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, summary)
}

func (h *RecordsHandler) relatedRecords(w http.ResponseWriter, r *http.Request) {
	page, err := h.queries.RelatedRecords(
		r.Context(),
		strings.TrimSpace(r.PathValue("objectKey")),
		strings.TrimSpace(r.PathValue("recordID")),
		strings.TrimSpace(r.PathValue("relatedObjectKey")),
		recordservice.RecordRelatedRecordsRequest{
			Page:           intQuery(r.URL.Query().Get("page")),
			PageSize:       intQuery(r.URL.Query().Get("page_size")),
			FieldKey:       strings.TrimSpace(r.URL.Query().Get("field")),
			Locale:         recordRequestLocale(r),
			FallbackLocale: recordFallbackLocale(r),
		},
		h.principal(r),
	)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, page)
}

func (h *RecordsHandler) updateRecord(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Data         map[string]any                 `json:"data"`
		Translations recordmodel.RecordTranslations `json:"translations,omitempty"`
	}
	if !h.decodeJSON(w, r, &req) {
		return
	}
	if _, exists := req.Data["expected_updated_at"]; !exists {
		if expected := strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), `"`); expected != "" {
			if req.Data == nil {
				req.Data = map[string]any{}
			}
			req.Data["expected_updated_at"] = expected
		}
	}
	objectKey, recordID := strings.TrimSpace(r.PathValue("objectKey")), strings.TrimSpace(r.PathValue("recordID"))
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		h.writeActionServiceError(w, r, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, map[string]string{"use_case": "record.update"}))
		return
	}
	record, err := h.queries.UpdateLocalizedRecordIdempotent(r.Context(), objectKey, recordID, req.Data, req.Translations, idempotencyKey, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, record)
}

func (h *RecordsHandler) deactivateBusinessProfile(w http.ResponseWriter, r *http.Request) {
	var request struct {
		InactiveStatus    string `json:"inactive_status"`
		ExpectedUpdatedAt string `json:"expected_updated_at"`
		Reason            string `json:"reason"`
	}
	if !h.decodeJSON(w, r, &request) {
		return
	}
	record, err := h.queries.DeactivateBusinessProfile(
		r.Context(),
		strings.TrimSpace(r.PathValue("objectKey")),
		strings.TrimSpace(r.PathValue("recordID")),
		request.InactiveStatus,
		request.ExpectedUpdatedAt,
		strings.TrimSpace(r.Header.Get("Idempotency-Key")),
		h.principal(r),
	)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"record": record, "reason": strings.TrimSpace(request.Reason)})
}

func (h *RecordsHandler) reactivateBusinessProfile(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ActiveStatus      string `json:"active_status"`
		ExpectedUpdatedAt string `json:"expected_updated_at"`
		Reason            string `json:"reason"`
	}
	if !h.decodeJSON(w, r, &request) {
		return
	}
	record, err := h.queries.ReactivateBusinessProfile(
		r.Context(), strings.TrimSpace(r.PathValue("objectKey")), strings.TrimSpace(r.PathValue("recordID")),
		request.ActiveStatus, request.ExpectedUpdatedAt, strings.TrimSpace(r.Header.Get("Idempotency-Key")), h.principal(r),
	)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"record": record, "reason": strings.TrimSpace(request.Reason)})
}

func (h *RecordsHandler) deleteRecord(w http.ResponseWriter, r *http.Request) {
	expected := strings.TrimSpace(r.URL.Query().Get("expected_updated_at"))
	if expected == "" {
		expected = strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), `"`)
	}
	if err := h.queries.DeleteRecordExpected(r.Context(), strings.TrimSpace(r.PathValue("objectKey")), strings.TrimSpace(r.PathValue("recordID")), expected, h.principal(r)); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *RecordsHandler) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	events, err := h.audit.Events(r.Context(), auditmodel.AuditEventQuery{
		ObjectKey:   strings.TrimSpace(values.Get("object_key")),
		RecordID:    strings.TrimSpace(values.Get("record_id")),
		Event:       strings.TrimSpace(values.Get("event")),
		ActorID:     strings.TrimSpace(values.Get("actor_id")),
		RoleKey:     strings.TrimSpace(values.Get("role_key")),
		RequestID:   strings.TrimSpace(values.Get("request_id")),
		CreatedFrom: strings.TrimSpace(values.Get("created_from")),
		CreatedTo:   strings.TrimSpace(values.Get("created_to")),
		Limit:       intQuery(values.Get("limit")),
	}, h.principal(r))

	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, events)
}

func (h *RecordsHandler) listAuditOptions(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	options, err := h.audit.Options(r.Context(), auditmodel.AuditOptionQuery{
		Field:       strings.TrimSpace(values.Get("field")),
		Query:       strings.TrimSpace(values.Get("q")),
		ObjectKey:   strings.TrimSpace(values.Get("object_key")),
		CreatedFrom: strings.TrimSpace(values.Get("created_from")),
		CreatedTo:   strings.TrimSpace(values.Get("created_to")),
		Limit:       intQuery(values.Get("limit")),
	}, h.principal(r))

	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"options": options})
}

func (h *RecordsHandler) effectivePermissions(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	result, err := h.permissions.FeaturePermissions(r.Context(), principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if surface, ok := surfacemodel.ParseProductSurface(r.Header.Get("X-Domainry-Product-Surface")); ok && surface == surfacemodel.ProductSurfaceAdminConsole {
		result = runtimeOpsExactFeaturePermissions(result, principal)
	}
	objectKey := strings.TrimSpace(r.URL.Query().Get("object_key"))
	recordID := strings.TrimSpace(r.URL.Query().Get("record_id"))
	if (objectKey == "") != (recordID == "") {
		h.writeError(w, r, http.StatusBadRequest, "backend.permissions.record_context_required")
		return
	}
	if objectKey != "" {
		needsRecordDecision := false
		for _, action := range result.Actions {
			if action.ObjectKey == objectKey && action.Allowed && actionpolicy.ActionIsRecordKind(action.Kind) {
				needsRecordDecision = true
				break
			}
		}
		if needsRecordDecision {
			if h.queries == nil {
				h.writeServiceError(w, r, apperror.New(apperror.KindInternal, "backend.permissions.record_service_unavailable", nil, nil))
				return
			}
			allowed, scopeErr := h.queries.RecordScopeAllows(r.Context(), objectKey, recordID, principal)
			if scopeErr != nil {
				h.writeServiceError(w, r, scopeErr)
				return
			}
			if !allowed {
				for index := range result.Actions {
					action := &result.Actions[index]
					if action.ObjectKey == objectKey && action.Allowed && actionpolicy.ActionIsRecordKind(action.Kind) {
						action.Allowed = false
						action.Reason = "record_scope_denied"
					}
				}
			}
		}
	}
	h.writeJSON(w, http.StatusOK, result)
}

func runtimeOpsExactFeaturePermissions(result recordcontract.RecordFeaturePermissionSnapshot, principal principalmodel.Principal) recordcontract.RecordFeaturePermissionSnapshot {
	functions := make([]recordcontract.RecordFeatureFunctionPermission, 0, len(result.Functions))
	for _, permission := range result.Functions {
		if permission.Key == "workspace.admin" || !principal.HasExactPermission(permission.Key) {
			continue
		}
		permission.Decision.Reason = "allowed"
		functions = append(functions, permission)
	}
	result.Functions = functions
	return result
}
