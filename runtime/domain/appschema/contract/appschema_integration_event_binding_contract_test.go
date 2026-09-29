package contract

import (
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func TestIntegrationEventBindingsValidateAutomationInputPaths(t *testing.T) {
	mapping := appschemamodel.IntegrationEventMappingSchema{
		AutomationInput: map[string]string{"name": "contact.name"},
		EventFields:     []appschemamodel.IntegrationEventFieldSchema{{Path: "contact.name", Type: "text"}},
	}
	environment, issues := IntegrationEventBindings(mapping)
	if _, found := environment["$event.contact.name"]; len(issues) != 0 || !found {
		t.Fatalf("environment=%#v issues=%#v", environment, issues)
	}
	mapping.AutomationInput["name"] = "contact.missing"
	_, issues = IntegrationEventBindings(mapping)
	if len(issues) != 1 || issues[0].Field != "automation_input.name" || issues[0].Code != "integration.event_path_undeclared" {
		t.Fatalf("issues=%#v", issues)
	}
}
