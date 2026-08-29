// Delete application service tests.
package record

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	"context"
	"errors"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"

	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

type deleteRepositoryProbe struct {
	recordrepository.RecordRepository
	records  map[string]recordmodel.Record
	lists    map[string][]recordmodel.Record
	commits  []transactionmodel.RecordMutationCommit
	single   int
	batch    int
	batchErr error
}

func TestDeleteServiceCombinesExpectedVersionWithAtomicDeletePredicate(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	repository := &deleteRepositoryProbe{records: map[string]recordmodel.Record{"customer:customer-1": {ID: "customer-1", UpdatedAt: "version-2", Data: map[string]any{"name": "Acme"}}}}
	service := NewRecordDeleteApplicationService(RecordDeleteDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
	})
	if err := service.DeleteExpected(t.Context(), "customer", "customer-1", "version-1", recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})); apperror.CodeOf(err) != "backend.record.version_conflict" || len(repository.commits) != 0 {
		t.Fatalf("stale delete err=%v commits=%#v", err, repository.commits)
	}
	if err := service.DeleteExpected(t.Context(), "customer", "customer-1", "version-2", recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})); err != nil {
		t.Fatal(err)
	}
	if len(repository.commits) != 1 || repository.commits[0].Optimistic.ExpectedUpdatedAt != "version-2" {
		t.Fatalf("delete commit=%#v", repository.commits)
	}
}

func TestDeleteServiceUsesPersistedRelationScope(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "emergency_contact", Fields: []definitionmodel.FieldSchema{{Key: "member_id", Type: "relation"}}}
	repository := &deleteRepositoryProbe{records: map[string]recordmodel.Record{
		"emergency_contact:contact-1": {ID: "contact-1", Data: map[string]any{"member_id": "member-1"}},
	}}
	persistedScopeCalled := false
	service := NewRecordDeleteApplicationService(RecordDeleteDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return false },
		CanAccessScope: func(_ context.Context, _ principalmodel.Principal, _ definitionmodel.ObjectSchema, record recordmodel.Record, write bool) (bool, error) {
			persistedScopeCalled = record.ID == "contact-1" && !write
			return true, nil
		},
	})
	if err := service.Delete(t.Context(), object.Key, "contact-1", recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})); err != nil {
		t.Fatal(err)
	}
	if !persistedScopeCalled || len(repository.commits) != 1 {
		t.Fatalf("persisted scope called=%v commits=%#v", persistedScopeCalled, repository.commits)
	}
}

func TestPlanDeleteMutationDoesNotCommit(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	repository := &deleteRepositoryProbe{records: map[string]recordmodel.Record{
		"customer:customer-1": {ID: "customer-1", UpdatedAt: "revision-1", Data: map[string]any{"name": "Acme"}},
	}}
	service := NewRecordDeleteApplicationService(RecordDeleteDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
	})
	plans, err := service.PlanDeleteMutation(t.Context(), "customer", "customer-1", "", recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
	if err != nil {
		t.Fatal(err)
	}
	if repository.single != 0 || repository.batch != 0 || len(repository.commits) != 0 {
		t.Fatalf("planning committed: single=%d batch=%d commits=%#v", repository.single, repository.batch, repository.commits)
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %#v", plans)
	}
	commit := plans[0].CanonicalCommit()
	if commit.Operation != "delete" || commit.RecordID != "customer-1" || commit.Optimistic.ExpectedUpdatedAt != "revision-1" {
		t.Fatalf("planned commit = %#v", commit)
	}
}

func (r *deleteRepositoryProbe) GetRecord(_ context.Context, _ string, object definitionmodel.ObjectSchema, recordID string) (recordmodel.Record, bool, error) {
	record, ok := r.records[object.Key+":"+recordID]
	return record, ok, nil
}

func (r *deleteRepositoryProbe) ListRecords(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	items := append([]recordmodel.Record(nil), r.lists[object.Key]...)
	return recordmodel.RecordPageResult{Items: items, Total: len(items)}, nil
}

func (r *deleteRepositoryProbe) CommitRecordMutation(_ context.Context, _ string, commit transactionmodel.RecordMutationCommit) error {
	r.single++
	r.commits = append(r.commits, commit)
	return nil
}

func (r *deleteRepositoryProbe) CommitRecordMutationBatch(_ context.Context, _ string, commits []transactionmodel.RecordMutationCommit) error {
	r.batch++
	if r.batchErr != nil {
		return r.batchErr
	}
	r.commits = append(r.commits, commits...)
	return nil
}

