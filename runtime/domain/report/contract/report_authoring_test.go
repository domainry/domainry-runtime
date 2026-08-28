package contract

import (
	"testing"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

func TestReportAuthoringDomainPublishesCompleteGenericContract(t *testing.T) {
	domain := ReportAuthoringDomain()
	if domain.Key != "report" || len(domain.Capabilities) != 1 {
		t.Fatalf("domain=%#v", domain)
	}
	capability := domain.Capabilities[0]
	if capability.Key != "report.definition" || capability.InputSchema == nil || capability.OutputSchema == nil || len(capability.Errors) == 0 || len(capability.Examples) != 3 || len(capability.ConfigurationRoutes) == 0 || capability.ResourceOperations != nil {
		t.Fatalf("capability=%#v", capability)
	}
	dataset := reportDefinitionPayloadSchema().Properties["dataset"]
	join := dataset.Properties["joins"].Items
	predicate := dataset.Properties["query_predicates"].Items
	if dataset.Type != "object" || join == nil || join.Properties["field_equalities"].MaxItems == nil || *join.Properties["field_equalities"].MaxItems != 8 || predicate == nil || predicate.Properties["filters"].MaxItems == nil || *predicate.Properties["filters"].MaxItems != 16 || reportIntPointer(2) == nil || reportFloatPointer(2) == nil {
		t.Fatal("report authoring schema helpers are incomplete")
	}
	payload := reportDefinitionPayloadSchema()
	objectSQL := payload.Properties["object_sql_v1"]
	if len(payload.OneOf) != 2 || len(payload.Required) != 1 || payload.Required[0] != "key" || objectSQL.Type != "object" || objectSQL.Properties["sql"].MinLength == nil || objectSQL.Properties["source_objects"].Items == nil || objectSQL.Properties["result_schema"].Items == nil {
		t.Fatalf("report execution union is incomplete: %#v", payload)
	}
	if !requiresAuthoringField(payload.OneOf[0], "dataset") || !requiresAuthoringField(payload.OneOf[1], "object_sql_v1") {
		t.Fatalf("report execution oneOf=%#v", payload.OneOf)
	}
	for _, parameter := range capability.Parameters {
		if (parameter.Key == "dataset" || parameter.Key == "object_sql_v1") && parameter.Required {
			t.Fatalf("execution branch %s is incorrectly unconditionally required", parameter.Key)
		}
	}
}

func requiresAuthoringField(schema capabilitycontract.CapabilityAuthoringSchema, field string) bool {
	for _, required := range schema.Required {
		if required == field {
			return true
		}
	}
	return false
}
