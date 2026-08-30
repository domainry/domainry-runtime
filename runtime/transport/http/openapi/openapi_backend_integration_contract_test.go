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
		{path: "/agent-dialog/runs/stream", method: "post", client: "runAgentStream"},
		{path: "/agent-dialog/analysis/query", method: "post", client: "queryAgentAnalysis"},
		{path: "/agent-dialog/report-query-runs/{queryRef}", method: "get", client: "getAgentReportQueryRun"},
		{path: "/agent-dialog/report-export-audits/{queryRef}", method: "get", client: "getAgentReportExportAudit"},
		{path: "/agent-dialog/download-tasks/{queryRef}", method: "get", client: "getAgentReportDownloadTask"},
		{path: "/agent-dialog/download-tasks/{queryRef}/prepare", method: "post", client: "prepareAgentReportHandoff"},
		{path: "/objects/{objectKey}/records/import/preview", method: "post", client: "previewRecordImport"},
		{path: "/objects/{objectKey}/records/import/apply", method: "post", client: "applyRecordImport"},
		{path: "/objects/{objectKey}/records/import/jobs", method: "post", client: "enqueueRecordImport"},
		{path: "/objects/{objectKey}/records/export", method: "get", client: "exportRecords"},
		{path: "/objects/{objectKey}/records/export/jobs", method: "post", client: "enqueueRecordExport"},
		{path: "/record-batch-jobs/{jobID}", method: "get", client: "getRecordBatchJob"},
		{path: "/record-batch-jobs/{jobID}/cancel", method: "post", client: "cancelRecordBatchJob"},
		{path: "/record-batch-jobs/{jobID}/download", method: "get", client: "downloadRecordBatchJob"},
		{path: "/objects/{objectKey}/actions/{actionKey}/bulk", method: "post", client: "runBulkAction"},
		{path: "/files", method: "post", client: "uploadFile"},
		{path: "/uploads/{filename}", method: "get", client: "downloadUpload"},
	} {
		operation := openAPITestOperation(t, paths, item.path, item.method)
		if operation["x-domainry-runtime-client-method"] != item.client {
			t.Errorf("%s %s runtime client method=%v want=%s", item.method, item.path, operation["x-domainry-runtime-client-method"], item.client)
		}
	}

	stream := openAPITestOperation(t, paths, "/agent-dialog/runs/stream", "post")
	if !openAPITestResponseContentType(stream, "text/event-stream") || !openAPITestRequiredHeader(stream, "Idempotency-Key") {
		t.Fatalf("Agent stream is not resumable typed SSE with required idempotency: %#v", stream)
	}

	for _, path := range []string{
		"/objects/{objectKey}/records/import/apply",
		"/objects/{objectKey}/records/import/jobs",
		"/objects/{objectKey}/records/export/jobs",
		"/record-batch-jobs/{jobID}/cancel",
		"/objects/{objectKey}/actions/{actionKey}/bulk",
	} {
		if operation := openAPITestOperation(t, paths, path, "post"); !openAPITestRequiredHeader(operation, "Idempotency-Key") {
			t.Errorf("POST %s is missing required Idempotency-Key", path)
		}
	}
	for _, path := range []string{"/objects/{objectKey}/records/import/preview", "/objects/{objectKey}/records/import/apply", "/objects/{objectKey}/records/import/jobs"} {
		content := openAPITestOperation(t, paths, path, "post")["requestBody"].(map[string]any)["content"].(map[string]any)
		if content["application/json"] == nil || content["text/csv"] == nil {
			t.Errorf("POST %s request content=%v", path, content)
		}
	}

	upload := openAPITestOperation(t, paths, "/files", "post")
	uploadContent := upload["requestBody"].(map[string]any)["content"].(map[string]any)
	if uploadContent["multipart/form-data"] == nil || !openAPITestRequiredParameter(upload, "query", "object_key") || !openAPITestRequiredParameter(upload, "query", "field_key") {
		t.Fatalf("multipart upload authorization contract=%#v", upload)
	}
	download := openAPITestOperation(t, paths, "/uploads/{filename}", "get")
	if !openAPITestRequiredParameter(download, "query", "object_key") || !openAPITestRequiredParameter(download, "query", "field_key") || !openAPITestResponseContentType(download, "application/octet-stream") {
		t.Fatalf("authorized file download contract=%#v", download)
	}

	reportExport := openAPITestOperation(t, paths, "/reports/{reportKey}/exports/{objectKey}/prepare", "post")
	responses := reportExport["responses"].(map[string]any)
	if responses["200"] != nil || responses["202"] == nil {
		t.Fatalf("Report export must always submit a Data Exchange job: responses=%#v", responses)
	}
	requestSchema := reportExport["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	required, _ := requestSchema["required"].([]string)
	if !containsString(required, "audit_id") || !containsString(required, "scope") {
		t.Fatalf("Report export request must require governed scope: required=%v", required)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestPersonalInboxAndWorkforcePublishDurableIntegrationContracts(t *testing.T) {
	paths := Build(appschemamodel.ApplicationSchemaSnapshot{})["paths"].(map[string]any)
	inbox := map[string]map[string]string{
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
		for suffix, methods := range inbox {
			for method, client := range methods {
				operation := openAPITestOperation(t, paths, prefix+suffix, method)
				if operation["x-domainry-runtime-client-method"] != client {
					t.Errorf("%s %s runtime client method=%v want=%s", method, prefix+suffix, operation["x-domainry-runtime-client-method"], client)
				}
			}
		}
		stream := openAPITestOperation(t, paths, prefix+"/notifications/stream", "get")
		if !openAPITestResponseContentType(stream, "text/event-stream") || !openAPITestParameter(stream, "header", "Last-Event-ID") {
			t.Errorf("%s Inbox stream does not publish resumable durable-refetch signal contract: %#v", prefix, stream)
		}
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
