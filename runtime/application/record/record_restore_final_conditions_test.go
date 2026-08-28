package record

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func restoreFinalObject(fields ...definitionmodel.FieldSchema) definitionmodel.ObjectSchema {
	base := []definitionmodel.FieldSchema{
		{Key: "status", Type: "text"},
		{Key: "deleted_at", Type: "text"},
		{Key: "deleted_by", Type: "text"},
	}
	return definitionmodel.ObjectSchema{Key: "customer", Fields: append(base, fields...)}
}

func restoreFinalRepository(data map[string]any) *restoreEdgeRepository {
	return &restoreEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: data}}
}

func restoreFinalService(object definitionmodel.ObjectSchema, repository *restoreEdgeRepository) *RecordRestoreApplicationService {
	return NewRecordRestoreApplicationService(RecordRestoreDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		UpdatedTriggers: func(string, map[string]any, map[string]any) []string { return []string{"updated"} },
	})
}

func TestRestoreAuthorizationSchedulerAndOptionalDependencyEdges(t *testing.T) {
	if _, err := (&RecordRestoreApplicationService{}).Restore(t.Context(), "customer", "customer-1", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization err=%v", err)
	}
	scheduler := restoreFinalObject()
	scheduler.Key = "job_run"
	scheduler.Config = map[string]any{"scheduler_runtime": true}
	service := restoreFinalService(scheduler, restoreFinalRepository(map[string]any{"status": "deleted", "deleted_at": "now", "deleted_by": "user-1"}))
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	if _, err := service.Restore(t.Context(), "job_run", "job-1", principal); apperror.CodeOf(err) != "backend.scheduler.runtime_api_required" {
		t.Fatalf("scheduler err=%v", err)
	}

	object := restoreFinalObject()
	repository := restoreFinalRepository(map[string]any{"status": "deleted", "deleted_at": "now", "deleted_by": "user-1"})
	service = restoreFinalService(object, repository)
	restored, err := service.Restore(t.Context(), "customer", "customer-1", principal)
	if err != nil || restored.Data["status"] != "active" || repository.commit.Operation != "restore" {
		t.Fatalf("restored=%#v commit=%#v err=%v", restored, repository.commit, err)
	}
}

func TestRestoreNormalizationWritableAndValidationFailures(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
	object := restoreFinalObject()
	service := restoreFinalService(object, restoreFinalRepository(map[string]any{
		"status": "deleted", "deleted_at": "now", "deleted_by": "user-1", "unknown": "value",
	}))
	if _, err := service.Restore(t.Context(), "customer", "customer-1", principal); apperror.CodeOf(err) != "backend.validation.unknown_field" {
		t.Fatalf("normalize err=%v", err)
	}

	restricted := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "status", Write: false}}})
	service = restoreFinalService(object, restoreFinalRepository(map[string]any{"status": "deleted", "deleted_at": "now", "deleted_by": "user-1"}))
	if _, err := service.Restore(t.Context(), "customer", "customer-1", restricted); apperror.CodeOf(err) != "backend.validation.field_not_writable" {
		t.Fatalf("writable err=%v", err)
	}

	object = restoreFinalObject(definitionmodel.FieldSchema{Key: "name", Type: "text", Required: true})
	service = restoreFinalService(object, restoreFinalRepository(map[string]any{"status": "deleted", "deleted_at": "now", "deleted_by": "user-1"}))
	if _, err := service.Restore(t.Context(), "customer", "customer-1", principal); apperror.CodeOf(err) != "backend.validation.required" {
		t.Fatalf("validation err=%v", err)
	}
}

func TestRestoreErrorEmptyCodeAndBlankParameterKey(t *testing.T) {
	plainCoded := recordCodedError{code: " "}
	err := recordRestoreErrorFrom(apperror.KindBadRequest, plainCoded)
	if apperror.CodeOf(err) != "backend.bad_request" {
		t.Fatalf("coded fallback err=%#v", err)
	}
	err = recordRestoreError(apperror.KindBadRequest, "backend.bad_request", nil, " ", "ignored")
	if apperror.ParamsOf(err) != nil {
		t.Fatalf("blank parameter err=%#v", err)
	}
}
