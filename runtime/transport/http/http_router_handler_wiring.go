package http

func UseHandlers(router *HTTPRouter, handlers HTTPRouterHandlers) *HTTPRouter {
	router.recordHTTP = handlers.Records
	router.surfaceContextHTTP = handlers.SurfaceContext
	router.uploadHTTP = handlers.Uploads
	router.discoveryHTTP = handlers.Discovery
	router.openAPIHTTP = handlers.OpenAPI
	router.workflowHTTP = handlers.Workflows
	router.automationHTTP = handlers.Automation
	router.schedulerHTTP = handlers.Scheduler
	router.reportHTTP = handlers.Reports
	router.businessReferenceHTTP = handlers.BusinessReferences
	router.integrationHTTP = nil
	if handlers.Integrations != nil {
		router.integrationHTTP = handlers.Integrations
	}
	router.businessSystemHTTP = handlers.BusinessSystem
	router.capabilityHTTP = handlers.Capabilities
	router.applicationSchemaHTTP = handlers.ApplicationSchema
	router.notificationHTTP = nil
	if handlers.Notifications != nil {
		router.notificationHTTP = handlers.Notifications
	}
	router.partyHTTP = nil
	if handlers.Party != nil {
		router.partyHTTP = handlers.Party
	}
	router.agentDialogHTTP = nil
	if handlers.AgentDialog != nil {
		router.agentDialogHTTP = handlers.AgentDialog
	}
	router.operationsHTTP = handlers.Operations
	router.lifecycleHTTP = nil
	if handlers.Lifecycle != nil {
		router.lifecycleHTTP = handlers.Lifecycle
	}
	if handlers.BusinessEvents != nil {
		router.businessEventHTTP = handlers.BusinessEvents
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
