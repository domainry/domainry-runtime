package projectvalidation

import (
	"context"
	"strings"
	"testing"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
)

type integrationMappingTestInput struct{}
type integrationMappingTestOutput struct{}

func integrationMappingHandler(t *testing.T, kind string) runtimeext.BusinessHandler {
	t.Helper()
	handler, err := runtimeext.NewTypedBusinessHandler(
		runtimeext.HandlerDescriptor{
			ActionKey: "meeting.ingest", ObjectKey: "meeting", Label: "Ingest", Kind: kind, RiskLevel: runtimeext.HandlerRiskLow,
			AuditEvent: "meeting.ingested", InputType: runtimeext.TypeIdentity[integrationMappingTestInput](), OutputType: runtimeext.TypeIdentity[integrationMappingTestOutput](),
			HandlerRevision: "revision-1", ObjectCapabilities: []runtimeext.ActionObjectCapability{{ObjectKey: "meeting", Operations: []string{runtimeext.ObjectCapabilityGet}}},
		},
		func(runtimeext.ActionExecution) (struct{}, error) { return struct{}{}, nil },
		runtimeext.Handler[struct{}, integrationMappingTestInput, integrationMappingTestOutput](func(context.Context, struct{}, integrationMappingTestInput) (integrationMappingTestOutput, error) {
			return integrationMappingTestOutput{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestValidateCompositionClosesIntegrationActionInvocationScope(t *testing.T) {
	model, err := projectmodel.Decode([]byte(`{
  "schema_version":"1",
  "project":{"key":"crm","name":"CRM","time_zone":"UTC","initial_workspace_administrator_role":"administrator"},
  "objects":{"meeting":{"name":"Meeting","fields":{"title":"text!"}}},
  "roles":{"administrator":{"name":"Administrator","permissions":[]}},
  "identity_profiles":{}
}`))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, kind, recordID string
	}{{name: "object action receives record", kind: runtimeext.HandlerKindObjectOperation, recordID: "meeting-a"}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := runtimeext.NewProjectExtensionRegistry()
			mapping := integrationsdk.EventMappingRequirement{
				Key: "meeting-event", Provider: "calendar", EventType: "calendar.changed", TargetType: "action",
				ObjectKey: "meeting", ActionKey: "meeting.ingest", RecordID: test.recordID, Enabled: true,
			}
			if err := registry.RegisterProjectExtensions(runtimeext.ProjectExtensions{BusinessHandlers: []runtimeext.BusinessHandler{integrationMappingHandler(t, test.kind)}, Definitions: runtimeext.ProjectDefinitions{IntegrationMappings: []integrationsdk.EventMappingRequirement{mapping}}}); err != nil {
				t.Fatal(err)
			}
			registry.Freeze()
			err := ValidateComposition(model, registry, nil)
			if err == nil || !strings.Contains(err.Error(), "project_definition.integration_action_scope_invalid") {
				t.Fatalf("scope validation error=%v", err)
			}
		})
	}
}

func TestValidateCompositionClosesWebhookMappingToMatchingAutomationTrigger(t *testing.T) {
	model, err := projectmodel.Decode([]byte(`{
  "schema_version":"1",
  "project":{"key":"crm","name":"CRM","time_zone":"UTC","initial_workspace_administrator_role":"administrator"},
  "objects":{"contact":{"name":"Contact","fields":{"name":"text!"}}},
  "roles":{"administrator":{"name":"Administrator","permissions":[]}},
  "identity_profiles":{}
}`))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, ruleKey, provider, inputKey, eventFieldType, wantCode string
	}{
		{name: "matching rule", ruleKey: "contact.webhook", provider: "crm", inputKey: "name", eventFieldType: "text"},
		{name: "unknown rule", ruleKey: "missing", provider: "crm", inputKey: "name", eventFieldType: "text", wantCode: "project_definition.integration_automation_unknown"},
		{name: "mismatched provider", ruleKey: "contact.webhook", provider: "other", inputKey: "name", eventFieldType: "text", wantCode: "project_definition.integration_automation_trigger_mismatch"},
		{name: "unknown input", ruleKey: "contact.webhook", provider: "crm", inputKey: "missing", eventFieldType: "text", wantCode: "project_definition.integration_automation_input_unknown"},
		{name: "input type mismatch", ruleKey: "contact.webhook", provider: "crm", inputKey: "name", eventFieldType: "number", wantCode: "project_definition.integration_automation_input_type_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := runtimeext.NewProjectExtensionRegistry()
			rule := automationmodel.AutomationRuleSchema{
				Key: "contact.webhook", Name: "Contact webhook", ObjectKey: "contact", Enabled: true,
				Trigger:      automationmodel.AutomationTriggerSchema{Phase: "webhook", Source: "crm", Operation: "contact.changed"},
				Instructions: []automationmodel.AutomationInstructionSchema{{Key: "publish", Type: "emit_event"}},
			}
			mapping := integrationsdk.EventMappingRequirement{
				Key: "contact-event", Provider: test.provider, EventType: "contact.changed", TargetType: "automation", AutomationRuleKey: test.ruleKey, Enabled: true,
				AutomationInput: map[string]string{test.inputKey: "contact.name"}, EventFields: []integrationsdk.EventFieldRequirement{{Path: "contact.name", Type: test.eventFieldType}},
			}
			if err := registry.RegisterProjectExtensions(runtimeext.ProjectExtensions{Definitions: runtimeext.ProjectDefinitions{
				AutomationRules: []automationmodel.AutomationRuleSchema{rule}, IntegrationMappings: []integrationsdk.EventMappingRequirement{mapping},
			}}); err != nil {
				t.Fatal(err)
			}
			registry.Freeze()
			err := ValidateComposition(model, registry, nil)
			if test.wantCode == "" && err != nil {
				t.Fatalf("matching webhook mapping rejected: %v", err)
			}
			if test.wantCode != "" && (err == nil || !strings.Contains(err.Error(), test.wantCode)) {
				t.Fatalf("validation error=%v, want %s", err, test.wantCode)
			}
		})
	}
}
