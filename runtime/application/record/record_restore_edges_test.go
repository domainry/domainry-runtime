package record

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
)

type restoreEdgeRepository struct {
	recordrepository.RecordRepository
	record    recordmodel.Record
	found     bool
	getErr    error
	commitErr error
	commit    transactionmodel.RecordMutationCommit
}

func (r *restoreEdgeRepository) GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	return r.record, r.found, r.getErr
}

func (r *restoreEdgeRepository) CommitRecordMutation(_ context.Context, _ string, commit transactionmodel.RecordMutationCommit) error {
	r.commit = commit
	return r.commitErr
}

func recordRestoreEdgeDependencies(repository *restoreEdgeRepository) RecordRestoreDependencies {
	return RecordRestoreDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return restoreTestObject(), nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		CanWrite:  func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
	}
}

func TestRestoreDependencyFailuresAndStateGuards(t *testing.T) {
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	for _, stage := range []string{"object", "not found", "not deleted", "relations", "write scope", "policies", "unique", "duplicate", "workflow", "commit"} {
		t.Run(stage, func(t *testing.T) {
			failure := errors.New(stage + " failed")
			repository := &restoreEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{
				"status": "deleted", "deleted_at": "before", "deleted_by": "user-1", "version": "invalid", "name": "Acme",
			}}}
			dependencies := recordRestoreEdgeDependencies(repository)
			wantCode := ""
			switch stage {
			case "object":
				dependencies.ObjectForAction = func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
					return definitionmodel.ObjectSchema{}, failure
				}
			case "not found":
				repository.found = false
				wantCode = "backend.record.not_found"
			case "not deleted":
				repository.record.Data["status"] = "active"
				wantCode = "backend.record.not_deleted"
			case "relations":
				dependencies.ValidateRelations = func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) error {
					return failure
				}
			case "write scope":
				dependencies.CanWrite = func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return false }
				wantCode = "backend.record.owner_write_denied"
			case "policies":
				dependencies.ValidatePolicies = func(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, string, principalmodel.Principal) error {
					return failure
				}
			case "unique":
				dependencies.ValidateUnique = func(context.Context, string, string, definitionmodel.ObjectSchema, string, map[string]any) error {
					return failure
				}
			case "duplicate":
				dependencies.ValidateDuplicate = func(context.Context, string, definitionmodel.ObjectSchema, string, map[string]any) error {
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
			}

			restored, err := NewRecordRestoreApplicationService(dependencies).Restore(t.Context(), "customer", "customer-1", principal)
			if wantCode != "" {
				if apperror.CodeOf(err) != wantCode {
					t.Fatalf("restored=%#v err=%v code=%q", restored, err, apperror.CodeOf(err))
				}
			} else if !errors.Is(err, failure) {
				t.Fatalf("restored=%#v err=%v want=%v", restored, err, failure)
			}
			committedBeforeFailure := stage == "commit"
			if (repository.commit.Operation != "") != committedBeforeFailure {
				t.Fatalf("commit=%#v committedBeforeFailure=%v", repository.commit, committedBeforeFailure)
			}
		})
	}
}

func TestRestoreInvalidStoredVersionFallsBackAndIncrements(t *testing.T) {
	repository := &restoreEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{
		"status": "deleted", "deleted_at": "before", "deleted_by": "user-1", "version": "invalid", "name": "Acme",
	}}}
	restored, err := NewRecordRestoreApplicationService(recordRestoreEdgeDependencies(repository)).Restore(t.Context(), "customer", "customer-1", recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
	if err != nil || restored.Data["version"] != float64(2) {
		t.Fatalf("restored=%#v err=%v", restored, err)
	}
}

func TestPlanRestoreWithMatchingExplicitRevision(t *testing.T) {
	repository := &restoreEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", UpdatedAt: "revision-1", Data: map[string]any{
		"status": "deleted", "deleted_at": "before", "deleted_by": "user-1", "version": float64(1), "name": "Acme",
	}}}
	plan, _, err := NewRecordRestoreApplicationService(recordRestoreEdgeDependencies(repository)).PlanRestoreMutation(t.Context(), "customer", "customer-1", "revision-1", recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
	if err != nil || plan.CanonicalCommit().Optimistic.ExpectedUpdatedAt != "revision-1" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
}

func TestRestoreBusinessConflictAndPlannerLifecycleFailures(t *testing.T) {
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	repository := &restoreEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{
		"status": "deleted", "deleted_at": "before", "deleted_by": "user", "version": float64(1), "name": "Acme",
	}}, commitErr: mutation.BusinessConflict("backend.restore.conflict", "customer", "customer-1", "status")}
	if _, err := NewRecordRestoreApplicationService(recordRestoreEdgeDependencies(repository)).Restore(t.Context(), "customer", "customer-1", principal); apperror.CodeOf(err) != "backend.restore.conflict" {
		t.Fatalf("business conflict err=%v", err)
	}
	immutable := restoreTestObject()
	immutable.LifecyclePolicy = &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleImmutableAfterState, StateField: "status", ImmutableStates: []string{"deleted"}}
	repository = &restoreEdgeRepository{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{
		"status": "deleted", "deleted_at": "before", "deleted_by": "user", "version": float64(1), "name": "Acme",
	}}}
	dependencies := recordRestoreEdgeDependencies(repository)
	dependencies.ObjectForAction = func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
		return immutable, nil
	}
	if _, _, err := NewRecordRestoreApplicationService(dependencies).PlanRestoreMutation(t.Context(), "customer", "customer-1", "", principal); err == nil {
		t.Fatal("immutable restore plan accepted")
	}
}
