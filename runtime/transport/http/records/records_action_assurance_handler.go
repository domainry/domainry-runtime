package records

import (
	"net/http"
	"strings"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
)

func (h *RecordsHandler) beginActionAssurance(w http.ResponseWriter, r *http.Request) {
	if h.assurance == nil {
		h.writeServiceError(w, r, apperror.New(apperror.KindUnavailable, "backend.action.assurance_unavailable", nil, nil))
		return
	}
	var request actionapplication.ActionAssuranceChallengeRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	challenge, err := h.assurance.Begin(r.Context(), request, actionAssuranceBearerToken(r), h.principal(r))
	if err != nil {
		h.auditActionAssurance(r, "action_assurance_challenge_denied", request.ActionKey, request.ObjectKey, request.RecordID, "Action assurance challenge denied", map[string]any{"error_code": apperror.CodeOf(err)})
		h.writeServiceError(w, r, err)
		return
	}
	h.auditActionAssurance(r, "action_assurance_challenge_started", request.ActionKey, request.ObjectKey, request.RecordID, "Action assurance challenge started", map[string]any{"provider": challenge.Provider, "status": challenge.Status, "masked_destination": challenge.MaskedDestination, "expires_at": challenge.ExpiresAt})
	h.writeJSON(w, http.StatusOK, identitysdk.AuthenticationOutcome{Status: identitysdk.AuthenticationStatusChallengeRequired, Challenge: &challenge})

}

func (h *RecordsHandler) verifyActionAssurance(w http.ResponseWriter, r *http.Request) {
	if h.assurance == nil {
		h.writeServiceError(w, r, apperror.New(apperror.KindUnavailable, "backend.action.assurance_unavailable", nil, nil))
		return
	}
	var request actionapplication.ActionAssuranceVerificationRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	result, err := h.assurance.Verify(r.Context(), request, actionAssuranceBearerToken(r), h.principal(r))
	if err != nil {
		h.auditActionAssurance(r, "action_assurance_verification_denied", request.ActionKey, request.ObjectKey, request.RecordID, "Action assurance verification denied", map[string]any{"error_code": apperror.CodeOf(err)})
		h.writeServiceError(w, r, err)
		return
	}
	h.auditActionAssurance(r, "action_assurance_grant_issued", request.ActionKey, request.ObjectKey, request.RecordID, "Action assurance grant issued", map[string]any{"grant_id": result.GrantID, "methods": result.Methods, "expires_at": result.ExpiresAt})
	h.writeJSON(w, http.StatusOK, result)
}

func (h *RecordsHandler) auditActionAssurance(request *http.Request, event, actionKey, objectKey, recordID, summary string, metadata map[string]any) {
	if h == nil || h.audit == nil {
		return
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["action_key"] = strings.TrimSpace(actionKey)
	h.audit.AppendWithMetadata(request.Context(), auditmodel.EventFamilyBusinessAction, event, strings.TrimSpace(objectKey), strings.TrimSpace(recordID), h.principal(request), summary, nil, nil, metadata)
}

func actionAssuranceBearerToken(request *http.Request) string {
	parts := strings.Fields(strings.TrimSpace(request.Header.Get("Authorization")))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
