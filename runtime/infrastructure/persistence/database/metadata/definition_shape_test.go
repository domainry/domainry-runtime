package metadata

import (
	"encoding/json"
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func TestMetadataDefinitionShapeSupportsEveryDeclaredResourceType(t *testing.T) {
	tests := []struct {
		resourceType string
		payload      string
		request      metadatamodel.MetadataDefinitionUpsertRequest
		wantKey      string
		wantObject   string
		wantName     string
	}{
		{resourceType: "object", payload: `{"key":"customer","name":"Customer","fields":[{"key":"ignored"}],"validations":[{"key":"ignored"}]}`, wantKey: "customer", wantObject: "customer", wantName: "Customer"},
		{resourceType: "field", payload: `{"key":"name","name":"Name","type":"text"}`, request: metadatamodel.MetadataDefinitionUpsertRequest{ObjectKey: "customer"}, wantKey: "customer.name", wantObject: "customer", wantName: "Name"},
		{resourceType: "validation", payload: `{"key":"customer.required","object_key":"customer","message":"Required"}`, wantKey: "customer.required", wantObject: "customer", wantName: "Required"},
		{resourceType: "view", payload: `{"key":"customer.list","object_key":"customer","name":"Customers"}`, wantKey: "customer.list", wantObject: "customer", wantName: "Customers"},
		{resourceType: "action", payload: `{"key":"customer.create","object_key":"customer","label":"Create","kind":"create"}`, wantKey: "customer.create", wantObject: "customer", wantName: "Create"},
		{resourceType: "workflow", payload: `{"key":"customer.review","name":"Review","trigger":{"object_key":"customer"}}`, wantKey: "customer.review", wantObject: "customer", wantName: "Review"},
		{resourceType: "scheduler", payload: `{"key":"customer.refresh","name":"Refresh","status":"enabled","target_type":"workflow","target_key":"scheduled:customer.review","schedule_type":"interval","interval_seconds":60}`, wantKey: "customer.refresh", wantName: "Refresh"},
		{resourceType: "automation_rule", payload: `{"key":"customer.auto","name":"Automation","object_key":"customer"}`, wantKey: "customer.auto", wantObject: "customer", wantName: "Automation"},
		{resourceType: "preference", payload: `{"key":"theme","name":"Theme","value_type":"text","value":"dark","effective_from":"2026-07-01"}`, wantKey: "theme", wantName: "Theme"},
		{resourceType: "dictionary", payload: `{"key":"status","name":"Status"}`, wantKey: "status", wantName: "Status"},
		{resourceType: "connector", payload: `{"key":"crm","name":"CRM","type":"api","provider":"example"}`, wantKey: "crm", wantName: "CRM"},
		{resourceType: "integration_event_mapping", payload: `{"key":"crm.created","provider":"example","target_type":"record","object_key":"customer"}`, wantKey: "crm.created", wantObject: "customer", wantName: "example"},
		{resourceType: "report", payload: `{"key":"customer.summary","name":"Summary"}`, wantKey: "customer.summary", wantName: "Summary"},
		{resourceType: "operation_state_example", payload: `{"key":"customer.active","name":"Active","object_key":"customer"}`, wantKey: "customer.active", wantObject: "customer", wantName: "Active"},
		{resourceType: "sensitive_field_policy", payload: `{"key":"customer.pii","name":"PII","object_key":"customer"}`, wantKey: "customer.pii", wantObject: "customer", wantName: "PII"},
		{resourceType: "report_export_control", payload: `{"key":"customer.export","name":"Export","report_key":"customer.summary"}`, wantKey: "customer.export", wantObject: "customer.summary", wantName: "Export"},
		{resourceType: "identity_profile_binding", payload: `{"object_key":"customer_profile","business_identity":{"key":"customer"}}`, wantKey: "customer_profile", wantObject: "customer_profile", wantName: "customer"},
		{resourceType: "rule_set", payload: `{"key":"customer.policy","name":"Customer policy","match_policy":"first_match","input_types":{"count":"integer"},"output_types":{"allowed":"boolean"},"effective_from":"2026-07-01","rules":[{"key":"allow","priority":1,"when":{"kind":"literal","value_type":"boolean","value":true},"outputs":{"allowed":{"kind":"literal","value_type":"boolean","value":true}}}],"default_outputs":{"allowed":{"kind":"literal","value_type":"boolean","value":false}}}`, wantKey: "customer.policy", wantName: "Customer policy"},
		{resourceType: "surface", payload: `{"key":"customer_admin","label":"Customer Admin","object_key":"customer"}`, wantKey: "customer_admin", wantObject: "customer", wantName: "Customer Admin"},
		{resourceType: "component", payload: `{"key":"customer_table","name":"Customer Table","object_key":"customer"}`, wantKey: "customer_table", wantObject: "customer", wantName: "Customer Table"},
		{resourceType: "entrypoint", payload: `{"key":"customer.home","name":"Customer Home"}`, wantKey: "customer.home", wantName: "Customer Home"},
		{resourceType: "skill", payload: `{"key":"customer_lookup","name":"Customer Lookup"}`, wantKey: "customer_lookup", wantName: "Customer Lookup"},
		{resourceType: "agent", payload: `{"key":"customer_agent","name":"Customer Agent"}`, wantKey: "customer_agent", wantName: "Customer Agent"},
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

func TestMetadataDefinitionShapeRejectsMalformedPayloadForEveryResourceType(t *testing.T) {
	for _, resourceType := range []string{
		"object", "field", "validation", "view", "action", "workflow", "scheduler", "automation_rule", "preference", "dictionary", "connector",
		"integration_event_mapping", "report", "operation_state_example", "sensitive_field_policy", "report_export_control", "identity_profile_binding", "rule_set", "surface", "component", "entrypoint", "skill", "agent",
	} {
		t.Run(resourceType, func(t *testing.T) {
			if _, err := metadataDefinitionShape(t.Context(), resourceType, "key", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err == nil {
				t.Fatal("malformed payload was accepted")
			}
		})
	}
}

func TestMetadataDefinitionShapeValidationErrorsAreStable(t *testing.T) {
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
		{name: "missing key", resourceType: "report", payload: `{}`, want: "metadata.report.missingKey"},
		{name: "preference null", resourceType: "preference", resourceKey: "provided", payload: `null`, want: "backend.preference.definition_invalid"},
		{name: "preference legacy aliases", resourceType: "preference", resourceKey: "theme", payload: `{"preference_key":"theme","label":"Theme","value":"dark"}`, want: "backend.preference.definition_invalid"},
		{name: "scheduler null", resourceType: "scheduler", resourceKey: "provided", payload: `null`, want: "metadata.scheduler.invalidPayload"},
		{name: "scheduler contract", resourceType: "scheduler", resourceKey: "provided", payload: `{}`, want: "backend.scheduler"},
		{name: "surface null", resourceType: "surface", resourceKey: "provided", payload: `null`, want: "metadata.surface.invalidPayload"},
		{name: "unsupported", resourceType: "unknown", payload: `{}`, want: "unsupported metadata resource type"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := metadataDefinitionShape(t.Context(), test.resourceType, test.resourceKey, metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(test.payload)})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want=%q", err, test.want)
			}
		})
	}
}