func TestDeleteServiceOwnsSoftDeleteTransaction(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{
		{Key: "status", Type: "text"}, {Key: "deleted_at", Type: "datetime"}, {Key: "deleted_by", Type: "text"}, {Key: "version", Type: "number"}, {Key: "name", Type: "text"},
	}}
	repository := &deleteRepositoryProbe{records: map[string]recordmodel.Record{
		"customer:customer-1": {ID: "customer-1", Data: map[string]any{"status": "active", "deleted_at": "", "deleted_by": "", "version": float64(2), "name": "Acme"}},
	}}
	automationCalled, policyCalled := false, false
	var executed []workflowmodel.WorkflowExecution
	service := NewRecordDeleteApplicationService(RecordDeleteDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		CanWrite:  func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
		RunBefore: func(context.Context, string, string, string, map[string]any, map[string]any, map[string]any, principalmodel.Principal) error {
			automationCalled = true
			return nil
		},
		ValidatePolicies: func(_ context.Context, _ definitionmodel.ObjectSchema, _, _ map[string]any, _ string, operation string, _ principalmodel.Principal) error {
			policyCalled = operation == "delete"
			return nil
		},
		AfterOutbox: func(string, string, map[string]any, recordmodel.Record, principalmodel.Principal) []integrationmodel.IntegrationOutboxMessage {
			return []integrationmodel.IntegrationOutboxMessage{{ID: "outbox-1"}}
		},
		UpdatedTriggers: func(string, map[string]any, map[string]any) []string {
			return []string{"record_updated:customer.status"}
		},
		PrepareWorkflow: func(context.Context, string, recordmodel.Record, map[string]any, principalmodel.Principal, string) ([]workflowmodel.WorkflowExecution, error) {
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

	if err := service.Delete(t.Context(), "customer", "customer-1", recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}})); err != nil {
		t.Fatal(err)
	}
	if len(repository.commits) != 1 {
		t.Fatalf("commits = %#v", repository.commits)
	}
	commit := repository.commits[0]
	if commit.Operation != "update" || !commit.Record.Deleted || commit.Record.UpdateBy != "admin" || commit.Record.Data["status"] != "deleted" || commit.Record.Data["deleted_at"] != "2026-07-17T12:00:00Z" || commit.Record.Data["deleted_by"] != "admin" || commit.Record.Data["version"] != float64(3) {
		t.Fatalf("soft delete commit = %#v", commit)
	}
	if commit.Audit == nil || commit.Audit.Metadata["soft_delete"] != true || len(commit.Outbox) != 1 || len(commit.WorkflowIntents) != 1 || !automationCalled || !policyCalled || len(executed) != 1 {
		t.Fatalf("soft delete evidence commit=%#v automation=%v policy=%v executed=%#v", commit, automationCalled, policyCalled, executed)
	}
}

