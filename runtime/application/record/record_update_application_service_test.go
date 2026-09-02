// Update domain service tests.
package record

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordruntime "github.com/domainry/domainry-runtime/runtime/domain/record/runtime"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	"context"
	"errors"
	"fmt"
	"strings"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"

	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
)

type updateRepositoryProbe struct {
	recordrepository.RecordRepository
	record     recordmodel.Record
	found      bool
	err        error
	commitErr  error
	commitHook func()
	commit     transactionmodel.RecordMutationCommit
	getCalls   int
}

func (r *updateRepositoryProbe) GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	r.getCalls++
	return r.record, r.found, r.err
}

func TestRecordApplicationAuthorizesWorkspaceBeforeRepositoryAccess(t *testing.T) {
	repository := &updateRepositoryProbe{}
	objectCalls := 0
	service := NewRecordUpdateApplicationService(RecordUpdateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			objectCalls++
			return definitionmodel.ObjectSchema{}, nil
		},
	})
	if _, err := service.Update(t.Context(), "case", "case-1", map[string]any{}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}); err == nil || apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("missing workspace not rejected: %v", err)
	}
	if repository.getCalls != 0 || objectCalls != 0 {
		t.Fatalf("record ports called before workspace authorization: repository=%d object=%d", repository.getCalls, objectCalls)
	}
}

func (r *updateRepositoryProbe) CommitRecordMutation(_ context.Context, _ string, commit transactionmodel.RecordMutationCommit) error {
	if r.commitHook != nil {
		r.commitHook()
	}
	if r.err != nil {
		return r.err
	}
	if r.commitErr != nil {
		return r.commitErr
	}
	r.commit = commit
	return nil
}

func TestUpdateServiceOwnsCompleteUpdateTransaction(t *testing.T) {
	repository := &updateRepositoryProbe{found: true, record: recordmodel.Record{ID: "case-1", OwnerUserID: "original-user", OwnerOrgID: "original-org", UpdatedAt: "version-1", Data: map[string]any{"status": "open", "name": "Before", "version": 7}}}
	object := definitionmodel.ObjectSchema{Key: "case", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}, {Key: "name", Type: "text"}, {Key: "version", Type: "number"}}}
	var beforeOperations []string
	var outboxOperations []string
	var preparedTriggers []string
	var executed []workflowmodel.WorkflowExecution
	relationCount, writeCount := 0, 0
	selfEffects := false
	service := NewRecordUpdateApplicationService(RecordUpdateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		CanWrite: func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool {
			writeCount++
			return true
		},
		ValidatePipeline: func(context.Context, definitionmodel.ObjectSchema, string, map[string]any, principalmodel.Principal) error {
			return nil
		},
		ApplyPipelineDefaults: func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal, bool) error {
			return nil
		},
		RunBefore: func(_ context.Context, _, operation, _ string, _, _, _ map[string]any, _ principalmodel.Principal) error {
			beforeOperations = append(beforeOperations, operation)
			return nil
		},
		ValidateRelations: func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) error {
			relationCount++
			return nil
		},
		ValidatePolicies: func(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, string, principalmodel.Principal) error {
			return nil
		},
		ApplySelfEffects: func(_ context.Context, _ definitionmodel.ObjectSchema, _, next map[string]any, _ string, _ principalmodel.Principal) (bool, error) {
			selfEffects = true
			next["name"] = "Self effect"
			return true, nil
		},
		ValidateUnique: func(context.Context, string, string, definitionmodel.ObjectSchema, string, map[string]any) error {
			return nil
		},
		ValidateDuplicate: func(context.Context, string, definitionmodel.ObjectSchema, string, map[string]any) error { return nil },
		AfterOutbox: func(_ string, operation string, _ map[string]any, _ recordmodel.Record, _ principalmodel.Principal) []publicationmodel.Message {
			outboxOperations = append(outboxOperations, operation)
			return []publicationmodel.Message{{ID: "outbox-" + operation}}
		},
		UpdatedTriggers: func(string, map[string]any, map[string]any) []string { return []string{"record_updated:case.status"} },
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
		Now: func() time.Time { return time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC) },
	})

	updated, err := service.Update(t.Context(), "case", "case-1", map[string]any{"status": "closed", "expected_updated_at": "version-1", "expected_version": 7}, recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Data["status"] != "closed" || updated.Data["name"] != "Self effect" || updated.UpdatedAt != "2026-07-17T12:00:00Z" {
		t.Fatalf("updated = %#v", updated)
	}
	if updated.OwnerUserID != "original-user" || updated.OwnerOrgID != "original-org" || repository.commit.Record.OwnerUserID != "original-user" || repository.commit.Record.OwnerOrgID != "original-org" {
		t.Fatalf("ordinary update changed stable ownership: updated=%#v commit=%#v", updated, repository.commit.Record)
	}
	if len(beforeOperations) != 2 || beforeOperations[0] != "update" || beforeOperations[1] != "transition" || len(outboxOperations) != 2 || outboxOperations[0] != "update" || outboxOperations[1] != "transition" {
		t.Fatalf("automation before=%v outbox=%v", beforeOperations, outboxOperations)
	}
	if repository.commit.Operation != "update" || repository.commit.Optimistic.ExpectedUpdatedAt != "version-1" || repository.commit.Optimistic.ExpectedVersion == nil || *repository.commit.Optimistic.ExpectedVersion != 7 || repository.commit.Audit == nil || repository.commit.Audit.Event != "record_updated" || len(repository.commit.Outbox) != 2 || len(repository.commit.WorkflowIntents) != 1 {
		t.Fatalf("commit = %#v", repository.commit)
	}
	if relationCount != 2 || writeCount != 2 || !selfEffects || len(preparedTriggers) != 1 || len(executed) != 1 {
		t.Fatalf("effects relation=%d write=%d self=%v triggers=%v executed=%v", relationCount, writeCount, selfEffects, preparedTriggers, executed)
	}
}

