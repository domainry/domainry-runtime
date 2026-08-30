package appschema

import (
	"encoding/json"
	"strings"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestApplicationDefinitionShapeSupportsEveryDeclaredResourceType(t *testing.T) {
	tests := []struct {
		resourceType string
		payload      string
		request      appschemamodel.ApplicationDefinitionUpsertRequest
		wantKey      string
		wantObject   string
		wantName     string
	}{
		{resourceType: "object", payload: `{"key":"customer","name":"Customer","fields":[{"key":"ignored"}],"validations":[{"key":"ignored"}]}`, wantKey: "customer", wantObject: "customer", wantName: "Customer"},
		{resourceType: "field", payload: `{"key":"name","name":"Name","type":"text"}`, request: appschemamodel.ApplicationDefinitionUpsertRequest{ObjectKey: "customer"}, wantKey: "customer.name", wantObject: "customer", wantName: "Name"},
		{resourceType: "validation", payload: `{"key":"customer.required","object_key":"customer","message":"Required"}`, wantKey: "customer.required", wantObject: "customer", wantName: "Required"},
		{resourceType: "action", payload: `{"key":"customer.create","object_key":"customer","label":"Create","kind":"create"}`, wantKey: "customer.create", wantObject: "customer", wantName: "Create"},
		{resourceType: "workflow", payload: `{"key":"customer.review","name":"Review","trigger":{"object_key":"customer"}}`, wantKey: "customer.review", wantObject: "customer", wantName: "Review"},
		{resourceType: "automation_rule", payload: `{"key":"customer.auto","name":"Automation","object_key":"customer"}`, wantKey: "customer.auto", wantObject: "customer", wantName: "Automation"},
		{resourceType: "dictionary", payload: `{"key":"status","name":"Status"}`, wantKey: "status", wantName: "Status"},
		{resourceType: "connector", payload: `{"key":"crm","name":"CRM","type":"api","provider":"example"}`, wantKey: "crm", wantName: "CRM"},
		{resourceType: "integration_event_mapping", payload: `{"key":"crm.created","provider":"example","target_type":"record","object_key":"customer"}`, wantKey: "crm.created", wantObject: "customer", wantName: "example"},
	}
	for _, test := range tests {
		t.Run(test.resourceType, func(t *testing.T) {
			test.request.Payload = json.RawMessage(test.payload)
			shape, err := metadataDefinitionShape(t.Context(), test.resourceType, "", test.request)
			if err != nil {
				t.Fatal(err)
			}
			if shape.Key != test.wantKey || shape.ObjectKey != test.wantObject || shape.Name != test.wantName {
				t.Fatalf("shape=%+v", shape)
			}
			if test.resourceType == "object" {
				object := shape.Payload.(definitionmodel.ObjectSchema)
				if object.Fields != nil || object.Validations != nil {
					t.Fatalf("object child definitions leaked into object payload: %+v", object)
				}
			}
			if test.resourceType == "field" {
				field := shape.Payload.(definitionmodel.FieldSchema)
				if field.Config["_definition_object_key"] != "customer" {
					t.Fatalf("field config=%v", field.Config)
				}
			}
		})
	}
}

func TestApplicationDefinitionShapeRejectsMalformedPayloadForEveryResourceType(t *testing.T) {
	for _, resourceType := range []string{
		"object", "field", "validation", "action", "workflow", "automation_rule", "dictionary", "connector",
		"integration_event_mapping",
	} {
		t.Run(resourceType, func(t *testing.T) {
			if _, err := metadataDefinitionShape(t.Context(), resourceType, "key", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err == nil {
				t.Fatal("malformed payload was accepted")
			}
		})
	}
}

func TestApplicationDefinitionShapeValidationErrorsAreStable(t *testing.T) {
	tests := []struct {
		name, resourceType, resourceKey, payload, want string
	}{
		{name: "field key", resourceType: "field", payload: `{"type":"text","name":"Name"}`, want: "metadata.field.missingKey"},
		{name: "field type", resourceType: "field", payload: `{"key":"name","name":"Name"}`, want: "metadata.field.missingType"},
		{name: "field name", resourceType: "field", payload: `{"key":"name","type":"text"}`, want: "metadata.field.missingName"},
		{name: "action object", resourceType: "action", payload: `{"key":"create","kind":"create"}`, want: "metadata.action.missingObjectKey"},
		{name: "action kind", resourceType: "action", payload: `{"key":"create","object_key":"customer"}`, want: "metadata.action.missingKind"},
		{name: "connector type", resourceType: "connector", payload: `{"key":"crm","provider":"example"}`, want: "metadata.connector.missingType"},
		{name: "connector provider", resourceType: "connector", payload: `{"key":"crm","type":"api"}`, want: "metadata.connector.missingProvider"},
		{name: "unsupported", resourceType: "unknown", payload: `{}`, want: "unsupported metadata resource type"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := metadataDefinitionShape(t.Context(), test.resourceType, test.resourceKey, appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(test.payload)})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want=%q", err, test.want)
			}
		})
	}
}

func TestApplicationDefinitionFirstStringHelper(t *testing.T) {
	if got := firstString([]string{" ", " value ", "later"}); got != "value" || firstString(nil) != "" {
		t.Fatalf("firstString=%q empty=%q", got, firstString(nil))
	}
}

func TestApplicationDefinitionTableMatchesBusinessResourceInventory(t *testing.T) {
	for resourceType, expectedTable := range map[string]string{
		"workflow":                  "workflow_definitions",
		"automation_rule":           "automation_rule_definitions",
		"connector":                 "application_connector_requirements",
		"integration_event_mapping": "application_integration_event_mapping_requirements",
	} {
		if table, err := metadataDefinitionTable(" " + resourceType + " "); err != nil || table != expectedTable {
			t.Fatalf("resource=%q table=%q err=%v", resourceType, table, err)
		}
	}
	for _, resourceType := range []string{"object", "field", "validation", "action", "dictionary"} {
		if !metadataModuleOwnsDefinition(resourceType) {
			t.Fatalf("Metadata module ownership missing for %q", resourceType)
		}
		if _, err := metadataDefinitionTable(resourceType); err == nil {
			t.Fatalf("Runtime still exposes a physical table for Metadata-owned resource %q", resourceType)
		}
	}
	if _, err := metadataDefinitionTable("unknown"); err == nil {
		t.Fatal("unsupported resource table was accepted")
	}
	for _, resourceType := range []string{"preference", "rule_set", "surface", "component", "view", "entrypoint", "scheduler", "report", "operation_state_example", "sensitive_field_policy", "report_export_control", "identity_profile_binding"} {
		if _, err := metadataDefinitionTable(resourceType); err == nil {
			t.Fatalf("retired presentation resource %q was accepted", resourceType)
		}
	}
}

func TestMetadataFieldShapeRequestNameAndExistingConfig(t *testing.T) {
	shape, err := metadataDefinitionShape(t.Context(), "field", "account.name", appschemamodel.ApplicationDefinitionUpsertRequest{
		Name:      "Display Name",
		ObjectKey: "account",
		Payload:   json.RawMessage(`{"key":"name","type":"text","config":{}}`),
	})
	if err != nil || shape.Name != "Display Name" {
		t.Fatalf("shape=%#v err=%v", shape, err)
	}
}
