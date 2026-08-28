package integrations

import "net/http"

func (h *IntegrationsHandler) RegisterRoutes(mux *http.ServeMux) {
	authenticated := h.authenticated
	if authenticated == nil {
		authenticated = h.admin
		h.authenticated = authenticated
	}
	mux.HandleFunc("GET /tenant-admin/integrations/catalog", authenticated(h.tenantAdminIntegrationCatalog))
	mux.HandleFunc("GET /tenant-admin/integrations/secrets", authenticated(h.tenantAdminIntegrationSecrets))
	mux.HandleFunc("PUT /tenant-admin/integrations/secrets/{secretKey}", authenticated(h.upsertIntegrationSecret))
	mux.HandleFunc("POST /tenant-admin/integrations/secrets/{secretKey}/disable", authenticated(h.disableIntegrationSecret))
	mux.HandleFunc("POST /tenant-admin/integrations/secrets/{secretKey}/rotate", authenticated(h.rotateIntegrationSecret))
	mux.HandleFunc("POST /tenant-admin/integrations/secrets/{secretKey}/expire", authenticated(h.expireIntegrationSecret))
	mux.HandleFunc("POST /tenant-admin/integrations/secrets/{secretKey}/revoke", authenticated(h.revokeIntegrationSecret))
	mux.HandleFunc("GET /tenant-admin/integrations/connections", authenticated(h.listIntegrationConnections))
	mux.HandleFunc("GET /tenant-admin/integrations/connections/{connectionKey}", authenticated(h.getIntegrationConnection))
	mux.HandleFunc("GET /tenant-admin/integrations/connections/{connectionKey}/versions", authenticated(h.listIntegrationConnectionVersions))
	mux.HandleFunc("POST /tenant-admin/integrations/connections/{connectionKey}/validate", authenticated(h.validateIntegrationConnection))
	mux.HandleFunc("PUT /tenant-admin/integrations/connections/{connectionKey}", authenticated(h.upsertIntegrationConnection))
	mux.HandleFunc("DELETE /tenant-admin/integrations/connections/{connectionKey}", authenticated(h.deleteIntegrationConnection))
	mux.HandleFunc("POST /tenant-admin/integrations/connections/{connectionKey}/disable", authenticated(h.disableIntegrationConnection))
	mux.HandleFunc("POST /tenant-admin/integrations/connections/{connectionKey}/rotate", authenticated(h.rotateIntegrationConnection))
	mux.HandleFunc("POST /tenant-admin/integrations/connections/{connectionKey}/test-operation", authenticated(h.testConnectorOperation))
	mux.HandleFunc("POST /tenant-admin/integrations/connections/{connectionKey}/oauth/google/start", authenticated(h.startGoogleOAuth))
	mux.HandleFunc("POST /tenant-admin/integrations/bindings/validate", authenticated(h.validateIntegrationBinding))
	mux.HandleFunc("GET /tenant-admin/integrations/connectors", authenticated(h.listIntegrationConnectors))
	mux.HandleFunc("GET /tenant-admin/integrations/external-identities", authenticated(h.listIntegrationExternalIdentities))
	mux.HandleFunc("PUT /tenant-admin/integrations/external-identities/{identityKey}", authenticated(h.upsertIntegrationExternalIdentity))
	mux.HandleFunc("POST /tenant-admin/integrations/external-identities/{identityKey}/disable", authenticated(h.disableIntegrationExternalIdentity))
	mux.HandleFunc("POST /tenant-admin/integrations/external-identities/resolve", authenticated(h.resolveIntegrationExternalIdentity))
	mux.HandleFunc("GET /tenant-admin/integrations/api-keys", authenticated(h.listIntegrationAPIKeys))
	mux.HandleFunc("POST /tenant-admin/integrations/api-keys", authenticated(h.createIntegrationAPIKey))
	mux.HandleFunc("POST /tenant-admin/integrations/api-keys/{apiKey}/disable", authenticated(h.disableIntegrationAPIKey))
	mux.HandleFunc("POST /tenant-admin/integrations/api-keys/{apiKey}/rotate", authenticated(h.rotateIntegrationAPIKey))
	mux.HandleFunc("GET /tenant-admin/integrations/webhook-subscriptions", authenticated(h.listIntegrationWebhookSubscriptions))
	mux.HandleFunc("PUT /tenant-admin/integrations/webhook-subscriptions/{subscriptionKey}", authenticated(h.upsertIntegrationWebhookSubscription))
	mux.HandleFunc("DELETE /tenant-admin/integrations/webhook-subscriptions/{subscriptionKey}", authenticated(h.deleteIntegrationWebhookSubscription))
	mux.HandleFunc("POST /tenant-admin/integrations/webhook-subscriptions/{subscriptionKey}/disable", authenticated(h.disableIntegrationWebhookSubscription))
	mux.HandleFunc("POST /tenant-admin/integrations/webhook-subscriptions/publish", authenticated(h.publishIntegrationWebhookEvent))

	mux.HandleFunc("GET /operations/integrations/activity", h.authenticated(h.opsIntegrationActivity))
	mux.HandleFunc("POST /operations/integrations/events/process-due", h.authenticated(h.processDueOpsIntegrationEvents))
	mux.HandleFunc("POST /operations/integrations/events/{eventID}/retry", h.authenticated(h.retryOpsIntegrationEvent))
	mux.HandleFunc("POST /operations/integrations/events/{eventID}/replay", h.authenticated(h.replayOpsIntegrationEvent))
	mux.HandleFunc("POST /operations/integrations/outbox/process-due", h.authenticated(h.processDueOpsIntegrationOutbox))
	mux.HandleFunc("POST /operations/integrations/outbox/{messageID}/retry", h.authenticated(h.retryOpsIntegrationOutbox))
	mux.HandleFunc("GET /business/integration-intents/{messageID}", h.authenticated(h.getBusinessIntegrationIntent))

	mux.HandleFunc("POST /integrations/entrypoints/workflows/{workflowKey}/run", h.entrypoint(h.runIntegrationWorkflow))
	mux.HandleFunc("POST /integrations/entrypoints/objects/{objectKey}/records/{recordID}/actions/{actionKey}", h.entrypoint(h.executeIntegrationAction))
	mux.HandleFunc("POST /integrations/agents/{agentKey}/tools/{toolKey}/invoke", h.entrypoint(h.invokeIntegrationAgentTool))
	mux.HandleFunc("GET /business/notifications/web-push/readiness", h.authenticated(h.webPushReadiness))
	mux.HandleFunc("GET /business/notifications/web-push/subscriptions", h.authenticated(h.listWebPushSubscriptions))
	mux.HandleFunc("PUT /business/notifications/web-push/subscriptions/{subscriptionID}", h.authenticated(h.upsertWebPushSubscription))
	mux.HandleFunc("POST /business/notifications/web-push/subscriptions/{subscriptionID}/revoke", h.authenticated(h.revokeWebPushSubscription))
	mux.HandleFunc("POST /integrations/web-push/subscriptions/cleanup-expired", h.admin(h.cleanupWebPushSubscriptions))
	mux.HandleFunc("POST /integrations/webhooks/{workspaceID}/{connectionKey}", h.receiveIntegrationWebhook)
	mux.HandleFunc("GET /integrations/google/oauth/callback", h.completeGoogleOAuth)
}