func TestUpdateIdempotentCommitsThroughReceiptAndReplaysWithoutSecondMutation(t *testing.T) {
	repository := &updateRepositoryProbe{
		found:  true,
		record: recordmodel.Record{ID: "member-1", UpdatedAt: "version-1", Data: map[string]any{"status": "active"}},
	}
	executions := &createExecutionProbe{}
	object := definitionmodel.ObjectSchema{Key: "member_profile", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}
	service := NewRecordUpdateApplicationService(RecordUpdateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanAccess:        func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		CanWrite:         func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
		ExecutionRuntime: recordruntime.NewRecordMutationExecutionRuntime(executions),
		BuildAudit: func(_ context.Context, event, objectKey, recordID string, _ principalmodel.Principal, summary string, before, after, metadata map[string]any) auditmodel.AuditEvent {
			return auditmodel.AuditEvent{Event: event, ObjectKey: objectKey, RecordID: recordID, Summary: summary, Before: before, After: after, Metadata: metadata}
		},
		Now: func() time.Time { return time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC) },
	})
	patch := map[string]any{"status": "blacklisted", "expected_updated_at": "version-1"}
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}, RequestID: "request-1"})
	updated, err := service.UpdateIdempotent(t.Context(), "member_profile", "member-1", patch, "blacklist-1", principal)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Data["status"] != "blacklisted" ||
		executions.commitCalls != 1 ||
		executions.commit.Operation != "update" ||
		executions.execution.Operation != "update" ||
		executions.execution.TargetID != "member-1" ||
		repository.commit.Operation != "" {
		t.Fatalf("updated=%#v execution=%#v execution commit=%#v repository commit=%#v", updated, executions.execution, executions.commit, repository.commit)
	}
	if executions.commit.Audit == nil || executions.commit.Audit.Event != "record_updated" ||
		executions.commit.Audit.Metadata["idempotency_scope"] != "record.update" ||
		executions.commit.Audit.Metadata["idempotency_status"] != "succeeded" ||
		!strings.HasPrefix(fmt.Sprint(executions.commit.Audit.Metadata["idempotency_key_hash"]), "sha256:") ||
		strings.Contains(fmt.Sprint(executions.commit.Audit.Metadata), "blacklist-1") {
		t.Fatalf("update audit lacks safe receipt correlation: %#v", executions.commit.Audit)
	}
	if patch["expected_updated_at"] != "version-1" {
		t.Fatalf("caller patch mutated: %#v", patch)
	}

	replayed, err := service.UpdateIdempotent(t.Context(), "member_profile", "member-1", patch, "blacklist-1", principal)
	if err != nil || replayed.Data["status"] != "blacklisted" || executions.commitCalls != 1 || repository.getCalls != 1 {
		t.Fatalf("replayed=%#v commits=%d gets=%d err=%v", replayed, executions.commitCalls, repository.getCalls, err)
	}
	_, err = service.UpdateIdempotent(t.Context(), "member_profile", "member-1", map[string]any{"status": "active"}, "blacklist-1", principal)
	if apperror.CodeOf(err) != idempotency.ErrorCodeKeyReused || executions.commitCalls != 1 {
		t.Fatalf("changed-payload replay err=%v commits=%d", err, executions.commitCalls)
	}
}

