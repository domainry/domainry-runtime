package openapi

import appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

import (
	"testing"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestOpenAPISpecCoversFrontendRuntimeContract(t *testing.T) {
	spec := Build(appschemamodel.ApplicationSchemaSnapshot{
		TemplateID:      "domain-only",
		TemplateVersion: "0.1.0",
		Objects: []definitionmodel.ObjectSchema{
			{
				Key:  "customer",
				Name: "Customer",
				Fields: []definitionmodel.FieldSchema{
					{Key: "name", Name: "Name", Type: "text", Required: true},
				},
			},
		},
		Actions: []definitionmodel.ActionSchema{
			{ObjectKey: "customer", Key: "qualify", Label: "Qualify", Kind: "record_operation"},
		},
	})
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("paths missing or wrong type: %#v", spec["paths"])
	}

	required := map[string][]string{
		"/health":                          {"get"},
		"/live":                            {"get"},
		"/ready":                           {"get"},
		"/startup":                         {"get"},
		"/openapi.json":                    {"get"},
		"/discovery/schema/administration": {"get"},
		"/records/permissions/effective":   {"get"},
		"/discovery/i18n/locales":          {"get"},
		"/discovery/i18n/resources":        {"get"},
		"/discovery/references/{kind}":     {"get"},
		"/records/{objectKey}":             {"get", "post"},
		"/records/{objectKey}/fields/{fieldKey}/options":                   {"get"},
		"/records/{objectKey}/items/{recordID}":                            {"get", "patch", "delete"},
		"/records/{objectKey}/items/{recordID}/references":                 {"get"},
		"/records/{objectKey}/items/{recordID}/related/{relatedObjectKey}": {"get"},
		"/records/{objectKey}/export":                                      {"post"},
		"/records/exports/jobs/{jobID}":                                    {"get"},
		"/records/{objectKey}/import/preview":                              {"post"},
		"/records/{objectKey}/import/apply":                                {"post"},
		"/records/{objectKey}/import/jobs":                                 {"post"},
		"/records/{objectKey}/items/{recordID}/actions/{actionKey}":        {"post"},
		"/records/{objectKey}/actions/{actionKey}":                         {"post"},
		"/records/{objectKey}/actions/{actionKey}/bulk":                    {"post"},
		"/uploads":                                                              {"post"},
		"/uploads/{fileID}/scan":                                                {"get"},
		"/uploads/{filename}":                                                   {"get"},
		"/workflow/definitions/{workflowKey}/run":                               {"post"},
		"/workflow/definitions/{workflowKey}/simulate":                          {"post"},
		"/workflow/recovery/executions":                                         {"get"},
		"/workflow/recovery/executions/{executionID}/retry":                     {"post"},
		"/workflow/recovery/executions/{executionID}/resolve":                   {"post"},
		"/automation/rules":                                                     {"get"},
		"/automation/execution-catalog":                                         {"get"},
		"/authoring/snapshot":                                                   {"get"},
		"/authoring/validate":                                                   {"post"},
		"/authoring/verify-delivery":                                            {"post"},
		"/application-schema/definitions/{resourceType}/{resourceKey}/validate": {"post"},
		"/application-schema/migration-plan":                                    {"get"},
		"/application-schema/objects/{objectKey}/record-count":                  {"get"},
		"/application-schema/diagnostics":                                       {"get"},
		"/dispatch/executions":                                                  {"post"},
		"/automation/executions":                                                {"get"},
		"/automation/validate":                                                  {"post"},
		"/automation/simulate":                                                  {"post"},
		"/automation/rules/{ruleKey}":                                           {"get"},
		"/automation/rules/{ruleKey}/simulate":                                  {"post"},
		"/records/{objectKey}/items/{recordID}/profile/reactivate":              {"post"},
	}

	for _, removed := range []string{
		"/schema",
		"/audit-events",
		"/audit-events/options",
		"/audit/events",
		"/audit/exports",
		"/audit/exports/downloads/{token}",
		"/management/audit-events",
		"/management/audit-events/export",
		"/operations/audit-events",
		"/operations/audit-events/export",
		"/metadata/dictionaries/{dictionaryKey}/items",
		"/metadata/definitions/{resourceType}",
		"/metadata/definitions/{resourceType}/{resourceKey}",
		"/metadata/localized-texts",
		"/metadata/localized-texts/coverage",
		"/metadata/localized-texts/export",
		"/metadata/localized-texts/export.xlsx",
		"/scheduler/jobs/{definitionID}/run",
		"/scheduler/definitions",
		"/scheduler/state",
		"/scheduler/schedules/preview",
		"/integration/connections",
		"/integration/events/{eventID}/replay",
		"/integration/connectors",
		"/integration/web-push/readiness",
		"/integration/web-push/subscriptions",
		"/integration/google/oauth/callback",
		"/workflow-executions",
		"/workflow-processes",
		"/workflow-tasks",
		"/workflows/{workflowKey}/run",
		"/report/{reportKey}/summary",
		"/report/{reportKey}/query",
		"/report/{reportKey}/snapshots/refresh",
		"/report/{reportKey}/exports/{objectKey}/prepare",
		"/report-exports/{jobID}",
		"/report-exports/{jobID}/cancel",
		"/report-exports/downloads/{token}",
		"/record-batch-jobs/{jobID}",
		"/record-batch-jobs/{jobID}/cancel",
		"/record-batch-jobs/{jobID}/download",
	} {
		if _, ok := paths[removed]; ok {
			t.Errorf("removed management OpenAPI path %s was reintroduced", removed)
		}
	}
	for path, methods := range required {
		pathSpec, ok := paths[path].(map[string]any)
		if !ok {
			t.Fatalf("OpenAPI path %s missing or wrong type", path)
		}
		for _, method := range methods {
			if _, ok := pathSpec[method]; !ok {
				t.Fatalf("OpenAPI path %s missing method %s", path, method)
			}
		}
	}

	for _, concretePath := range []string{
		"/records/customer",
		"/records/customer/items/{recordID}",
		"/records/customer/items/{recordID}/actions/qualify",
	} {
		if _, ok := paths[concretePath]; !ok {
			t.Fatalf("OpenAPI concrete schema-derived path %s missing", concretePath)
		}
	}
}