func TestDeleteServiceOwnsHardDeleteCascadeAndSetNull(t *testing.T) {
	root := definitionmodel.ObjectSchema{Key: "root", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	child := definitionmodel.ObjectSchema{Key: "child", Fields: []definitionmodel.FieldSchema{{Key: "root_id", Type: "relation", Config: map[string]any{"target": "root", "on_delete": "cascade"}}}}
	note := definitionmodel.ObjectSchema{Key: "note", Fields: []definitionmodel.FieldSchema{{Key: "root_id", Type: "relation", Config: map[string]any{"target": "root", "on_delete": "set_null"}}}}
	schema := map[string]definitionmodel.ObjectSchema{"root": root, "child": child, "note": note}
	repository := &deleteRepositoryProbe{
		records: map[string]recordmodel.Record{
			"root:root-1":   {ID: "root-1", Data: map[string]any{"name": "Root"}},
			"child:child-1": {ID: "child-1", Data: map[string]any{"root_id": "root-1"}},
		},
		lists: map[string][]recordmodel.Record{
			"child": {{ID: "child-1", Data: map[string]any{"root_id": "root-1"}}},
			"note":  {{ID: "note-1", Data: map[string]any{"root_id": "root-1"}}},
		},
	}
	automationCalls := 0
	var setNullRecords []string
	var policyObjects []string
	service := NewRecordDeleteApplicationService(RecordDeleteDependencies{
		Repository: repository,
		Relations:  recordservice.NewRecordDeleteRelationDomainService(repository, func() map[string]definitionmodel.ObjectSchema { return schema }),
		ObjectForAction: func(_ principalmodel.Principal, objectKey, _ string) (definitionmodel.ObjectSchema, error) {
			return schema[objectKey], nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		RunBefore: func(context.Context, string, string, string, map[string]any, map[string]any, map[string]any, principalmodel.Principal) error {
			automationCalls++
			return nil
		},
		ValidatePolicies: func(_ context.Context, object definitionmodel.ObjectSchema, _, _ map[string]any, _ string, _ string, _ principalmodel.Principal) error {
			policyObjects = append(policyObjects, object.Key)
			return nil
		},
		PlanUpdateReference: func(_ context.Context, reference recordservice.RecordDeleteReference, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
			setNullRecords = append(setNullRecords, reference.Record.ID)
			updated := reference.Record
			updated.Data = map[string]any{"root_id": nil}
			mutationContext, err := transactionmodel.NewMutationContext(transactionmodel.MutationContextInput{WorkspaceID: principal.WorkspaceID, Source: transactionmodel.MutationSourceHTTP, CorrelationID: "delete-cascade", MetadataRevision: "revision-1"})
			if err != nil {
				return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
			}
			plan, err := transactionmodel.NewMutationPlan(mutationContext, transactionmodel.RecordMutationCommit{Operation: "update", Object: reference.Object, Record: updated}, reference.Record.Data)
			return plan, updated, err
		},
		BuildAudit: func(_ context.Context, event, objectKey, recordID string, _ principalmodel.Principal, summary string, before, after, metadata map[string]any) auditmodel.AuditEvent {
			return auditmodel.AuditEvent{Event: event, ObjectKey: objectKey, RecordID: recordID, Summary: summary, Before: before, After: after, Metadata: metadata}
		},
		Now: func() time.Time { return time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC) },
	})

	if err := service.Delete(t.Context(), "root", "root-1", recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})); err != nil {
		t.Fatal(err)
	}
	if automationCalls != 1 || len(setNullRecords) != 1 || setNullRecords[0] != "note-1" {
		t.Fatalf("automation=%d setNull=%v", automationCalls, setNullRecords)
	}
	if len(repository.commits) != 3 || repository.commits[0].Object.Key != "child" || repository.commits[0].Operation != "delete" || repository.commits[1].Object.Key != "note" || repository.commits[1].Operation != "update" || repository.commits[2].Object.Key != "root" || repository.commits[2].Operation != "delete" {
		t.Fatalf("hard delete commits = %#v", repository.commits)
	}
	if repository.batch != 1 || repository.single != 0 {
		t.Fatalf("cascade delete must use one batch commit: batch=%d single=%d", repository.batch, repository.single)
	}
	if len(policyObjects) != 2 || policyObjects[0] != "child" || policyObjects[1] != "root" {
		t.Fatalf("policy objects = %v", policyObjects)
	}
}

func TestCascadeDeleteBatchFailureLeavesNoPartialCommitOrPostCommitEffect(t *testing.T) {
	root := definitionmodel.ObjectSchema{Key: "root"}
	child := definitionmodel.ObjectSchema{Key: "child", Fields: []definitionmodel.FieldSchema{{Key: "root_id", Type: "relation", Config: map[string]any{"target": "root", "on_delete": "cascade"}}}}
	schema := map[string]definitionmodel.ObjectSchema{"root": root, "child": child}
	failure := errors.New("injected batch failure")
	repository := &deleteRepositoryProbe{
		records:  map[string]recordmodel.Record{"root:root-1": {ID: "root-1", UpdatedAt: "root-v1", Data: map[string]any{}}, "child:child-1": {ID: "child-1", UpdatedAt: "child-v1", Data: map[string]any{"root_id": "root-1"}}},
		lists:    map[string][]recordmodel.Record{"child": {{ID: "child-1", UpdatedAt: "child-v1", Data: map[string]any{"root_id": "root-1"}}}},
		batchErr: failure,
	}
	effects := 0
	service := NewRecordDeleteApplicationService(RecordDeleteDependencies{
		Repository: repository,
		Relations:  recordservice.NewRecordDeleteRelationDomainService(repository, func() map[string]definitionmodel.ObjectSchema { return schema }),
		ObjectForAction: func(_ principalmodel.Principal, objectKey, _ string) (definitionmodel.ObjectSchema, error) {
			return schema[objectKey], nil
		},
		CanAccess:       func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		ExecuteWorkflow: func(context.Context, []workflowmodel.WorkflowExecution, principalmodel.Principal) { effects++ },
	})
	err := service.Delete(t.Context(), "root", "root-1", recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
	if !errors.Is(err, failure) || repository.batch != 1 || repository.single != 0 || len(repository.commits) != 0 || effects != 0 {
		t.Fatalf("err=%v batch=%d single=%d commits=%d effects=%d", err, repository.batch, repository.single, len(repository.commits), effects)
	}
}
