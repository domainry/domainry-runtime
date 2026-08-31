// Scheduler application service tests.
package scheduler

import (
	"reflect"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestScheduledWorkflowRuntimeMethodBudget(t *testing.T) {
	contract := reflect.TypeOf((*ScheduledWorkflowRuntime)(nil)).Elem()
	if contract.NumMethod() != 1 || contract.NumMethod() > 15 {
		t.Fatalf("scheduler operation runtime has %d methods", contract.NumMethod())
	}
}

func TestSchedulerServicePreviewDoesNotRequireRuntimeServices(t *testing.T) {
	service := NewSchedulerApplicationService(nil)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	preview, err := service.PreviewDefinition(t.Context(), map[string]any{"target_type": "workflow", "target_key": "scheduled:*", "schedule_type": "interval", "interval_seconds": 60}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.NextRuns) != 3 {
		t.Fatalf("expected three next runs, got %+v", preview)
	}
}

func TestSchedulerServicePreviewsLeafScheduleWithoutJobEnvelope(t *testing.T) {
	service := NewSchedulerApplicationService(nil)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	preview, err := service.PreviewSchedule(t.Context(), map[string]any{"schedule_type": "weekly_at", "time_of_day": "09:30", "day_of_week": "monday", "timezone": "Asia/Shanghai"}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.NextRuns) != 3 {
		t.Fatalf("expected three next runs, got %+v", preview)
	}
}
