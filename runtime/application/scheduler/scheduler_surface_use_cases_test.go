package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type schedulerSurfaceDefinitionSource struct {
	definitions    []recordmodel.Record
	definition     recordmodel.Record
	found          bool
	versions       []SchedulerDefinitionVersion
	listErr        error
	getErr         error
	versionListErr error
}

func (s schedulerSurfaceDefinitionSource) ListSchedulerDefinitions(context.Context) ([]recordmodel.Record, error) {
	return s.definitions, s.listErr
}

func (s schedulerSurfaceDefinitionSource) GetSchedulerDefinition(context.Context, string) (recordmodel.Record, bool, error) {
	return s.definition, s.found, s.getErr
}

func (s schedulerSurfaceDefinitionSource) ListSchedulerDefinitionVersions(context.Context, string) ([]SchedulerDefinitionVersion, error) {
	return s.versions, s.versionListErr
}

func TestSchedulerSurfaceDTOsDoNotCrossLeak(t *testing.T) {
	source := recordmodel.Record{
		ID:        "nightly",
		CreatedAt: "created",
		UpdatedAt: "updated",
		Data: map[string]any{
			"key": "nightly", "name": "Nightly", "status": "enabled",
			"schedule_type": "cron", "cron_expression": "0 0 * * *",
			"business_calendar_key": "cn-workdays", "target_type": "workflow", "target_key": "approval",
			"lease_owner": "worker-secret", "lease_expires_at": "lease", "fencing_token": 7,
			"error_message": "database details", "payload": map[string]any{"customer": "secret"},
			"secret_reference": "vault://scheduler",
		},
	}
	adminJSON, err := json.Marshal(projectTenantAdminSchedulerDefinition(source))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbiddenField := range []string{"lease_owner", "lease_expires_at", "fencing_token", "error_message", "payload", "secret_reference"} {
		if strings.Contains(string(adminJSON), forbiddenField) {
			t.Fatalf("Tenant Admin DTO leaked %q: %s", forbiddenField, adminJSON)
		}
	}

	opsJSON, err := json.Marshal(projectOpsSchedulerRun(source))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbiddenField := range []string{"name", "cron_expression", "business_calendar_key", "target_key", "payload", "secret_reference"} {
		if strings.Contains(string(opsJSON), forbiddenField) {
			t.Fatalf("Ops DTO leaked %q: %s", forbiddenField, opsJSON)
		}
	}

	deadLetter := projectOpsSchedulerDeadLetter(recordmodel.Record{ID: "dead-1", Data: map[string]any{
		"job_run_id": "run-1", "scheduler_definition_key": "nightly", "status": "open", "failed_at": "2026-07-25T17:00:03Z",
	}})
	if deadLetter.RunID != "run-1" || deadLetter.DefinitionKey != "nightly" || deadLetter.FailedAt != "2026-07-25T17:00:03Z" {
		t.Fatalf("dead-letter projection = %+v", deadLetter)
	}
}

func TestSchedulerSurfaceTenantAdminDefinitionBoundaries(t *testing.T) {
	readPrincipal := schedulerTestPrincipal("scheduler.definition.read")
	service := &SchedulerApplicationService{}

	if _, err := service.TenantAdminDefinitions(t.Context(), principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("anonymous definitions error = %v", err)
	}
	if _, err := service.TenantAdminDefinitions(t.Context(), readPrincipal); apperror.CodeOf(err) != "backend.scheduler.definition_source_unavailable" {
		t.Fatalf("missing source error = %v", err)
	}

	sourceFailure := errors.New("definition source failure")
	service.UseDefinitionSource(schedulerSurfaceDefinitionSource{listErr: sourceFailure})
	if _, err := service.TenantAdminDefinitions(t.Context(), readPrincipal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("definition list error = %v", err)
	}

	record := recordmodel.Record{
		ID:        "fallback-key",
		CreatedAt: "created",
		UpdatedAt: "updated",
		Data: map[string]any{
			"name": "Nightly",
		},
	}
	service.UseDefinitionSource(schedulerSurfaceDefinitionSource{
		definitions: []recordmodel.Record{record},
		definition:  record,
		found:       true,
		versions: []SchedulerDefinitionVersion{{
			VersionID: "version-1",
			Event:     "published",
			CreatedAt: "version-created",
			Data:      map[string]any{"status": "enabled"},
		}},
	})
	definitions, err := service.TenantAdminDefinitions(t.Context(), readPrincipal)
	if err != nil || len(definitions) != 1 || definitions[0].Key != "fallback-key" {
		t.Fatalf("definitions = %+v, err = %v", definitions, err)
	}
	definition, err := service.TenantAdminDefinition(t.Context(), record.ID, readPrincipal)
	if err != nil || definition.Key != record.ID {
		t.Fatalf("definition = %+v, err = %v", definition, err)
	}
	versions, err := service.TenantAdminDefinitionVersions(t.Context(), record.ID, readPrincipal)
	if err != nil || len(versions) != 1 || versions[0].VersionID != "version-1" || versions[0].Value.Key != record.ID {
		t.Fatalf("versions = %+v, err = %v", versions, err)
	}

	service.UseDefinitionSource(schedulerSurfaceDefinitionSource{getErr: sourceFailure})
	if _, err := service.TenantAdminDefinition(t.Context(), record.ID, readPrincipal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("definition read error = %v", err)
	}
	service.UseDefinitionSource(schedulerSurfaceDefinitionSource{definition: record, found: true, versionListErr: sourceFailure})
	if _, err := service.TenantAdminDefinitionVersions(t.Context(), record.ID, readPrincipal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("definition version error = %v", err)
	}
}

