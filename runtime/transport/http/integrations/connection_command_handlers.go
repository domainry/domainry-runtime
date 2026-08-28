package integrations

import (
	"context"
	"net/http"
	"strings"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	operationshttp "github.com/domainry/domainry-runtime/runtime/transport/http/operations"
)

func (h *IntegrationsHandler) validateIntegrationConnection(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationConnectionUpsertRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	connectionKey := strings.TrimSpace(r.PathValue("connectionKey"))
	if err := h.connections.ValidateIntegrationConnectionDraft(r.Context(), connectionKey, request, h.principal(r)); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"valid": true, "connection_key": connectionKey, "normalized": request})
}

func (h *IntegrationsHandler) upsertIntegrationConnection(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationConnectionUpsertRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	connectionKey := strings.TrimSpace(r.PathValue("connectionKey"))
	principal := h.principal(r)
	if h.operations == nil {
		connection, err := h.connections.UpsertIntegrationConnection(r.Context(), connectionKey, request, principal)
		if err != nil {
			h.writeServiceError(w, r, err)
			return
		}
		h.writeJSON(w, http.StatusOK, connection)
		return
	}
	result, err := h.operations.ExecuteDirectAuthoringUpsert(r.Context(), operationsapplication.DirectAuthoringUpsertRequest{
		CapabilityKey: "integration.connection", ResourceID: connectionKey,
		BuilderTaskID: strings.TrimSpace(r.Header.Get("Builder-Task-ID")), IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")), ExpectedResourceHash: strings.TrimSpace(r.Header.Get("Expected-Schema-Hash")), Payload: request,
	}, principal,
		func(context.Context) error { return h.connections.AuthorizeIntegrationConnectionUpsert(principal) },
		func(ctx context.Context) (string, bool, error) {
			return h.connections.IntegrationConnectionAuthoringHash(ctx, connectionKey, principal)
		},
		func(ctx context.Context) (any, error) {
			return h.connections.UpsertIntegrationConnection(ctx, connectionKey, request, principal)
		},
	)
	operationshttp.WriteOwnerReceiptHeaders(w, result)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result.Value)
}

func (h *IntegrationsHandler) disableIntegrationConnection(w http.ResponseWriter, r *http.Request) {
	connection, err := h.connections.DisableIntegrationConnection(r.Context(), strings.TrimSpace(r.PathValue("connectionKey")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, connection)
}

func (h *IntegrationsHandler) deleteIntegrationConnection(w http.ResponseWriter, r *http.Request) {
	if err := h.connections.DeleteIntegrationConnection(r.Context(), strings.TrimSpace(r.PathValue("connectionKey")), h.principal(r)); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *IntegrationsHandler) rotateIntegrationConnection(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationConnectionUpsertRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	connection, err := h.connections.RotateIntegrationConnection(r.Context(), strings.TrimSpace(r.PathValue("connectionKey")), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, connection)
}
