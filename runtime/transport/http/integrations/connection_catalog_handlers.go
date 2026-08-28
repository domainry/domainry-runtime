package integrations

import (
	"net/http"
	"strings"

	businessintegration "github.com/domainry/domainry-runtime/runtime/application/integration"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
)

func (h *IntegrationsHandler) listIntegrationConnectors(w http.ResponseWriter, r *http.Request) {
	connectors, catalogErr := h.connections.IntegrationConnectorCatalog(r.Context(), h.principal(r))
	if catalogErr != nil {
		h.writeServiceError(w, r, catalogErr)
		return
	}
	connections, err := h.connections.ListIntegrationConnections(r.Context(), h.principal(r))
	connectionsAvailable := err == nil
	if err != nil {
		connections = nil
	}
	contractHash, hashErr := integrationcontract.ConnectorContractHash(connectors)
	if hashErr != nil {
		h.writeServiceError(w, r, hashErr)
		return
	}
	w.Header().Set("ETag", `"`+contractHash+`"`)
	w.Header().Set("X-Connector-Contract-Version", integrationcontract.ConnectorContractVersion)
	connectors = localizeConnectorCatalog(connectors, h.locale(r))
	h.writeJSON(w, http.StatusOK, map[string]any{
		"contract_version": integrationcontract.ConnectorContractVersion, "contract_hash": contractHash,
		"connectors": connectors, "connections": connections, "connections_available": connectionsAvailable,
		"count": len(connectors),
	})
}

func (h *IntegrationsHandler) listIntegrationConnections(w http.ResponseWriter, r *http.Request) {
	connections, err := h.connections.ListIntegrationConnections(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"connections": connections, "count": len(connections)})
}

func (h *IntegrationsHandler) getIntegrationConnection(w http.ResponseWriter, r *http.Request) {
	connectionKey := strings.TrimSpace(r.PathValue("connectionKey"))
	principal := h.principal(r)
	connection, err := h.connections.GetIntegrationConnection(r.Context(), connectionKey, principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	resourceHash, found, err := h.connections.IntegrationConnectionAuthoringHash(r.Context(), connectionKey, principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if found {
		w.Header().Set("X-Resource-Hash", resourceHash)
	}
	h.writeJSON(w, http.StatusOK, connection)
}

func (h *IntegrationsHandler) listIntegrationConnectionVersions(w http.ResponseWriter, r *http.Request) {
	connectionKey := strings.TrimSpace(r.PathValue("connectionKey"))
	items, err := h.connections.IntegrationConnectionVersions(r.Context(), connectionKey, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"connection_key": connectionKey, "versioning": "audit_revision", "items": items, "count": len(items)})
}

func (h *IntegrationsHandler) testConnectorOperation(w http.ResponseWriter, r *http.Request) {
	var request businessintegration.ConnectorOperationTestRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	result, err := h.connections.TestConnectorOperation(r.Context(), strings.TrimSpace(r.PathValue("connectionKey")), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
