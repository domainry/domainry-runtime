package record

import accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
)

type recordDeleteEmptyCodeError struct{}

func (recordDeleteEmptyCodeError) Error() string                  { return "empty code" }
func (recordDeleteEmptyCodeError) ErrorCode() string              { return " " }
func (recordDeleteEmptyCodeError) ErrorParams() map[string]string { return nil }

func TestRecordDeleteAuthorizationSchedulerAndSoftValidationEdges(t *testing.T) {
	principal := recordUpdateFinalPrincipal()
	service := NewRecordDeleteApplicationService(RecordDeleteDependencies{})
	if err := service.Delete(t.Context(), "customer", "customer-1", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization error = %v", err)
	}
	repository := &deleteEdgeRepository{found: true, record: recordmodel.Record{ID: "run-1", Data: map[string]any{}}}
	dependencies := recordDeleteEdgeDependencies(repository, definitionmodel.ObjectSchema{Key: "job_run", Config: map[string]any{"scheduler_runtime": true}})
	if err := NewRecordDeleteApplicationService(dependencies).Delete(t.Context(), "job_run", "run-1", principal); apperror.CodeOf(err) != "backend.scheduler.runtime_api_required" {
		t.Fatalf("scheduler guard error = %v", err)
	}

	requiredObject := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}, {Key: "deleted_at", Type: "datetime"}, {Key: "deleted_by", Type: "text"}, {Key: "name", Type: "text", Required: true}}}
	repository = &deleteEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"status": "active"}}}
	dependencies = recordDeleteEdgeDependencies(repository, requiredObject)
	if err := NewRecordDeleteApplicationService(dependencies).Delete(t.Context(), "customer", "customer-1", principal); apperror.CodeOf(err) != "backend.validation.required" {
		t.Fatalf("soft-delete validation error = %v", err)
	}
	invalidObject := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}, {Key: "deleted_at", Type: "datetime"}, {Key: "deleted_by", Type: "text"}, {Key: "age", Type: "number"}}}
	repository = &deleteEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"status": "active", "age": "invalid"}}}
	dependencies = recordDeleteEdgeDependencies(repository, invalidObject)
	if err := NewRecordDeleteApplicationService(dependencies).Delete(t.Context(), "customer", "customer-1", principal); apperror.CodeOf(err) != "backend.validation.number" {
		t.Fatalf("soft-delete normalization error = %v", err)
	}

	writableObject := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}, {Key: "deleted_at", Type: "datetime"}, {Key: "deleted_by", Type: "text"}}}
	repository = &deleteEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"status": "active"}}}
	dependencies = recordDeleteEdgeDependencies(repository, writableObject)
	denied := principal
	accessfixture.Set(&denied, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "status", Read: true, Write: false}}})
	if err := NewRecordDeleteApplicationService(dependencies).Delete(t.Context(), "customer", "customer-1", denied); apperror.CodeOf(err) != "backend.validation.field_not_writable" {
		t.Fatalf("soft-delete writable error = %v", err)
	}
}

func TestRecordSoftDeleteMissingOptionalFieldsAndPorts(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}, {Key: "deleted_at", Type: "datetime"}, {Key: "deleted_by", Type: "text"}}}
	repository := &deleteEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"status": "active"}}}
	dependencies := recordDeleteEdgeDependencies(repository, object)
	dependencies.CanAccess = nil
	dependencies.CanWrite = nil
	dependencies.UpdatedTriggers = func(string, map[string]any, map[string]any) []string {
		return []string{"record_updated:customer.status"}
	}
	dependencies.PrepareWorkflow = nil
	dependencies.ExecuteWorkflow = nil
	if err := NewRecordDeleteApplicationService(dependencies).Delete(t.Context(), object.Key, "customer-1", recordUpdateFinalPrincipal()); err != nil {
		t.Fatalf("minimal soft delete error = %v", err)
	}
}

func TestRecordHardDeleteRelationFailureCancellationNilUpdateAndOutbox(t *testing.T) {
	principal := recordUpdateFinalPrincipal()
	root := definitionmodel.ObjectSchema{Key: "root", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	note := definitionmodel.ObjectSchema{Key: "note", Fields: []definitionmodel.FieldSchema{{Key: "root_id", Type: "relation", Config: map[string]any{"target": "root", "on_delete": "set_null"}}}}
	schema := map[string]definitionmodel.ObjectSchema{"root": root, "note": note}
	edgeErr := errors.New("relation list failed")
	repository := &deleteEdgeRepository{found: true, record: recordmodel.Record{ID: "root-1", Data: map[string]any{"name": "root"}}, listErr: edgeErr}
	dependencies := recordDeleteEdgeDependencies(repository, root)
	dependencies.Relations = recordservice.NewRecordDeleteRelationDomainService(repository, func() map[string]definitionmodel.ObjectSchema { return schema })
	if err := NewRecordDeleteApplicationService(dependencies).Delete(t.Context(), root.Key, "root-1", principal); !errors.Is(err, edgeErr) {
		t.Fatalf("relation failure = %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	repository = &deleteEdgeRepository{found: true, record: recordmodel.Record{ID: "root-1", Data: map[string]any{"name": "root"}}, lists: map[string][]recordmodel.Record{}, onList: cancel}
	dependencies = recordDeleteEdgeDependencies(repository, root)
	dependencies.Relations = recordservice.NewRecordDeleteRelationDomainService(repository, func() map[string]definitionmodel.ObjectSchema { return schema })
	if err := NewRecordDeleteApplicationService(dependencies).Delete(ctx, root.Key, "root-1", principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("post-relation cancellation = %v", err)
	}

	repository = &deleteEdgeRepository{found: true, record: recordmodel.Record{ID: "root-1", Data: map[string]any{"name": "root"}}, lists: map[string][]recordmodel.Record{"note": {{ID: "note-1", Data: map[string]any{"root_id": "root-1"}}}}}
	dependencies = recordDeleteEdgeDependencies(repository, root)
	dependencies.Relations = recordservice.NewRecordDeleteRelationDomainService(repository, func() map[string]definitionmodel.ObjectSchema { return schema })
	dependencies.PlanUpdateReference = nil
	dependencies.AfterOutbox = func(string, string, map[string]any, recordmodel.Record, principalmodel.Principal) []integrationmodel.IntegrationOutboxMessage {
		return []integrationmodel.IntegrationOutboxMessage{{ID: "outbox"}}
	}
	if err := NewRecordDeleteApplicationService(dependencies).Delete(t.Context(), root.Key, "root-1", principal); apperror.CodeOf(err) != "backend.internal" || len(repository.commits) != 0 {
		t.Fatalf("missing relation planner commits=%#v err=%v", repository.commits, err)
	}
}

func TestRecordDeleteErrorEmptyCodeAndBlankParameter(t *testing.T) {
	if apperror.CodeOf(recordDeleteErrorFrom(apperror.KindBadRequest, recordDeleteEmptyCodeError{})) != "backend.bad_request" {
		t.Fatal("empty coded error did not use fallback")
	}
	if appErr := recordDeleteError(apperror.KindBadRequest, "code", nil, " ", "ignored").(*apperror.AppError); appErr.Params != nil {
		t.Fatalf("blank key params=%#v", appErr.Params)
	}
}
