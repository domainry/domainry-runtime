package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type schedulerSurfaceDefinitionSource struct {
	definitions []recordmodel.Record
	definition  recordmodel.Record
	found       bool
	versions    []SchedulerDefinitionVersion
	listErr     error
}

func (s schedulerSurfaceDefinitionSource) ListSchedulerDefinitions(context.Context) ([]recordmodel.Record, error) {
	return s.definitions, s.listErr
}
func (s schedulerSurfaceDefinitionSource) GetSchedulerDefinition(context.Context, string) (recordmodel.Record, bool, error) {
	return s.definition, s.found, nil
}
func (s schedulerSurfaceDefinitionSource) ListSchedulerDefinitionVersions(context.Context, string) ([]SchedulerDefinitionVersion, error) {
	return s.versions, nil
}

func TestSchedulerDefinitionSurfaceReadsPublishedDefinitions(t *testing.T) {
	record := recordmodel.Record{ID: "nightly", Data: map[string]any{"key": "nightly", "status": "enabled", "target_type": "workflow"}}
	service := NewSchedulerApplicationService(nil, nil, nil, nil)
	service.UseDefinitionSource(schedulerSurfaceDefinitionSource{definitions: []recordmodel.Record{record}, definition: record, found: true, versions: []SchedulerDefinitionVersion{{VersionID: "v1", Event: "published", Data: record.Data}}})
	principal := schedulerTestPrincipal("scheduler.definition.read")
	definitions, err := service.TenantAdminDefinitions(t.Context(), principal)
	if err != nil || len(definitions) != 1 || definitions[0].Key != "nightly" {
		t.Fatalf("definitions = %+v, err = %v", definitions, err)
	}
	definition, err := service.TenantAdminDefinition(t.Context(), "nightly", principal)
	if err != nil || definition.Key != "nightly" {
		t.Fatalf("definition = %+v, err = %v", definition, err)
	}
	versions, err := service.TenantAdminDefinitionVersions(t.Context(), "nightly", principal)
	if err != nil || len(versions) != 1 || versions[0].VersionID != "v1" {
		t.Fatalf("versions = %+v, err = %v", versions, err)
	}
}

func TestSchedulerDefinitionSurfaceReportsSourceFailures(t *testing.T) {
	service := NewSchedulerApplicationService(nil, nil, nil, nil)
	principal := schedulerTestPrincipal("scheduler.definition.read")
	if _, err := service.TenantAdminDefinitions(t.Context(), principal); apperror.CodeOf(err) != "backend.scheduler.definition_source_unavailable" {
		t.Fatalf("missing source error = %v", err)
	}
	service.UseDefinitionSource(schedulerSurfaceDefinitionSource{listErr: errors.New("read failed")})
	if _, err := service.TenantAdminDefinitions(t.Context(), principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("source failure = %v", err)
	}
}

func TestSchedulerOpsStateNoLongerReadsRuntimeJobLifecycle(t *testing.T) {
	state, err := NewSchedulerApplicationService(nil, nil, nil, nil).OpsState(t.Context(), schedulerTestPrincipal("operations.read"))
	if err != nil {
		t.Fatal(err)
	}
	if state.Provisioned || len(state.Runs) != 0 || len(state.Attempts) != 0 || len(state.DeadLetters) != 0 {
		t.Fatalf("Runtime unexpectedly owns scheduler execution state: %+v", state)
	}
	if _, err := NewSchedulerApplicationService(nil, nil, nil, nil).OpsState(t.Context(), principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("anonymous error = %v", err)
	}
}

func TestSchedulerSurfaceAuthorizationUsesSchedulerCapabilities(t *testing.T) {
	for _, permission := range []string{"operations.read", "scheduler.command"} {
		if err := schedulerOpsReadAllowed(schedulerTestPrincipal(permission)); err != nil {
			t.Fatalf("read permission %q rejected: %v", permission, err)
		}
	}
	for _, permission := range []string{"scheduler.definition.run", "scheduler.command"} {
		if err := schedulerOpsCommandAllowed(schedulerTestPrincipal(permission)); err != nil {
			t.Fatalf("command permission %q rejected: %v", permission, err)
		}
	}
	if err := schedulerOpsCommandAllowed(schedulerTestPrincipal("job_run.update")); apperror.CodeOf(err) != "backend.scheduler.permission_required" {
		t.Fatalf("legacy permission unexpectedly accepted: %v", err)
	}
}
