package openapi

import (
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"
)

// addIntegrationOpenAPIPaths publishes only Runtime-owned durable handoff
// reads and the backwards-compatible Web Push proxy. Integration management,
// provider webhook/OAuth ingress, credentials, invocations and owner workers
// are advertised by the selected Integration Module/SaaS deployment.
func addIntegrationOpenAPIPaths(paths map[string]any, _ appschemamodel.ApplicationSchemaSnapshot) {
	paths["/business/integration-intents/{messageID}"] = map[string]any{
		"get": openAPIOperation("getBusinessIntegrationIntent", "Integration Business", "Read the current user's redacted Runtime publication handoff result", openAPIAdminSecurity(), openAPIPathParameter("messageID", "Runtime publication message ID"), openAPIJSONResponse("Integration intent", openAPIObject(nil))),
	}
	remoteNotificationSurfaces := openAPIProductSurfaces(surfacemodel.ProductSurfaceBusinessWorkspace)
	paths["/business/notifications/web-push/readiness"] = map[string]any{"get": openAPIOperation("getWebPushReadiness", "Remote notifications", "Read Integration-owned Web Push readiness and the public VAPID key", remoteNotificationSurfaces, openAPIAdminSecurity(), openAPIJSONResponse("Readiness", openAPIRef("WebPushReadiness")))}
	paths["/business/notifications/web-push/subscriptions"] = map[string]any{"get": openAPIOperation("listWebPushSubscriptions", "Remote notifications", "List the current user's Integration-owned subscriptions without secret material", remoteNotificationSurfaces, openAPIAdminSecurity(), openAPIJSONResponse("Subscriptions", openAPIRef("WebPushSubscriptionList")))}
	paths["/business/notifications/web-push/subscriptions/{subscriptionID}"] = map[string]any{"put": openAPIOperation("upsertWebPushSubscription", "Remote notifications", "Create or replace the current user's explicit Web Push opt-in", remoteNotificationSurfaces, openAPIAdminSecurity(), openAPIPathParameter("subscriptionID", "Stable client subscription identity"), openAPIJSONRequest(openAPIRef("WebPushSubscriptionUpsertRequest")), openAPIJSONResponse("Subscription", openAPIRef("WebPushSubscription")))}
	paths["/business/notifications/web-push/subscriptions/{subscriptionID}/revoke"] = map[string]any{"post": openAPIOperation("revokeWebPushSubscription", "Remote notifications", "Revoke the current user's Web Push subscription and erase endpoint key material", remoteNotificationSurfaces, openAPIAdminSecurity(), openAPIPathParameter("subscriptionID", "Subscription identity"), openAPIJSONResponse("Subscription", openAPIRef("WebPushSubscription")))}
	paths["/integrations/web-push/subscriptions/cleanup-expired"] = map[string]any{"post": openAPIOperation("cleanupExpiredWebPushSubscriptions", "Remote notifications", "Erase expired endpoint key material in the current workspace", openAPIProductSurfaces(surfacemodel.ProductSurfaceAdminConsole), openAPIAdminSecurity(), openAPIJSONResponse("Cleanup result", openAPIObject(nil)))}
}
