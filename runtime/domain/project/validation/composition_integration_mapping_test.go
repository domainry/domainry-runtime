package projectvalidation

import (
	"context"
	"strings"
	"testing"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
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
