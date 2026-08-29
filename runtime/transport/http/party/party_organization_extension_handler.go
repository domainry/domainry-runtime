package party

import (
	"net/http"
	"strings"

	partymodel "github.com/domainry/domainry-party-sdk/contract"
)

func (h *PartyHandler) listOrganizationExtensions(w http.ResponseWriter, r *http.Request) {
	values, err := h.dependencies.Catalog.ListOrganizationExtensions(r.Context(), h.dependencies.Principal(r))
	if err != nil {
		h.dependencies.WriteServiceError(w, r, err)
		return
	}
	h.dependencies.WriteJSON(w, http.StatusOK, map[string]any{"items": values, "total": len(values)})
}

func (h *PartyHandler) upsertOrganizationExtension(w http.ResponseWriter, r *http.Request) {
	value := partymodel.OrganizationExtension{}
	if !h.dependencies.DecodeJSON(w, r, &value) {
		return
	}
	value.ID = strings.TrimSpace(r.PathValue("extensionID"))
	result, err := h.dependencies.Catalog.UpsertOrganizationExtension(r.Context(), value, h.dependencies.Principal(r))
	if err != nil {
		h.dependencies.WriteServiceError(w, r, err)
		return
	}
	h.dependencies.SecurityAudit(r, "party_organization_extension_upserted", result.ID, map[string]any{"kind": result.Kind, "status": result.Status})
	h.dependencies.WriteJSON(w, http.StatusOK, result)
}

func (h *PartyHandler) listOrganizationMemberships(w http.ResponseWriter, r *http.Request) {
	values, err := h.dependencies.Catalog.ListOrganizationExtensionMemberships(r.Context(), strings.TrimSpace(r.URL.Query().Get("workforce_profile_id")), h.dependencies.Principal(r))
	if err != nil {
		h.dependencies.WriteServiceError(w, r, err)
		return
	}
	h.dependencies.WriteJSON(w, http.StatusOK, map[string]any{"items": values, "total": len(values)})
}

func (h *PartyHandler) upsertOrganizationMembership(w http.ResponseWriter, r *http.Request) {
	value := partymodel.OrganizationExtensionMembership{}
	if !h.dependencies.DecodeJSON(w, r, &value) {
		return
	}
	value.ID = strings.TrimSpace(r.PathValue("membershipID"))
	result, err := h.dependencies.Catalog.UpsertOrganizationExtensionMembership(r.Context(), value, h.dependencies.Principal(r))
	if err != nil {
		h.dependencies.WriteServiceError(w, r, err)
		return
	}
	h.dependencies.SecurityAudit(r, "party_organization_membership_upserted", result.ID, map[string]any{"extension_id": result.ExtensionID, "status": result.Status})
	h.dependencies.WriteJSON(w, http.StatusOK, result)
}