func TestSchedulerSurfaceAuthorizationAlternativesAndFailures(t *testing.T) {
	if _, err := (&SchedulerApplicationService{}).TenantAdminAuthoringContract(t.Context(), principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("anonymous authoring error = %v", err)
	}

	for _, permission := range []string{"workspace.admin", "metadata.write", "scheduler.definition.write"} {
		if err := schedulerDefinitionWriteAllowed(schedulerTestPrincipal(permission)); err != nil {
			t.Fatalf("definition write permission %q rejected: %v", permission, err)
		}
	}
	if err := schedulerDefinitionWriteAllowed(schedulerTestPrincipal("metadata.read")); apperror.CodeOf(err) != "backend.scheduler.permission_required" {
		t.Fatalf("unrelated definition write permission error = %v", err)
	}
	if err := schedulerDefinitionWriteAllowed(principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("anonymous definition write error = %v", err)
	}

	for _, permission := range []string{"operations.read", "job_run.read", "job_run.update", "scheduler.command"} {
		if err := schedulerOpsReadAllowed(schedulerTestPrincipal(permission)); err != nil {
			t.Fatalf("ops read permission %q rejected: %v", permission, err)
		}
	}
	if err := schedulerOpsReadAllowed(principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("anonymous ops read error = %v", err)
	}

	for _, permission := range []string{"scheduler.definition.run", "job_run.update", "scheduler.command"} {
		if err := schedulerOpsCommandAllowed(schedulerTestPrincipal(permission)); err != nil {
			t.Fatalf("ops command permission %q rejected: %v", permission, err)
		}
	}
	if err := schedulerOpsCommandAllowed(principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("anonymous ops command error = %v", err)
	}
}

func TestSchedulerSurfaceOpsStateFailureBoundaries(t *testing.T) {
	principal := schedulerTestPrincipal("operations.read")
	if _, err := (&SchedulerApplicationService{}).OpsState(t.Context(), principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("anonymous ops state error = %v", err)
	}
	if _, err := NewSchedulerApplicationService(schedulerTestSchema(), nil, nil, nil).OpsState(t.Context(), principal); apperror.CodeOf(err) != "backend.scheduler.repository_unavailable" {
		t.Fatalf("missing repository error = %v", err)
	}

	listFailure := errors.New("record list failure")
	for _, failingObject := range []string{"job_run", "job_run_event", "job_dead_letter"} {
		failingObject := failingObject
		repository := &schedulerRepositoryFake{
			list: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
				if object.Key == failingObject {
					return recordmodel.RecordPageResult{}, listFailure
				}
				return recordmodel.RecordPageResult{}, nil
			},
		}
		if _, err := NewSchedulerApplicationService(schedulerTestSchema(), nil, repository, nil).OpsState(t.Context(), principal); apperror.CodeOf(err) != "backend.internal" {
			t.Fatalf("%s list error = %v", failingObject, err)
		}
	}

	service := NewSchedulerApplicationService(
		schedulerSchemaStub{snapshot: metadatamodel.MetadataSchemaSnapshot{}},
		nil,
		&schedulerRepositoryFake{},
		nil,
	)
	if _, err := service.opsRecords(t.Context(), principal, "job_run"); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
		t.Fatalf("missing runtime object error = %v", err)
	}
}

