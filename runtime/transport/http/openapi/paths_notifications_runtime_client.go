package openapi

func annotateNotificationRuntimeClient(paths map[string]any) {
	operations := map[string]map[string]string{
		"/notification/inbox":                                              {"get": "listNotifications"},
		"/notification/inbox/facets":                                       {"get": "notificationFacets"},
		"/notification/inbox/unread-count":                                 {"get": "notificationUnreadCount"},
		"/notification/inbox/stream":                                       {"get": "subscribeNotificationSync"},
		"/notification/inbox/read-all":                                     {"post": "markAllNotificationsRead"},
		"/notification/inbox/saved-views":                                  {"get": "listNotificationSavedViews"},
		"/notification/inbox/saved-views/{viewKey}":                        {"put": "saveNotificationSavedView", "delete": "deleteNotificationSavedView"},
		"/notification/inbox/delegations":                                  {"get": "listNotificationDelegations"},
		"/notification/inbox/delegations/{delegationID}":                   {"put": "saveNotificationDelegation", "delete": "deleteNotificationDelegation"},
		"/notification/inbox/delegated-owners":                             {"get": "listNotificationDelegatedOwners"},
		"/notification/inbox/{notificationID}":                             {"get": "getNotification"},
		"/notification/inbox/{notificationID}/actions/{actionKey}/resolve": {"get": "resolveNotificationAction"},
		"/notification/inbox/{notificationID}/acknowledge":                 {"post": "acknowledgeNotificationAlert"},
		"/notification/inbox/{notificationID}/read":                        {"post": "setNotificationRead"},
		"/notification/inbox/{notificationID}/unread":                      {"post": "setNotificationRead"},
		"/notification/inbox/{notificationID}/archive":                     {"post": "setNotificationArchived"},
		"/notification/inbox/{notificationID}/restore":                     {"post": "setNotificationArchived"},
		"/notification/inbox/preference":                                   {"get": "notificationPreference", "put": "saveNotificationPreference"},
	}
	for path, methods := range operations {
		pathItem, _ := paths[path].(map[string]any)
		for verb, method := range methods {
			operation, _ := pathItem[verb].(map[string]any)
			if operation != nil {
				operation["x-domainry-runtime-client-method"] = method
			}
		}
	}
}
