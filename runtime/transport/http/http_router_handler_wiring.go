package http

func UseHandlers(router *HTTPRouter, handlers HTTPRouterHandlers) *HTTPRouter {
	router.recordHTTP = handlers.Records
	router.uploadHTTP = handlers.Uploads
	router.discoveryHTTP = handlers.Discovery
	router.openAPIHTTP = handlers.OpenAPI
	router.workflowHTTP = handlers.Workflows
	router.automationHTTP = handlers.Automation
	router.dispatchHTTP = handlers.Dispatch
	router.businessReferenceHTTP = handlers.BusinessReferences
	router.publicationHandoffHTTP = nil
	if handlers.PublicationHandoff != nil {
		router.publicationHandoffHTTP = handlers.PublicationHandoff
	}
	router.businessSystemHTTP = handlers.BusinessSystem
	router.applicationSchemaHTTP = handlers.ApplicationSchema
	router.notificationHTTP = nil
	if handlers.Notifications != nil {
		router.notificationHTTP = handlers.Notifications
	}
	router.operationsHTTP = handlers.Operations
	router.lifecycleHTTP = nil
	if handlers.Lifecycle != nil {
		router.lifecycleHTTP = handlers.Lifecycle
	}
	if handlers.BusinessEvents != nil {
		router.businessEventHTTP = handlers.BusinessEvents
	}
	if handlers.WorkspaceProvision != nil {
		router.workspaceProvisionHTTP = handlers.WorkspaceProvision
	}
	return router
}

func (s *HTTPRouter) HandlerCallbacks() HandlerCallbacks {
	return HandlerCallbacks{
		Principal:                 s.principalFromRequest,
		WriteJSON:                 writeJSON,
		WriteError:                writeError,
		WriteServiceError:         writeServiceError,
		DecodeJSON:                s.decodeJSONBody,
		SecurityAudit:             s.appendSecurityAudit,
		SecurityAuditForPrincipal: s.appendSecurityAuditForPrincipal,
		ProvisionRequired:         s.manifestProvisionEntrypointRequired,
		Locale:                    requestLocale,
	}
}
