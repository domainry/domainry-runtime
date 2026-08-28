package openapi

func annotateNotificationRuntimeClient(paths map[string]any) {
	operations := map[string]map[string]string{
		"/notifications":                                              {"get": "listNotifications"},
		"/notifications/facets":                                       {"get": "notificationFacets"},
		"/notifications/unread-count":                                 {"get": "notificationUnreadCount"},
		"/notifications/stream":                                       {"get": "subscribeNotificationSync"},
		"/notifications/read-all":                                     {"post": "markAllNotificationsRead"},
		"/notifications/saved-views":                                  {"get": "listNotificationSavedViews"},
		"/notifications/saved-views/{viewKey}":                        {"put": "saveNotificationSavedView", "delete": "deleteNotificationSavedView"},
		"/notifications/delegations":                                  {"get": "listNotificationDelegations"},
		"/notifications/delegations/{delegationID}":                   {"put": "saveNotificationDelegation", "delete": "deleteNotificationDelegation"},
		"/notifications/delegated-owners":                             {"get": "listNotificationDelegatedOwners"},
		"/notifications/{notificationID}":                             {"get": "getNotification"},
		"/notifications/{notificationID}/actions/{actionKey}/resolve": {"get": "resolveNotificationAction"},
		"/notifications/{notificationID}/acknowledge":                 {"post": "acknowledgeNotificationAlert"},
		"/notifications/{notificationID}/read":                        {"post": "setNotificationRead"},
		"/notifications/{notificationID}/unread":                      {"post": "setNotificationRead"},
		"/notifications/{notificationID}/archive":                     {"post": "setNotificationArchived"},
		"/notifications/{notificationID}/restore":                     {"post": "setNotificationArchived"},
		"/notification-preferences":                                   {"get": "notificationPreference", "put": "saveNotificationPreference"},
	}
	for _, prefix := range []string{"/business", "/portal"} {
		for suffix, methods := range operations {
			pathItem, _ := paths[prefix+suffix].(map[string]any)
			for verb, method := range methods {
				operation, _ := pathItem[verb].(map[string]any)
				if operation != nil {
					operation["x-domainry-runtime-client-method"] = method
				}
			}
		}
	}
}
