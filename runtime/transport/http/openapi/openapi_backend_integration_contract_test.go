package openapi

import (
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func TestBackendIntegrationRoutesPublishTypedRuntimeClientContracts(t *testing.T) {
	paths := Build(appschemamodel.ApplicationSchemaSnapshot{})["paths"].(map[string]any)
	for _, item := range []struct {
		path   string
		method string
		client string
	}{
		{path: "/records/{objectKey}/import/preview", method: "post", client: "previewRecordImport"},
		{path: "/records/{objectKey}/import/apply", method: "post", client: "applyRecordImport"},
		{path: "/records/{objectKey}/import/jobs", method: "post", client: "enqueueRecordImport"},
		{path: "/records/{objectKey}/export", method: "post", client: "exportRecords"},
		{path: "/records/exports/jobs/{jobID}", method: "get", client: "downloadRecordExport"},
		{path: "/records/{objectKey}/actions/{actionKey}/bulk", method: "post", client: "runBulkAction"},
		{path: "/uploads", method: "post", client: "uploadFile"},
		{path: "/uploads/{filename}", method: "get", client: "downloadUpload"},
	} {
		operation := openAPITestOperation(t, paths, item.path, item.method)
		if operation["x-domainry-runtime-client-method"] != item.client {
			t.Errorf("%s %s runtime client method=%v want=%s", item.method, item.path, operation["x-domainry-runtime-client-method"], item.client)
		}
	}

	for _, path := range []string{
		"/records/{objectKey}/import/apply",
		"/records/{objectKey}/import/jobs",
		"/records/{objectKey}/export",
		"/records/{objectKey}/actions/{actionKey}/bulk",
	} {
		if operation := openAPITestOperation(t, paths, path, "post"); !openAPITestRequiredHeader(operation, "Idempotency-Key") {
			t.Errorf("POST %s is missing required Idempotency-Key", path)
		}
	}
	if _, exists := paths["/records/{objectKey}/export/jobs"]; exists {
		t.Fatal("legacy record export job path is still published")
	}
	if _, exists := paths["/records/{objectKey}/export"].(map[string]any)["get"]; exists {
		t.Fatal("legacy GET record export method is still published")
	}
	for _, path := range []string{"/records/{objectKey}/import/preview", "/records/{objectKey}/import/apply", "/records/{objectKey}/import/jobs"} {
		content := openAPITestOperation(t, paths, path, "post")["requestBody"].(map[string]any)["content"].(map[string]any)
		if content["application/json"] == nil || content["text/csv"] == nil {
			t.Errorf("POST %s request content=%v", path, content)
		}
	}

	upload := openAPITestOperation(t, paths, "/uploads", "post")
	uploadContent := upload["requestBody"].(map[string]any)["content"].(map[string]any)
	if uploadContent["multipart/form-data"] == nil || !openAPITestRequiredParameter(upload, "query", "object_key") || !openAPITestRequiredParameter(upload, "query", "field_key") {
		t.Fatalf("multipart upload authorization contract=%#v", upload)
	}
	download := openAPITestOperation(t, paths, "/uploads/{filename}", "get")
	if !openAPITestRequiredParameter(download, "query", "object_key") || !openAPITestRequiredParameter(download, "query", "field_key") || !openAPITestResponseContentType(download, "application/octet-stream") {
		t.Fatalf("authorized file download contract=%#v", download)
	}

}

func TestPersonalInboxPublishesDurableIntegrationContracts(t *testing.T) {
	paths := Build(appschemamodel.ApplicationSchemaSnapshot{})["paths"].(map[string]any)
	inbox := map[string]map[string]string{
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
	for path, methods := range inbox {
		for method, client := range methods {
			operation := openAPITestOperation(t, paths, path, method)
			if operation["x-domainry-runtime-client-method"] != client {
				t.Errorf("%s %s runtime client method=%v want=%s", method, path, operation["x-domainry-runtime-client-method"], client)
			}
		}
	}
	stream := openAPITestOperation(t, paths, "/notification/inbox/stream", "get")
	if !openAPITestResponseContentType(stream, "text/event-stream") || !openAPITestParameter(stream, "header", "Last-Event-ID") {
		t.Errorf("Inbox stream does not publish resumable durable-refetch signal contract: %#v", stream)
	}

}

func openAPITestOperation(t *testing.T, paths map[string]any, path, method string) map[string]any {
	t.Helper()
	pathItem, ok := paths[path].(map[string]any)
	if !ok {
		t.Fatalf("missing OpenAPI path %s", path)
	}
	operation, ok := pathItem[method].(map[string]any)
	if !ok {
		t.Fatalf("missing OpenAPI operation %s %s", method, path)
	}
	return operation
}

func openAPITestParameter(operation map[string]any, in, name string) bool {
	parameters, _ := operation["parameters"].([]map[string]any)
	for _, parameter := range parameters {
		if parameter["in"] == in && parameter["name"] == name {
			return true
		}
	}
	return false
}

func openAPITestRequiredParameter(operation map[string]any, in, name string) bool {
	parameters, _ := operation["parameters"].([]map[string]any)
	for _, parameter := range parameters {
		if parameter["in"] == in && parameter["name"] == name && parameter["required"] == true {
			return true
		}
	}
	return false
}

func openAPITestRequiredHeader(operation map[string]any, name string) bool {
	return openAPITestRequiredParameter(operation, "header", name)
}

func openAPITestResponseContentType(operation map[string]any, contentType string) bool {
	responses, _ := operation["responses"].(map[string]any)
	for status, raw := range responses {
		if status == "default" {
			continue
		}
		response, _ := raw.(map[string]any)
		content, _ := response["content"].(map[string]any)
		if content[contentType] != nil {
			return true
		}
	}
	return false
}

func openAPITestStringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