func TestSchedulerSurfaceProjectionEmptyAndNilValues(t *testing.T) {
	if projected := ProjectOpsSchedulerOperation(SchedulerOperationResult{Status: "accepted"}); projected.Run != nil {
		t.Fatalf("empty operation unexpectedly projected a run: %+v", projected)
	}
	projected := ProjectOpsSchedulerOperation(SchedulerOperationResult{
		Status: "completed",
		Run:    recordmodel.Record{ID: "run-1", Data: map[string]any{"status": "succeeded"}},
	})
	if projected.Run == nil || projected.Run.ID != "run-1" || projected.Run.Status != "succeeded" {
		t.Fatalf("operation run projection = %+v", projected)
	}
	if value := schedulerDTOString(nil, "missing"); value != "" {
		t.Fatalf("nil DTO data = %q", value)
	}
	if value := schedulerDTOString(map[string]any{"missing": nil}, "missing"); value != "" {
		t.Fatalf("nil DTO value = %q", value)
	}
	if value := valueOrID(map[string]any{"key": "  "}, "key", " fallback "); value != "fallback" {
		t.Fatalf("fallback value = %q", value)
	}
	if apperror.CodeOf(unavailableDefinitionSource()) != "backend.scheduler.definition_source_unavailable" {
		t.Fatal("definition source unavailable error lost its code")
	}
	if apperror.CodeOf(schedulerErrorUnavailable("backend.scheduler.test_unavailable")) != "backend.scheduler.test_unavailable" {
		t.Fatal("scheduler unavailable error lost its code")
	}
}

func TestWorkspaceAdminAloneCannotExecuteSchedulerOps(t *testing.T) {
	workspaceAdmin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true,
		WorkspaceID: "workspace-a"},
	}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}},
	)
	if err := schedulerOpsCommandAllowed(workspaceAdmin); apperror.CodeOf(err) != "backend.scheduler.permission_required" {
		t.Fatalf("workspace.admin unexpectedly granted scheduler ops: %v", err)
	}
	if err := schedulerOpsReadAllowed(workspaceAdmin); apperror.CodeOf(err) != "backend.scheduler.permission_required" {
		t.Fatalf("workspace.admin unexpectedly granted scheduler diagnostics: %v", err)
	}
	ops := workspaceAdmin
	accessfixture.Set(&ops, accessfixture.Bundle{Permissions: []string{"job_run.update"}})
	if err := schedulerOpsCommandAllowed(ops); err != nil {
		t.Fatalf("explicit scheduler Ops permission rejected: %v", err)
	}
}

func TestTenantAdminSchedulerAuthoringContractUsesGovernedChangePlan(t *testing.T) {
	service := NewSchedulerApplicationService(nil, nil, nil, nil)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true,
		WorkspaceID: "workspace-a"},
	}, accessfixture.Bundle{Permissions: []string{"scheduler.definition.write"}},
	)
	contract, err := service.TenantAdminAuthoringContract(t.Context(), principal)
	if err != nil {
		t.Fatal(err)
	}
	if !contract.RequiresChangePlan || contract.MutationOwner != "domain_change_plan" || contract.BusinessCalendarField != "business_calendar_key" {
		t.Fatalf("authoring contract = %+v", contract)
	}
}

func TestOpsSchedulerStateProjectsBackendRuntimeAvailability(t *testing.T) {
	principal := schedulerTestPrincipal("operations.read")
	listCalls := 0
	repository := &schedulerRepositoryFake{
		list: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
			listCalls++
			switch object.Key {
			case "job_run":
				return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "run-1", Data: map[string]any{"status": "failed"}}}}, nil
			case "job_run_event":
				return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "attempt-1", Data: map[string]any{"event": "failed"}}}}, nil
			case "job_dead_letter":
				return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "dead-1", Data: map[string]any{"status": "open"}}}}, nil
			default:
				t.Fatalf("unexpected object %q", object.Key)
				return recordmodel.RecordPageResult{}, nil
			}
		},
	}

	incompleteSchema := schedulerSchemaStub{snapshot: metadatamodel.MetadataSchemaSnapshot{
		Objects: []definitionmodel.ObjectSchema{{Key: "job_run"}},
	}}
	unavailable, err := NewSchedulerApplicationService(incompleteSchema, nil, repository, nil).OpsState(t.Context(), principal)
	if err != nil {
		t.Fatal(err)
	}
	if unavailable.Provisioned || len(unavailable.Runs) != 0 || len(unavailable.Attempts) != 0 || len(unavailable.DeadLetters) != 0 || listCalls != 0 {
		t.Fatalf("unavailable state = %+v, list calls = %d", unavailable, listCalls)
	}

	available, err := NewSchedulerApplicationService(schedulerTestSchema(), nil, repository, nil).OpsState(t.Context(), principal)
	if err != nil {
		t.Fatal(err)
	}
	if !available.Provisioned || len(available.Runs) != 1 || len(available.Attempts) != 1 || len(available.DeadLetters) != 1 || listCalls != 3 {
		t.Fatalf("available state = %+v, list calls = %d", available, listCalls)
	}
}
