package projectvalidation

import (
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

func TestValidateCompositionResolvesScheduledWorkflowTargetPrefix(t *testing.T) {
	model, err := projectmodel.Decode([]byte(`{
  "schema_version":"1",
  "project":{"key":"payments","name":"Payments","time_zone":"UTC","initial_workspace_administrator_role":"administrator"},
  "objects":{},
  "roles":{"administrator":{"name":"Administrator","permissions":[]}},
  "identity_profiles":{}
}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		operation string
		wantCode  string
	}{
		{name: "scheduler protocol target", operation: "scheduled:payment_reconciliation"},
		{name: "unknown target", operation: "scheduled:missing", wantCode: "project_definition.schedule_workflow_unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := runtimeext.NewProjectExtensionRegistry()
			extensions := runtimeext.ProjectExtensions{Definitions: runtimeext.ProjectDefinitions{
				Workflows: []definitionmodel.WorkflowSchema{{Key: "payment_reconciliation", Name: "Payment reconciliation"}},
				Schedules: []schedulersdk.Definition{{
					Key: "payment_reconciliation", Revision: "1", Schedule: schedulersdk.Schedule{Type: "interval", IntervalSeconds: 300},
					Target: schedulersdk.TargetRef{Type: "runtime_operation", Owner: "workflow", Operation: test.operation},
				}},
			}}
			if err := registry.RegisterProjectExtensions(extensions); err != nil {
				t.Fatal(err)
			}
			registry.Freeze()
			err := ValidateComposition(model, registry, nil)
			if test.wantCode == "" && err != nil {
				t.Fatalf("scheduled workflow target rejected: %v", err)
			}
			if test.wantCode != "" && (err == nil || !strings.Contains(err.Error(), test.wantCode)) {
				t.Fatalf("validation error=%v, want %s", err, test.wantCode)
			}
		})
	}
}