func TestOpenAPIPublishesRuntimeOwnedAuthoringEvidenceContract(t *testing.T) {
	spec := Build(appschemamodel.ApplicationSchemaSnapshot{})
	extension, ok := spec["x-domainry-runtime-authoring-evidence"].(map[string]any)
	if !ok || extension["trust_policy"] != changeplanmodel.RuntimeAuthoringEvidenceTrustPolicy {
		t.Fatalf("evidence extension=%#v", spec["x-domainry-runtime-authoring-evidence"])
	}
	requestHeaders := extension["request_headers"].(map[string]any)
	responseHeaders := extension["response_headers"].(map[string]any)
	if requestHeaders["evidence_step_token"] != changeplanmodel.RuntimeAuthoringEvidenceStepTokenHeader || len(requestHeaders) != 2 || responseHeaders["step_receipt"] != changeplanmodel.RuntimeAuthoringStepReceiptHeader {
		t.Fatalf("request headers=%#v response headers=%#v", requestHeaders, responseHeaders)
	}
	paths := spec["paths"].(map[string]any)
	delivery := paths["/authoring/verify-delivery"].(map[string]any)["post"].(map[string]any)
	body := delivery["requestBody"].(map[string]any)
	content := body["content"].(map[string]any)
	schema := content["application/json"].(map[string]any)["schema"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	if properties["coverage"] == nil || properties["receipts"] == nil {
		t.Fatalf("delivery evidence schema=%#v", schema)
	}
	for _, compilerOwned := range []string{"version", "binding", "scenarios"} {
		if properties[compilerOwned] != nil {
			t.Fatalf("delivery schema exposes Runtime-owned %s: %#v", compilerOwned, schema)
		}
	}
}
