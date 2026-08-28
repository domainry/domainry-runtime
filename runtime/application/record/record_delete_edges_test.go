package record

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type deleteEdgeRepository struct {
	recordrepository.RecordRepository
	record    recordmodel.Record
	found     bool
	getErr    error
	commitErr error
	commits   []transactionmodel.RecordMutationCommit
	onCommit  func()
	listErr   error
	lists     map[string][]recordmodel.Record
	onList    func()
}

func (r *deleteEdgeRepository) GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	return r.record, r.found, r.getErr
}

func (r *deleteEdgeRepository) CommitRecordMutation(_ context.Context, _ string, commit transactionmodel.RecordMutationCommit) error {
	r.commits = append(r.commits, commit)
	if r.onCommit != nil {
		r.onCommit()
	}
	return r.commitErr
}

func (r *deleteEdgeRepository) ListRecords(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	if r.onList != nil {
		r.onList()
	}
	if r.listErr != nil {
		return recordmodel.RecordPageResult{}, r.listErr
	}
	items := append([]recordmodel.Record(nil), r.lists[object.Key]...)
	return recordmodel.RecordPageResult{Items: items, Total: len(items)}, nil
}

func recordDeleteEdgeDependencies(repository recordrepository.RecordRepository, object definitionmodel.ObjectSchema) RecordDeleteDependencies {
	return RecordDeleteDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		CanWrite:  func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
	}
}

func TestDeletePreconditionFailuresStopBeforeCommit(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	for _, stage := range []string{"cancelled", "object", "repository", "not found", "outside scope", "before", "cancelled after before"} {
		t.Run(stage, func(t *testing.T) {
			failure := errors.New(stage + " failed")
			repository := &deleteEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"name": "Acme"}}}
			dependencies := recordDeleteEdgeDependencies(repository, object)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			wantCode := ""
			switch stage {
			case "cancelled":
				cancel()
			case "object":
				dependencies.ObjectForAction = func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
					return definitionmodel.ObjectSchema{}, failure
				}
			case "repository":
				repository.getErr = failure
				wantCode = "backend.internal"
			case "not found":
				repository.found = false
				wantCode = "backend.record.not_found"
			case "outside scope":
				dependencies.CanAccess = func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return false }
				wantCode = "backend.record.outside_scope"
			case "before":
				dependencies.RunBefore = func(context.Context, string, string, string, map[string]any, map[string]any, map[string]any, principalmodel.Principal) error {
					return failure
				}
			case "cancelled after before":
				dependencies.RunBefore = func(context.Context, string, string, string, map[string]any, map[string]any, map[string]any, principalmodel.Principal) error {
					cancel()
					return nil
				}
			}

			err := NewRecordDeleteApplicationService(dependencies).Delete(ctx, object.Key, "customer-1", principal)
			if wantCode != "" {
				if apperror.CodeOf(err) != wantCode {
					t.Fatalf("err=%v code=%q", err, apperror.CodeOf(err))
				}
			} else if stage == "cancelled" || stage == "cancelled after before" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("err=%v", err)
				}
			} else if !errors.Is(err, failure) {
				t.Fatalf("err=%v want=%v", err, failure)
			}
			if len(repository.commits) != 0 {
				t.Fatalf("commits=%#v", repository.commits)
			}
		})
	}
}

func TestSoftDeleteFailureEdges(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}, {Key: "deleted_at", Type: "datetime"}, {Key: "deleted_by", Type: "text"}, {Key: "version", Type: "number"}}}
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	for _, stage := range []string{"already deleted", "write scope", "policy", "workflow", "commit", "cancelled after commit"} {
		t.Run(stage, func(t *testing.T) {
			failure := errors.New(stage + " failed")
			repository := &deleteEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"status": "active", "deleted_at": "", "deleted_by": "", "version": "invalid"}}}
			dependencies := recordDeleteEdgeDependencies(repository, object)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			wantCode := ""
			switch stage {
			case "already deleted":
				repository.record.Data["status"] = "deleted"
				wantCode = "backend.record.already_deleted"
			case "write scope":
				dependencies.CanWrite = func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return false }
				wantCode = "backend.record.owner_write_denied"
			case "policy":
				dependencies.ValidatePolicies = func(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, string, principalmodel.Principal) error {
					return failure
				}
			case "workflow":
				dependencies.UpdatedTriggers = func(string, map[string]any, map[string]any) []string {
					return []string{"record_updated:customer.status"}
				}
				dependencies.PrepareWorkflow = func(context.Context, string, recordmodel.Record, map[string]any, principalmodel.Principal, string) ([]workflowmodel.WorkflowExecution, error) {
					return nil, failure
				}
			case "commit":
				repository.commitErr = failure
				wantCode = "backend.internal"
			case "cancelled after commit":
				repository.onCommit = cancel
			}

			err := NewRecordDeleteApplicationService(dependencies).Delete(ctx, object.Key, "customer-1", principal)
			if wantCode != "" {
				if apperror.CodeOf(err) != wantCode {
					t.Fatalf("err=%v code=%q", err, apperror.CodeOf(err))
				}
			} else if stage == "cancelled after commit" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("err=%v", err)
				}
			} else if !errors.Is(err, failure) {
				t.Fatalf("err=%v want=%v", err, failure)
			}
		})
	}
}

func TestHardDeletePolicyAndCommitFailures(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	for _, stage := range []string{"policy", "commit"} {
		t.Run(stage, func(t *testing.T) {
			failure := errors.New(stage + " failed")
			repository := &deleteEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"name": "Acme"}}}
			dependencies := recordDeleteEdgeDependencies(repository, object)
			if stage == "policy" {
				dependencies.ValidatePolicies = func(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, string, principalmodel.Principal) error {
					return failure
				}
			} else {
				repository.commitErr = failure
			}
			err := NewRecordDeleteApplicationService(dependencies).Delete(t.Context(), object.Key, "customer-1", principal)
			if stage == "commit" {
				if apperror.CodeOf(err) != "backend.internal" {
					t.Fatalf("err=%v code=%q", err, apperror.CodeOf(err))
				}
			} else if !errors.Is(err, failure) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
