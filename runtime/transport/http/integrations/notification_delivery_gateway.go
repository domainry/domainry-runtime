package integrations

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	"github.com/domainry/domainry-notification-sdk/deliverygateway"
)

func (h *IntegrationsHandler) acceptNotificationDelivery(w http.ResponseWriter, r *http.Request) {
	application, identity, ok := h.authenticateNotificationService(w, r)
	if !ok {
		return
	}
	var request deliverygateway.Request
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&request); err != nil {
		h.writeGatewayError(w, http.StatusBadRequest, "notification.delivery_request_invalid")
		return
	}
	if err := request.Validate(application); err != nil {
		h.writeGatewayError(w, http.StatusBadRequest, "notification.delivery_request_invalid")
		return
	}
	decision, err := h.identity.Authorization().Reauthorize(r.Context(), identitysdk.DecisionRequest{
		Identity: identity,
		Access:   identitysdk.AccessRequest{ObjectKey: "integration", Action: "invoke", RecordID: request.RequestID},
		Facts:    identitysdk.ResourceFacts{"tenant_id": application.TenantID, "workspace_id": application.WorkspaceID, "application_key": application.ApplicationKey, "request_id": request.RequestID},
	})
	if err != nil || !decision.Allowed {
		h.writeGatewayError(w, http.StatusForbidden, "notification.delivery_not_authorized")
		return
	}
	receipt, err := h.runtimeExecution.AcceptNotificationDelivery(r.Context(), request, h.productName)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, receipt)
}

func (h *IntegrationsHandler) authenticateNotificationService(w http.ResponseWriter, r *http.Request) (notificationsdk.ApplicationRef, identitysdk.RequestIdentity, bool) {
	application := notificationsdk.ApplicationRef{
		TenantID: strings.TrimSpace(r.Header.Get("X-Domainry-Tenant-ID")), WorkspaceID: strings.TrimSpace(r.Header.Get("X-Domainry-Workspace-ID")), ApplicationKey: strings.TrimSpace(r.Header.Get("X-Domainry-Application-Key")),
	}
	credential := strings.TrimSpace(r.Header.Get("X-Domainry-Service-Credential"))
	if h.identity == nil || h.identity.Tokens() == nil || h.identity.Authorization() == nil || h.identityAudience == "" || credential == "" || application.Validate() != nil {
		h.writeGatewayError(w, http.StatusUnauthorized, "notification.service_credential_invalid")
		return application, identitysdk.RequestIdentity{}, false
	}
	verified, err := h.identity.Tokens().Verify(r.Context(), identitysdk.VerifyTokenRequest{AccessToken: credential, Audience: identitysdk.ApplicationKey(h.identityAudience)})
	if err != nil || strings.TrimSpace(string(verified.SubjectID)) == "" || string(verified.TenantID) != application.TenantID || string(verified.WorkspaceID) != application.WorkspaceID || string(verified.Audience) != h.identityAudience {
		h.writeGatewayError(w, http.StatusUnauthorized, "notification.service_credential_invalid")
		return application, identitysdk.RequestIdentity{}, false
	}
	principal := identitysdk.Principal{ContractVersion: identitysdk.PrincipalContextContractVersion, Known: true, WorkspaceID: application.WorkspaceID, UserID: string(verified.SubjectID), AuthorizationRevision: string(verified.AuthorizationRevision)}
	return application, identitysdk.RequestIdentity{Principal: principal, AccessToken: credential}, true
}

func (h *IntegrationsHandler) writeGatewayError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(notificationsdk.Error{StatusCode: status, Code: code})
}
