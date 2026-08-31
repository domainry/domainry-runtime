// Scheduler application service tests.
package scheduler

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"reflect"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type schedulerSchemaStub struct {
	snapshot appschemamodel.ApplicationSchemaSnapshot
}

func (s schedulerSchemaStub) SchemaForPrincipal(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
	return s.snapshot
}

func (schedulerSchemaStub) ListSchedulerDefinitions(context.Context) ([]recordmodel.Record, error) {
	return []recordmodel.Record{}, nil
}

func (schedulerSchemaStub) GetSchedulerDefinition(context.Context, string) (recordmodel.Record, bool, error) {
	return recordmodel.Record{}, false, nil
}

func (schedulerSchemaStub) ListSchedulerDefinitionVersions(context.Context, string) ([]SchedulerDefinitionVersion, error) {
	return []SchedulerDefinitionVersion{}, nil
}

func TestSchedulerOperationRuntimeMethodBudget(t *testing.T) {
	contract := reflect.TypeOf((*SchedulerOperationRuntime)(nil)).Elem()
	if contract.NumMethod() != 1 || contract.NumMethod() > 15 {
		t.Fatalf("scheduler operation runtime has %d methods", contract.NumMethod())
	}
}

func TestSchedulerServicePreviewDoesNotRequireRuntimeServices(t *testing.T) {
	schema := schedulerSchemaStub{snapshot: appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "job_definition"}}}}
	service := NewSchedulerApplicationService(schema, nil, nil, nil)
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
	service := NewSchedulerApplicationService(nil, nil, nil, nil)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	preview, err := service.PreviewSchedule(t.Context(), map[string]any{"schedule_type": "weekly_at", "time_of_day": "09:30", "day_of_week": "monday", "timezone": "Asia/Shanghai"}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.NextRuns) != 3 {
		t.Fatalf("expected three next runs, got %+v", preview)
	}
}
