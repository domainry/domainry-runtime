package capabilities

import (
	"crypto/sha256"
	"encoding/hex"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	"net/http"
	"strings"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type CapabilitiesHandler struct {
	service           *capabilityapplication.CapabilityAuthoringApplicationService
	principal         func(*http.Request) principalmodel.Principal
	writeJSON         func(http.ResponseWriter, int, any)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
}

type CapabilitiesDependencies struct {
	Service           *capabilityapplication.CapabilityAuthoringApplicationService
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
}

func NewCapabilitiesHandler(deps CapabilitiesDependencies) *CapabilitiesHandler {
	return &CapabilitiesHandler{
		service: deps.Service, principal: deps.Principal,
		writeJSON: deps.WriteJSON, writeServiceError: deps.WriteServiceError,
	}
}

func WriteLegacyProjectionHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Domainry-Capability-Mode", "compatibility-projection")
	w.Header().Set("Link", `</capabilities>; rel="successor-version"`)
}

func (h *CapabilitiesHandler) platformCapabilities(w http.ResponseWriter, r *http.Request) {
	principal := h.discoveryPrincipal(r)
	contract, err := h.service.Capabilities(r.Context(), principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeDiscoveryResponse(w, r, contract.ContractHash, contract.InstanceHash, contract)
}

func (h *CapabilitiesHandler) capabilityIndex(w http.ResponseWriter, r *http.Request) {
	includeEndpointContracts := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include")), "endpoint_contracts")
	result, err := h.service.DiscoveryIndexExpanded(r.Context(), h.discoveryPrincipal(r), includeEndpointContracts)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeDiscoveryResponse(w, r, result.ContractHash, result.InstanceHash, result)
}

func (h *CapabilitiesHandler) capabilityDomain(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.DomainCapabilities(r.Context(), h.discoveryPrincipal(r), r.PathValue("domainKey"), capabilityapplication.CapabilityDiscoveryFilter{
		Status: r.URL.Query().Get("status"), Requires: r.URL.Query().Get("requires"),
	})
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeDiscoveryResponse(w, r, result.ContractHash, result.InstanceHash, result)
}

func (h *CapabilitiesHandler) capabilityDetail(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.CapabilityDetailSelected(r.Context(), h.discoveryPrincipal(r), r.PathValue("capabilityKey"), capabilityapplication.CapabilityDetailSelection{
		ObjectKey: r.URL.Query().Get("object_key"), ConnectorKey: r.URL.Query().Get("connector_key"), ProviderKey: r.URL.Query().Get("provider_key"), OperationKey: r.URL.Query().Get("operation_key"),
	})
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeDiscoveryResponse(w, r, result.ContractHash, result.InstanceHash, result)
}

func (h *CapabilitiesHandler) capabilityReferences(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.ReferenceValues(r.Context(), h.discoveryPrincipal(r), r.PathValue("kind"), r.URL.Query().Get("scope"))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeDiscoveryResponse(w, r, capabilityapplication.RuntimeAuthoringContractHash, result.InstanceHash, result)
}

func (h *CapabilitiesHandler) discoveryPrincipal(r *http.Request) principalmodel.Principal {
	return h.principal(r)
}

func (h *CapabilitiesHandler) writeDiscoveryResponse(w http.ResponseWriter, r *http.Request, contractHash, instanceHash string, value any) {
	if err := capabilityapplication.ValidateCapabilityDiscoveryHashes(r.Header.Get("If-Match-Contract-Hash"), contractHash, r.Header.Get("If-Match-Instance-Hash"), instanceHash); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	responseHash := sha256.Sum256([]byte(contractHash + ":" + instanceHash))
	etag := `"` + hex.EncodeToString(responseHash[:]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	if strings.TrimSpace(r.Header.Get("If-None-Match")) == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}
