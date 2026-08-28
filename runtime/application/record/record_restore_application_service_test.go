// Restore application service tests.
package record

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	"context"
	"errors"
	"testing"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type restoreRepositoryProbe struct {
	recordrepository.RecordRepository
	record recordmodel.Record
	found  bool
	err    error
	commit transactionmodel.RecordMutationCommit
}

func (r *restoreRepositoryProbe) GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	return r.record, r.found, r.err
}

func (r *restoreRepositoryProbe) CommitRecordMutation(_ context.Context, _ string, commit transactionmodel.RecordMutationCommit) error {
	if r.err != nil {
		return r.err
	}
	r.commit = commit
	return nil
}

func TestRestoreServiceOwnsCompleteRestoreTransaction(t *testing.T) {
	object := restoreTestObject()
	repository := &restoreRepositoryProbe{found: true, record: recordmodel.Record{
		ID: "customer-1", UpdatedAt: "before", Data: map[string]any{
			"status": "deleted", "deleted_at": "2026-07-01", "deleted_by": "u0", "version": "2", "name": "Acme",
		},
	}}
	var preparedTriggers []string
	var executed []workflowmodel.WorkflowExecution
	service := NewRecordRestoreApplicationService(RecordRestoreDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		CanWrite:  func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
		ValidateRelations: func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) error {
			return nil
		},
		ValidatePolicies: func(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, string, principalmodel.Principal) error {
			return nil
		},
		ValidateUnique: func(context.Context, string, string, definitionmodel.ObjectSchema, string, map[string]any) error {
			return nil
		},
		ValidateDuplicate: func(context.Context, string, definitionmodel.ObjectSchema, string, map[string]any) error { return nil },
		UpdatedTriggers: func(string, map[string]any, map[string]any) []string {
			return []string{"record_updated:customer.status"}
		},
		PrepareWorkflow: func(_ context.Context, _ string, _ recordmodel.Record, _ map[string]any, _ principalmodel.Principal, trigger string) ([]workflowmodel.WorkflowExecution, error) {
			preparedTriggers = append(preparedTriggers, trigger)
			return []workflowmodel.WorkflowExecution{{ID: "workflow-1"}}, nil
		},
		ExecuteWorkflow: func(_ context.Context, intents []workflowmodel.WorkflowExecution, _ principalmodel.Principal) {
			executed = append(executed, intents...)
		},
		BuildAudit: func(_ context.Context, event, objectKey, recordID string, _ principalmodel.Principal, summary string, before, after, metadata map[string]any) auditmodel.AuditEvent {
			return auditmodel.AuditEvent{Event: event, ObjectKey: objectKey, RecordID: recordID, Summary: summary, Before: before, After: after, Metadata: metadata}
		},
	})

	restored, err := service.Restore(t.Context(), "customer", "customer-1", recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}))
	if err != nil {
		t.Fatal(err)
	}
	if restored.Data["status"] != "active" || restored.Data["deleted_at"] != "" || restored.Data["deleted_by"] != "" || restored.Data["version"] != float64(3) {
		t.Fatalf("restored data = %#v", restored.Data)
	}
	if repository.commit.Operation != "restore" || repository.commit.Optimistic.ExpectedUpdatedAt != "before" || repository.commit.Audit == nil || repository.commit.Audit.Event != "record_restored" {
		t.Fatalf("restore commit = %#v", repository.commit)
	}
	if len(repository.commit.WorkflowIntents) != 1 || len(preparedTriggers) != 1 || preparedTriggers[0] != "record_updated:customer.status" || len(executed) != 1 {
		t.Fatalf("restore effects: triggers=%v commit=%#v executed=%#v", preparedTriggers, repository.commit.WorkflowIntents, executed)
	}
}

func TestPlanRestoreMutationUsesObservedRevisionWithoutCommit(t *testing.T) {
	object := restoreTestObject()
	repository := &restoreRepositoryProbe{found: true, record: recordmodel.Record{
		ID: "customer-1", UpdatedAt: "revision-1", Data: map[string]any{
			"status": "deleted", "deleted_at": "2026-07-01", "deleted_by": "u0", "version": "2", "name": "Acme",
		},
	}}
	service := NewRecordRestoreApplicationService(RecordRestoreDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		CanWrite:  func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
	})
	plan, restored, err := service.PlanRestoreMutation(t.Context(), "customer", "customer-1", "", recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
	if err != nil {
		t.Fatal(err)
	}
	if repository.commit.Operation != "" {
		t.Fatalf("planning committed = %#v", repository.commit)
	}
	commit := plan.CanonicalCommit()
	if commit.Operation != "restore" || commit.Optimistic.ExpectedUpdatedAt != "revision-1" || restored.Data["status"] != "active" {
		t.Fatalf("planned commit=%#v restored=%#v", commit, restored)
	}
	if _, _, err := service.PlanRestoreMutation(t.Context(), "customer", "customer-1", "stale", recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})); apperror.CodeOf(err) != "backend.record.version_conflict" {
		t.Fatalf("stale revision err = %v", err)
	}
}

func TestRestoreServicePreservesStructuredFailures(t *testing.T) {
	t.Run("unsupported", func(t *testing.T) {
		service := NewRecordRestoreApplicationService(RecordRestoreDependencies{ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return definitionmodel.ObjectSchema{Key: "customer"}, nil
		}})
		_, err := service.Restore(t.Context(), "customer", "customer-1", recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
		assertRecordApplicationError(t, err, apperror.KindBadRequest, "backend.record.restore_unsupported", nil)
	})

	t.Run("outside scope", func(t *testing.T) {
		repository := &restoreRepositoryProbe{found: true, record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"status": "deleted"}}}
		service := NewRecordRestoreApplicationService(RecordRestoreDependencies{
			Repository: repository,
			ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
				return restoreTestObject(), nil
			},
			CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return false },
		})
		_, err := service.Restore(t.Context(), "customer", "customer-1", recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
		assertRecordApplicationError(t, err, apperror.KindForbidden, "backend.record.outside_scope", nil)
	})

	t.Run("repository", func(t *testing.T) {
		repository := &restoreRepositoryProbe{err: errors.New("store unavailable")}
		service := NewRecordRestoreApplicationService(RecordRestoreDependencies{
			Repository: repository,
			ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
				return restoreTestObject(), nil
			},
		})
		_, err := service.Restore(t.Context(), "customer", "customer-1", recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
		assertRecordApplicationError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "get record"})
	})
}

func assertRecordApplicationError(t *testing.T, err error, kind apperror.ErrorKind, code string, params map[string]string) {
	t.Helper()
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error = %T %v, want *apperror.AppError", err, err)
	}
	if appErr.Kind != kind || appErr.Code != code {
		t.Fatalf("error = %#v, want kind=%s code=%s", appErr, kind, code)
	}
	for key, want := range params {
		if got := appErr.Params[key]; got != want {
			t.Fatalf("error param %s = %q, want %q", key, got, want)
		}
	}
}

func restoreTestObject() definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{
		{Key: "status", Type: "text"},
		{Key: "deleted_at", Type: "text"},
		{Key: "deleted_by", Type: "text"},
		{Key: "version", Type: "number"},
		{Key: "name", Type: "text"},
	}}
}