func TestUpdateIdempotentRequiresExecutionRuntime(t *testing.T) {
	repository := &updateRepositoryProbe{found: true, record: recordmodel.Record{ID: "member-1", UpdatedAt: "v1", Data: map[string]any{"status": "active"}}}
	service := NewRecordUpdateApplicationService(RecordUpdateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return definitionmodel.ObjectSchema{Key: "member_profile"}, nil
		},
	})
	_, err := service.UpdateIdempotent(t.Context(), "member_profile", "member-1", map[string]any{"status": "blacklisted"}, "blacklist-1", principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	if apperror.CodeOf(err) != idempotency.ErrorCodeReceiptUnavailable || repository.getCalls != 0 {
		t.Fatalf("err=%v gets=%d", err, repository.getCalls)
	}
}

func TestUpdateServiceRejectsVersionConflictWithoutMutatingCallerPatch(t *testing.T) {
	repository := &updateRepositoryProbe{found: true, record: recordmodel.Record{ID: "case-1", UpdatedAt: "version-2", Data: map[string]any{"name": "Before"}}}
	service := NewRecordUpdateApplicationService(RecordUpdateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return definitionmodel.ObjectSchema{Key: "case", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}, nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
	})
	patch := map[string]any{"name": "After", "expected_updated_at": "version-1"}

	_, err := service.Update(t.Context(), "case", "case-1", patch, recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
	assertRecordUpdateApplicationError(t, err, apperror.KindConflict, "backend.record.version_conflict", map[string]string{"expected": "version-1", "actual": "version-2"})
	if patch["expected_updated_at"] != "version-1" || repository.commit.Operation != "" {
		t.Fatalf("patch=%#v commit=%#v", patch, repository.commit)
	}
}

func TestUpdateServiceAlwaysCarriesReadRevisionIntoStorageCAS(t *testing.T) {
	repository := &updateRepositoryProbe{found: true, record: recordmodel.Record{ID: "case-1", UpdatedAt: "revision-9", Data: map[string]any{"name": "Before"}}}
	service := NewRecordUpdateApplicationService(RecordUpdateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return definitionmodel.ObjectSchema{Key: "case", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}, nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		CanWrite:  func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
	})
	if _, err := service.Update(t.Context(), "case", "case-1", map[string]any{"name": "After"}, recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})); err != nil {
		t.Fatal(err)
	}
	if repository.commit.Optimistic.ExpectedUpdatedAt != "revision-9" {
		t.Fatalf("optimistic precondition=%+v", repository.commit.Optimistic)
	}
}

func TestUpdateServiceMapsStorageCASFailureToStableVersionConflict(t *testing.T) {
	repository := &updateRepositoryProbe{found: true, record: recordmodel.Record{ID: "case-1", UpdatedAt: "revision-9", Data: map[string]any{"name": "Before"}}, commitErr: mutation.MutationConflict("case", "case-1", mutation.MutationConflictOptimistic, nil)}
	service := NewRecordUpdateApplicationService(RecordUpdateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return definitionmodel.ObjectSchema{Key: "case", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}, nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		CanWrite:  func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
	})
	_, err := service.Update(t.Context(), "case", "case-1", map[string]any{"name": "After"}, recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
	assertRecordUpdateApplicationError(t, err, apperror.KindConflict, "backend.record.version_conflict", nil)
}