func TestMetadataDefinitionFirstStringHelper(t *testing.T) {
	if got := firstString([]string{" ", " value ", "later"}); got != "value" || firstString(nil) != "" {
		t.Fatalf("firstString=%q empty=%q", got, firstString(nil))
	}
}

func TestMetadataDefinitionTableMatchesBusinessResourceInventory(t *testing.T) {
	for _, resourceType := range []string{
		"object", "field", "validation", "view", "action", "workflow", "scheduler", "automation_rule", "preference", "dictionary", "connector", "integration_event_mapping",
		"report", "operation_state_example", "sensitive_field_policy", "report_export_control", "identity_profile_binding", "rule_set", "surface", "component", "entrypoint", "skill", "agent",
	} {
		if table, err := metadataDefinitionTable(" " + resourceType + " "); err != nil || !strings.HasSuffix(table, "_definitions") {
			t.Fatalf("resource=%q table=%q err=%v", resourceType, table, err)
		}
	}
	if _, err := metadataDefinitionTable("unknown"); err == nil {
		t.Fatal("unsupported resource table was accepted")
	}
}

func TestMetadataFieldShapeRequestNameAndExistingConfig(t *testing.T) {
	shape, err := metadataDefinitionShape(t.Context(), "field", "account.name", metadatamodel.MetadataDefinitionUpsertRequest{
		Name:      "Display Name",
		ObjectKey: "account",
		Payload:   json.RawMessage(`{"key":"name","type":"text","config":{}}`),
	})
	if err != nil || shape.Name != "Display Name" {
		t.Fatalf("shape=%#v err=%v", shape, err)
	}
}

func TestMetadataStorePublishesAndReadsEveryRecoveredDefinitionShape(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewMetadataStore(store)
	for _, test := range []struct {
		resourceType string
		resourceKey  string
		payload      string
	}{
		{resourceType: "integration_event_mapping", resourceKey: "crm.created", payload: `{"key":"crm.created","provider":"example","target_type":"record","object_key":"customer"}`},
		{resourceType: "operation_state_example", resourceKey: "customer.active", payload: `{"key":"customer.active","name":"Active","object_key":"customer"}`},
		{resourceType: "sensitive_field_policy", resourceKey: "customer.pii", payload: `{"key":"customer.pii","name":"PII","object_key":"customer"}`},
		{resourceType: "report_export_control", resourceKey: "customer.export", payload: `{"key":"customer.export","name":"Export","report_key":"customer.summary"}`},
		{resourceType: "surface", resourceKey: "customer.admin", payload: `{"key":"customer.admin","name":"Customer Admin","object_key":"customer"}`},
		{resourceType: "component", resourceKey: "customer.table", payload: `{"key":"customer.table","name":"Customer Table","object_key":"customer"}`},
		{resourceType: "scheduler", resourceKey: "customer.refresh", payload: `{"key":"customer.refresh","name":"Refresh","status":"enabled","target_type":"workflow","target_key":"scheduled:customer.review","schedule_type":"interval","interval_seconds":60}`},
	} {
		t.Run(test.resourceType, func(t *testing.T) {
			expectAbsent := ""
			published, err := publishDefinitionWithoutAuditForTest(t.Context(), repository, test.resourceType, test.resourceKey, metadatamodel.MetadataDefinitionUpsertRequest{
				ExpectedSchemaHash: &expectAbsent,
				Payload:            json.RawMessage(test.payload),
			})
			if err != nil {
				t.Fatal(err)
			}
			loaded, found, err := repository.GetDefinition(t.Context(), metadataTestInstallationScope(), test.resourceType, test.resourceKey)
			if err != nil || !found || loaded.SchemaHash != published.SchemaHash || loaded.ResourceKey != test.resourceKey {
				t.Fatalf("published=%+v loaded=%+v found=%v err=%v", published, loaded, found, err)
			}
		})
	}
}
