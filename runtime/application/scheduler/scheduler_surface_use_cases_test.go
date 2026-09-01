package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type schedulerSurfaceDefinitionSource struct {
	definitions []PublishedDefinition
	definition  PublishedDefinition
	found       bool
	versions    []SchedulerDefinitionVersion
	listErr     error
}

func (s schedulerSurfaceDefinitionSource) ListSchedulerDefinitions(context.Context) ([]PublishedDefinition, error) {
	return s.definitions, s.listErr
}
func (s schedulerSurfaceDefinitionSource) GetSchedulerDefinition(context.Context, string) (PublishedDefinition, bool, error) {
	return s.definition, s.found, nil
}
func (s schedulerSurfaceDefinitionSource) ListSchedulerDefinitionVersions(context.Context, string) ([]SchedulerDefinitionVersion, error) {
	return s.versions, nil
}

func TestSchedulerDefinitionSurfaceReadsPublishedDefinitions(t *testing.T) {
	published := PublishedDefinition{Key: "nightly", Data: map[string]any{"key": "nightly", "status": "enabled", "target_type": "workflow"}}
	service := NewSchedulerApplicationService(nil)
	service.UseDefinitionSource(schedulerSurfaceDefinitionSource{definitions: []PublishedDefinition{published}, definition: published, found: true, versions: []SchedulerDefinitionVersion{{VersionID: "v1", Event: "published", Data: published.Data}}})
	principal := schedulerTestPrincipal(ActionListTenantAdminSchedulerDefinitions, ActionGetTenantAdminSchedulerDefinition)
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
	service := NewSchedulerApplicationService(nil)
	principal := schedulerTestPrincipal(ActionListTenantAdminSchedulerDefinitions)
	if _, err := service.TenantAdminDefinitions(t.Context(), principal); apperror.CodeOf(err) != "backend.scheduler.definition_source_unavailable" {
		t.Fatalf("missing source error = %v", err)
	}
	service.UseDefinitionSource(schedulerSurfaceDefinitionSource{listErr: errors.New("read failed")})
	if _, err := service.TenantAdminDefinitions(t.Context(), principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("source failure = %v", err)
	}
}

func TestSchedulerOpsAuthorizationDoesNotProjectRuntimeJobLifecycle(t *testing.T) {
	if err := NewSchedulerApplicationService(nil).AuthorizeOpsRead(t.Context(), schedulerTestPrincipal(ActionGetOpsSchedulerState)); err != nil {
		t.Fatal(err)
	}
	if err := NewSchedulerApplicationService(nil).AuthorizeOpsRead(t.Context(), principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("anonymous error = %v", err)
	}
}

func TestSchedulerSurfaceAuthorizationRequiresItsExactAction(t *testing.T) {
	service := NewSchedulerApplicationService(nil)
	for _, permission := range []string{"operations.read", "scheduler.command", "workspace.admin"} {
		if err := service.AuthorizeOpsRead(t.Context(), schedulerTestPrincipal(permission)); apperror.CodeOf(err) != "backend.scheduler.permission_required" {
			t.Fatalf("unrelated permission %q authorized Scheduler state: %v", permission, err)
		}
	}
}
