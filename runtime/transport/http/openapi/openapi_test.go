package openapi

import appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

import (
	"testing"

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
		"/health":                        {"get"},
		"/live":                          {"get"},
		"/ready":                         {"get"},
		"/startup":                       {"get"},
		"/operations/monitoring/metrics": {"get"},
		"/openapi.json":                  {"get"},
		"/tenant-admin/runtime-schema":   {"get"},
		"/permissions/effective":         {"get"},
		"/i18n/locales":                  {"get"},
		"/i18n/resources":                {"get"},
		"/objects/{objectKey}/records":   {"get", "post"},
		"/objects/{objectKey}/records/{recordID}":                            {"get", "patch", "delete"},
		"/objects/{objectKey}/records/{recordID}/references":                 {"get"},
		"/objects/{objectKey}/records/{recordID}/related/{relatedObjectKey}": {"get"},
		"/objects/{objectKey}/records/export":                                {"get"},
		"/objects/{objectKey}/records/export/jobs":                           {"post"},
		"/objects/{objectKey}/records/import/preview":                        {"post"},
		"/objects/{objectKey}/records/import/apply":                          {"post"},
		"/objects/{objectKey}/records/import/jobs":                           {"post"},
		"/record-batch-jobs/{jobID}":                                         {"get"},
		"/record-batch-jobs/{jobID}/cancel":                                  {"post"},
		"/record-batch-jobs/{jobID}/download":                                {"get"},
		"/objects/{objectKey}/records/{recordID}/actions/{actionKey}":        {"post"},
		"/objects/{objectKey}/actions/{actionKey}/run":                       {"post"},
		"/objects/{objectKey}/actions/{actionKey}/bulk":                      {"post"},
		"/dictionaries/{dictionaryKey}/items":                                {"get"},
		"/reports/{reportKey}/summary":                                       {"get"},
		"/reports/{reportKey}/query":                                         {"post"},
		"/reports/{reportKey}/snapshots/refresh":                             {"post"},
		"/reports/{reportKey}/exports/{objectKey}/prepare":                   {"post"},
		"/report-exports/{jobID}":                                            {"get"},
		"/report-exports/{jobID}/cancel":                                     {"post"},
		"/report-exports/downloads/{token}":                                  {"get"},
		"/business/audit-event-exports":                                      {"post"},
		"/business/audit-event-exports/downloads/{token}":                    {"get"},
		"/files":                                                                   {"post"},
		"/files/{fileID}/scan":                                                     {"get"},
		"/uploads/{filename}":                                                      {"get"},
		"/business/workflows/{workflowKey}/run":                                    {"post"},
		"/portal/workflows/{workflowKey}/run":                                      {"post"},
		"/tenant-admin/workflows/{workflowKey}/simulate":                           {"post"},
		"/operations/workflow/executions":                                          {"get"},
		"/operations/workflow/executions/{executionID}/retry":                      {"post"},
		"/operations/workflow/executions/{executionID}/resolve":                    {"post"},
		"/operations/capability-status":                                            {"get"},
		"/automation-rules":                                                        {"get"},
		"/automation-rules/capabilities":                                           {"get"},
		"/tenant-admin/execution-capabilities":                                     {"get"},
		"/tenant-admin/platform-capabilities":                                      {"get"},
		"/frontend-capability-manifest":                                            {"get"},
		"/frontend-capability-manifest/validate":                                   {"post"},
		"/domain-system-snapshot":                                                  {"get"},
		"/domain-system-validation":                                                {"post"},
		"/domain-system-delivery-verification":                                     {"post"},
		"/tenant-admin/metadata/definitions/{resourceType}/{resourceKey}/validate": {"post"},
		"/automation-rules/executions":                                             {"get"},
		"/automation-rules/validate":                                               {"post"},
		"/automation-rules/simulate":                                               {"post"},
		"/automation-rules/{ruleKey}":                                              {"get"},
		"/automation-rules/{ruleKey}/simulate":                                     {"post"},
		"/business/notifications/web-push/readiness":                               {"get"},
		"/business/notifications/web-push/subscriptions":                           {"get"},
		"/objects/{objectKey}/records/{recordID}/reactivate-profile":               {"post"},
		"/party":                     {"get"},
		"/party/{partyID}":           {"get", "put"},
		"/tenant-admin/audit-events": {"get"},
		"/operations/audit-events":   {"get"},
	}

	for _, removed := range []string{
		"/schema",
		"/audit-events",
		"/audit-events/options",
		"/metadata/definitions/{resourceType}/{resourceKey}/validate",
		"/scheduler/jobs/{definitionID}/run",
		"/integrations/connections",
		"/integrations/events/{eventID}/replay",
		"/tenant-admin/integrations/connectors",
		"/integrations/google/oauth/callback",
		"/workflow-executions",
		"/workflow-processes",
		"/workflow-tasks",
		"/workflows/{workflowKey}/run",
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
		"/objects/customer/records",
		"/objects/customer/records/{recordID}",
		"/objects/customer/records/{recordID}/actions/qualify",
	} {
		if _, ok := paths[concretePath]; !ok {
			t.Fatalf("OpenAPI concrete schema-derived path %s missing", concretePath)
		}
	}
}