func TestUpdateServiceMapsIdempotencyCommitFailureToStableConflict(t *testing.T) {
	repository := &updateRepositoryProbe{
		found:     true,
		record:    recordmodel.Record{ID: "case-1", UpdatedAt: "revision-9", Data: map[string]any{"name": "Before"}},
		commitErr: mutation.MutationConflict("workflow_execution", "workflow-1", mutation.MutationConflictIdempotency, errors.New("Error 1062: Duplicate entry")),
	}
	service := NewRecordUpdateApplicationService(RecordUpdateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return definitionmodel.ObjectSchema{Key: "case", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}, nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		CanWrite:  func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
	})

	_, err := service.Update(t.Context(), "case", "case-1", map[string]any{"name": "After"}, recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
	assertRecordUpdateApplicationError(t, err, apperror.KindConflict, "backend.mutation.idempotency_conflict", map[string]string{"resource": "workflow_execution", "identifier": "workflow-1"})
	if strings.Contains(strings.ToLower(err.Error()), "duplicate entry") {
		t.Fatalf("application error leaked dialect detail: %v", err)
	}
}

func TestUpdateServiceRejectsReadOnlyFieldAndAuditsDenial(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "employee_profile", Fields: []definitionmodel.FieldSchema{{Key: "identity_user", Type: "relation"}}}
	repository := &updateRepositoryProbe{found: true, record: recordmodel.Record{ID: "profile-1", Data: map[string]any{"identity_user": "u1"}}}
	deniedReason := ""
	service := NewRecordUpdateApplicationService(RecordUpdateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		CanWrite:  func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
		Denied: func(_ context.Context, _, _ string, _ principalmodel.Principal, _ error, reason string, _ map[string]any) {
			deniedReason = reason
		},
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "employee_profile", FieldKey: "identity_user", Read: true, Write: false}}})
	_, err := service.Update(t.Context(), "employee_profile", "profile-1", map[string]any{"identity_user": "u2"}, principal)
	assertRecordUpdateApplicationError(t, err, apperror.KindForbidden, "backend.validation.field_not_writable", map[string]string{"field": "identity_user", "role": ""})
	if deniedReason != "field_permission" || repository.commit.Operation != "" {
		t.Fatalf("deniedReason=%q commit=%#v", deniedReason, repository.commit)
	}
}

func TestUpdateServiceAuditsScopeDenialAndWrapsCommitFailure(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "case", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	repository := &updateRepositoryProbe{found: true, record: recordmodel.Record{ID: "case-1", Data: map[string]any{"name": "Before"}}}
	deniedReason := ""
	service := NewRecordUpdateApplicationService(RecordUpdateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return false },
		Denied: func(_ context.Context, _, _ string, _ principalmodel.Principal, _ error, reason string, _ map[string]any) {
			deniedReason = reason
		},
	})
	_, err := service.Update(t.Context(), "case", "case-1", map[string]any{"name": "After"}, recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
	assertRecordUpdateApplicationError(t, err, apperror.KindForbidden, "backend.record.outside_scope", nil)
	if deniedReason != "data_scope" {
		t.Fatalf("denied reason = %q", deniedReason)
	}

	repository.err = errors.New("store unavailable")
	service = NewRecordUpdateApplicationService(RecordUpdateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		CanWrite:  func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
	})
	_, err = service.Update(t.Context(), "case", "case-1", map[string]any{"name": "After"}, recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
	assertRecordUpdateApplicationError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "get record"})
}

func assertRecordUpdateApplicationError(t *testing.T, err error, kind apperror.ErrorKind, code string, params map[string]string) {
	t.Helper()
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Kind != kind || appErr.Code != code {
		t.Fatalf("error=%#v, want kind=%s code=%s", err, kind, code)
	}
	for key, value := range params {
		if appErr.Params[key] != value {
			t.Fatalf("error params=%#v, want %s=%s", appErr.Params, key, value)
		}
	}
}
