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

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"

	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
)

type createRepositoryProbe struct {
	recordrepository.RecordRepository
	commit   transactionmodel.RecordMutationCommit
	err      error
	onCommit func()
}

type createExecutionProbe struct {
	execution   recordmodel.RecordMutationExecution
	commit      transactionmodel.RecordMutationCommit
	commitCalls int
	beginCalls  int
	beginErr    error
	commitErr   error
}

func (p *createExecutionProbe) TryBeginRecordMutation(_ context.Context, request recordmodel.RecordMutationClaimRequest) (recordmodel.RecordMutationClaimResult, error) {
	p.beginCalls++
	if p.beginErr != nil {
		return recordmodel.RecordMutationClaimResult{}, p.beginErr
	}
	if p.execution.ID != "" {
		decision := idempotency.DecisionReplay
		if p.execution.RequestFingerprint != request.RequestFingerprint {
			decision = idempotency.DecisionFingerprintConflict
		}
		return recordmodel.RecordMutationClaimResult{Decision: decision, Execution: p.execution}, nil
	}
	p.execution = request.Execution
	p.execution.ID, p.execution.RequestFingerprint = "execution-1", request.RequestFingerprint
	p.execution.Status, p.execution.LeaseOwner, p.execution.FencingToken = string(idempotency.StatusProcessing), request.LeaseOwner, 1
	return recordmodel.RecordMutationClaimResult{Decision: idempotency.DecisionAcquired, Execution: p.execution}, nil
}

func TestCreateReplayRechecksPermissionBeforeReadingReceipt(t *testing.T) {
	executions := &createExecutionProbe{execution: recordmodel.RecordMutationExecution{ID: "existing-receipt", Status: string(idempotency.StatusSucceeded)}}
	service := NewRecordCreateApplicationService(RecordCreateDependencies{
		Repository: &createRepositoryProbe{},
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return definitionmodel.ObjectSchema{}, apperror.New(apperror.KindForbidden, "backend.record.permission_denied", nil, nil)
		},
		ExecutionRuntime: recordruntime.NewRecordMutationExecutionRuntime(executions),
	})
	if _, err := service.CreateIdempotent(t.Context(), "customer", map[string]any{"name": "Acme"}, "existing-key", principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}); apperror.CodeOf(err) != "backend.record.permission_denied" || executions.beginCalls != 0 {
		t.Fatalf("permission replay error=%v receipt reads=%d", err, executions.beginCalls)
	}
}

func TestCreateLocalizedRecordCarriesTranslationsInCanonicalCommit(t *testing.T) {
	repository := &createRepositoryProbe{}
	object := definitionmodel.ObjectSchema{Key: "product", Fields: []definitionmodel.FieldSchema{{Key: "sku", Type: "text"}, {Key: "name", Type: "text", Config: map[string]any{"localized": true}}}}
	service := NewRecordCreateApplicationService(RecordCreateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanWrite: func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
		BuildAudit: func(_ context.Context, event, objectKey, recordID string, _ principalmodel.Principal, summary string, before, after, metadata map[string]any) auditmodel.AuditEvent {
			return auditmodel.AuditEvent{Event: event, ObjectKey: objectKey, RecordID: recordID, Summary: summary, Before: before, After: after, Metadata: metadata}
		},
		NewRecordID: func(string) string { return "product-1" },
		Now:         func() time.Time { return time.Date(2026, time.August, 11, 10, 0, 0, 0, time.UTC) },
	})
	created, err := service.CreateClaimedLocalized(t.Context(), "product", map[string]any{"sku": "P-1", "name": "默认商品"}, recordmodel.RecordTranslations{"en-US": {"name": "Product"}, "zh-CN": {"name": "商品"}}, recordmodel.RecordMutationClaimResult{}, recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}))
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "product-1" || len(repository.commit.LocalizedValues) != 2 {
		t.Fatalf("created=%+v localized=%+v", created, repository.commit.LocalizedValues)
	}
	if repository.commit.LocalizedValues[0].Locale != "en-US" || repository.commit.LocalizedValues[0].TextValue != "Product" || repository.commit.LocalizedValues[1].Locale != "zh-CN" {
		t.Fatalf("localized commit=%+v", repository.commit.LocalizedValues)
	}
	if repository.commit.Audit == nil || fmt.Sprint(repository.commit.Audit.Metadata["localized_fields"]) != "[name]" || fmt.Sprint(repository.commit.Audit.Metadata["localized_locales"]) != "[en-US zh-CN]" || repository.commit.Audit.Metadata["localized_value_count"] != 2 {
		t.Fatalf("localized audit=%+v", repository.commit.Audit)
	}
	if encoded := fmt.Sprint(repository.commit.Audit.Metadata); strings.Contains(encoded, "Product") || strings.Contains(encoded, "商品") {
		t.Fatalf("localized audit leaked translated text: %s", encoded)
	}
}

func TestCreateUserFieldOmissionAndDefaults(t *testing.T) {
	tests := []struct {
		name    string
		field   definitionmodel.FieldSchema
		input   map[string]any
		want    any
		wantKey bool
	}{
		{name: "optional omitted remains null", field: definitionmodel.FieldSchema{Key: "adopted_by", Type: "user"}, input: map[string]any{}, want: nil, wantKey: false},
		{name: "explicit value preserved", field: definitionmodel.FieldSchema{Key: "adopted_by", Type: "user"}, input: map[string]any{"adopted_by": "reviewer"}, want: "reviewer", wantKey: true},
		{name: "metadata default value preserved", field: definitionmodel.FieldSchema{Key: "adopted_by", Type: "user", DefaultValue: "designated-reviewer"}, input: map[string]any{}, want: "designated-reviewer", wantKey: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &createRepositoryProbe{}
			object := definitionmodel.ObjectSchema{Key: "governed_document", Fields: []definitionmodel.FieldSchema{test.field}}
			service := NewRecordCreateApplicationService(RecordCreateDependencies{
				Repository: repository,
				ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
					return object, nil
				},
				CanWrite:    func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
				NewRecordID: func(string) string { return "document-1" },
			})
			created, err := service.Create(t.Context(), object.Key, test.input, recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "uploader", OrgID: "store-a"}}))
			if err != nil {
				t.Fatal(err)
			}
			got, exists := repository.commit.Record.Data[test.field.Key]
			if exists != test.wantKey || !reflect.DeepEqual(got, test.want) || !reflect.DeepEqual(created.Data, repository.commit.Record.Data) {
				t.Fatalf("created=%#v persisted=%#v want value=%#v present=%v", created.Data, repository.commit.Record.Data, test.want, test.wantKey)
			}
			if created.OwnerUserID != "uploader" || repository.commit.Record.OwnerUserID != "uploader" || created.OwnerOrgID != "store-a" || repository.commit.Record.OwnerOrgID != "store-a" {
				t.Fatalf("Runtime owner metadata was not defaulted: created=%#v persisted=%#v", created, repository.commit.Record)
			}
		})
	}
}

func TestCreateUsesObjectAndRowAuthorizationWhilePreservingDataValidation(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "ticket", Fields: []definitionmodel.FieldSchema{
		{Key: "title", Type: "text"},
		{Key: "status", Type: "text", DefaultValue: "new"},
	}}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Key: "agent", FieldPolicies: []accessfixture.FieldPolicyFixture{
		{ObjectKey: object.Key, FieldKey: "title", Write: true},
		{ObjectKey: object.Key, FieldKey: "status", Write: false},
	}},
	)
	newService := func(repository *createRepositoryProbe, configure func(*RecordCreateDependencies)) *RecordCreateApplicationService {
		dependencies := RecordCreateDependencies{
			Repository: repository,
			ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
				return object, nil
			},
			CanWriteCandidate: func(_ context.Context, _ principalmodel.Principal, _ definitionmodel.ObjectSchema, _ recordmodel.Record) (bool, error) {
				return true, nil
			},
			ValidatePolicies: func(_ context.Context, _ definitionmodel.ObjectSchema, _ map[string]any, candidate map[string]any, _, _ string, _ principalmodel.Principal) error {
				if candidate["status"] != "new" && candidate["status"] != "closed" {
					return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "ticket.status_invalid"}
				}
				return nil
			},
			NewRecordID: func(string) string { return "ticket-1" },
		}
		if configure != nil {
			configure(&dependencies)
		}
		return NewRecordCreateApplicationService(dependencies)
	}

	t.Run("runtime default is authorized as candidate and persisted", func(t *testing.T) {
		repository := &createRepositoryProbe{}
		created, err := newService(repository, nil).Create(t.Context(), object.Key, map[string]any{"title": "Printer offline"}, principal)
		if err != nil {
			t.Fatal(err)
		}
		if created.Data["status"] != "new" || repository.commit.Record.Data["status"] != "new" {
			t.Fatalf("created=%#v persisted=%#v", created.Data, repository.commit.Record.Data)
		}
	})

	t.Run("field write policy does not block authorized creation", func(t *testing.T) {
		repository := &createRepositoryProbe{}
		_, err := newService(repository, nil).Create(t.Context(), object.Key, map[string]any{"title": "Printer offline", "status": "closed"}, principal)
		if err != nil || repository.commit.Operation != "create" || repository.commit.Record.Data["status"] != "closed" {
			t.Fatalf("err=%v commit=%#v", err, repository.commit)
		}
	})

	t.Run("business validation still rejects invalid state", func(t *testing.T) {
		repository := &createRepositoryProbe{}
		_, err := newService(repository, nil).Create(t.Context(), object.Key, map[string]any{"title": "Printer offline", "status": "invalid"}, principal)
		if apperror.CodeOf(err) != "ticket.status_invalid" || repository.commit.Operation != "" {
			t.Fatalf("err=%v commit=%#v", err, repository.commit)
		}
	})

	t.Run("unknown caller field is still rejected", func(t *testing.T) {
		repository := &createRepositoryProbe{}
		_, err := newService(repository, nil).Create(t.Context(), object.Key, map[string]any{"title": "Printer offline", "unknown": true}, principal)
		if apperror.CodeOf(err) != "backend.validation.unknown_field" || repository.commit.Operation != "" {
			t.Fatalf("err=%v commit=%#v", err, repository.commit)
		}
	})

	t.Run("candidate scope denial still rejects defaulted record", func(t *testing.T) {
		repository := &createRepositoryProbe{}
		service := newService(repository, func(dependencies *RecordCreateDependencies) {
			dependencies.CanWriteCandidate = func(_ context.Context, _ principalmodel.Principal, _ definitionmodel.ObjectSchema, candidate recordmodel.Record) (bool, error) {
				return candidate.Data["status"] != "new", nil
			}
		})
		_, err := service.Create(t.Context(), object.Key, map[string]any{"title": "Printer offline"}, principal)
		if apperror.CodeOf(err) != "backend.record.owner_write_denied" || repository.commit.Operation != "" {
			t.Fatalf("err=%v commit=%#v", err, repository.commit)
		}
	})
}

func (p *createExecutionProbe) CommitRecordMutationExecution(_ context.Context, commit transactionmodel.RecordMutationCommit, _ recordmodel.RecordMutationCompletion) (recordmodel.RecordMutationExecution, error) {
	p.commitCalls++
	p.commit = commit
	if p.commitErr != nil {
		return recordmodel.RecordMutationExecution{}, p.commitErr
	}
	p.execution.Status, p.execution.Result = string(idempotency.StatusSucceeded), commit.Record
	return p.execution, nil
}

func (p *createExecutionProbe) CompleteRecordMutationExecution(_ context.Context, completion recordmodel.RecordMutationCompletion) (recordmodel.RecordMutationExecution, error) {
	p.commitCalls++
	p.execution.Status = string(idempotency.StatusSucceeded)
	return p.execution, nil
}

func (r *createRepositoryProbe) CommitRecordMutation(_ context.Context, _ string, commit transactionmodel.RecordMutationCommit) error {
	if r.err != nil {
		return r.err
	}
	r.commit = commit
	if r.onCommit != nil {
		r.onCommit()
	}
	return nil
}

func TestCreateServiceOwnsCompleteCreateTransaction(t *testing.T) {
	repository := &createRepositoryProbe{}
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	var calls []string
	var executed []workflowmodel.WorkflowExecution
	service := NewRecordCreateApplicationService(RecordCreateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			calls = append(calls, "object")
			return object, nil
		},
		CanWrite: func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool {
			calls = append(calls, "write")
			return true
		},
		ValidatePipeline: func(context.Context, definitionmodel.ObjectSchema, string, map[string]any, principalmodel.Principal) error {
			calls = append(calls, "pipeline_validate")
			return nil
		},
		ApplyPipelineDefaults: func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal, bool) error {
			calls = append(calls, "pipeline_defaults")
			return nil
		},
		FindReplay: func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) (recordmodel.Record, bool, error) {
			calls = append(calls, "replay")
			return recordmodel.Record{}, false, nil
		},
		RunBefore: func(_ context.Context, _, _, _ string, _, _ map[string]any, candidate map[string]any, _ principalmodel.Principal) error {
			calls = append(calls, "automation_before")
			candidate["name"] = "Automated"
			return nil
		},
		ValidateRelations: func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) error {
			calls = append(calls, "relations")
			return nil
		},
		ValidatePolicies: func(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, string, principalmodel.Principal) error {
			calls = append(calls, "policies")
			return nil
		},
		ValidateUnique: func(context.Context, string, string, definitionmodel.ObjectSchema, string, map[string]any) error {
			calls = append(calls, "unique")
			return nil
		},
		ValidateDuplicate: func(context.Context, string, definitionmodel.ObjectSchema, string, map[string]any) error {
			calls = append(calls, "duplicate")
			return nil
		},
		AfterOutbox: func(string, string, map[string]any, recordmodel.Record, principalmodel.Principal) []publicationmodel.Message {
			calls = append(calls, "outbox")
			return []publicationmodel.Message{{ID: "outbox-1"}}
		},
		PrepareWorkflow: func(context.Context, string, recordmodel.Record, map[string]any, principalmodel.Principal, string) ([]workflowmodel.WorkflowExecution, error) {
			calls = append(calls, "workflow_prepare")
			return []workflowmodel.WorkflowExecution{{ID: "workflow-1"}}, nil
		},
		ExecuteWorkflow: func(_ context.Context, intents []workflowmodel.WorkflowExecution, _ principalmodel.Principal) {
			calls = append(calls, "workflow_execute")
			executed = append(executed, intents...)
		},
		BuildAudit: func(_ context.Context, event, objectKey, recordID string, _ principalmodel.Principal, summary string, before, after, metadata map[string]any) auditmodel.AuditEvent {
			return auditmodel.AuditEvent{Event: event, ObjectKey: objectKey, RecordID: recordID, Summary: summary, Before: before, After: after, Metadata: metadata}
		},
		NewRecordID: func(string) string { return "customer-1" },
		Now:         func() time.Time { return time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC) },
	})

	created, err := service.Create(t.Context(), "customer", map[string]any{"name": "Input"}, recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}))
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "customer-1" || created.Data["name"] != "Automated" || created.CreatedAt != "2026-07-17T12:00:00Z" || created.UpdatedAt != created.CreatedAt {
		t.Fatalf("created = %#v", created)
	}
	if repository.commit.Operation != "create" || repository.commit.Audit == nil || repository.commit.Audit.Event != "record_created" || len(repository.commit.Outbox) != 1 || len(repository.commit.WorkflowIntents) != 1 {
		t.Fatalf("create commit = %#v", repository.commit)
	}
	wantCalls := []string{"object", "pipeline_validate", "pipeline_defaults", "write", "replay", "automation_before", "relations", "policies", "unique", "duplicate", "outbox", "workflow_prepare", "workflow_execute"}
	if !reflect.DeepEqual(calls, wantCalls) || len(executed) != 1 {
		t.Fatalf("calls = %v, executed = %#v", calls, executed)
	}
}

func TestCreateServiceReturnsIdempotentReplayWithoutCommit(t *testing.T) {
	repository := &createRepositoryProbe{}
	replay := recordmodel.Record{ID: "customer-existing", Data: map[string]any{"name": "Existing"}}
	audited := false
	service := NewRecordCreateApplicationService(RecordCreateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}, nil
		},
		CanWrite: func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
		FindReplay: func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) (recordmodel.Record, bool, error) {
			return replay, true, nil
		},
		Audit: func(_ context.Context, event, _, recordID string, _ principalmodel.Principal, _ string, _, _ map[string]any, metadata map[string]any) {
			audited = event == "automation_create_idempotent_replay" && recordID == replay.ID && metadata["operation"] == "create"
		},
		NewRecordID: func(string) string { return "unused" },
	})

	created, err := service.Create(t.Context(), "customer", map[string]any{"name": "Existing"}, recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != replay.ID || !audited || repository.commit.Operation != "" {
		t.Fatalf("replay=%#v audited=%v commit=%#v", created, audited, repository.commit)
	}
}

func TestCreateServiceCallerKeyCommitsOnceAndReplaysDurableResult(t *testing.T) {
	repository := &createRepositoryProbe{}
	executions := &createExecutionProbe{}
	automationCalls := 0
	var auditEvents []auditmodel.AuditEvent
	service := NewRecordCreateApplicationService(RecordCreateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}, nil
		},
		CanWrite: func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
		RunBefore: func(context.Context, string, string, string, map[string]any, map[string]any, map[string]any, principalmodel.Principal) error {
			automationCalls++
			return nil
		},
		NewRecordID:      func(string) string { return "customer-1" },
		ExecutionRuntime: recordruntime.NewRecordMutationExecutionRuntime(executions),
		BuildAudit: func(_ context.Context, event, objectKey, recordID string, _ principalmodel.Principal, summary string, before, after, metadata map[string]any) auditmodel.AuditEvent {
			return auditmodel.AuditEvent{Event: event, ObjectKey: objectKey, RecordID: recordID, Summary: summary, Before: before, After: after, Metadata: metadata}
		},
		Audit: func(_ context.Context, event, objectKey, recordID string, _ principalmodel.Principal, summary string, before, after, metadata map[string]any) {
			auditEvents = append(auditEvents, auditmodel.AuditEvent{Event: event, ObjectKey: objectKey, RecordID: recordID, Summary: summary, Before: before, After: after, Metadata: metadata})
		},
	})
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}, RequestID: "request-a"})
	first, err := service.CreateIdempotent(t.Context(), "customer", map[string]any{"name": "Acme"}, "create-key", principal)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateIdempotent(t.Context(), "customer", map[string]any{"name": "Acme"}, "create-key", principal)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "customer-1" || second.ID != first.ID || executions.commitCalls != 1 || automationCalls != 1 || repository.commit.Operation != "" {
		t.Fatalf("first=%#v second=%#v receipt commits=%d automation=%d repository=%#v", first, second, executions.commitCalls, automationCalls, repository.commit)
	}
	if _, err := service.CreateIdempotent(t.Context(), "customer", map[string]any{"name": "Different"}, "create-key", principal); apperror.CodeOf(err) != idempotency.ErrorCodeKeyReused {
		t.Fatalf("fingerprint conflict error=%v", err)
	}
	if len(auditEvents) != 2 || auditEvents[0].Event != "record_create_idempotent_replayed" || auditEvents[1].Event != "record_create_idempotency_fingerprint_conflict" {
		t.Fatalf("idempotency audits=%#v", auditEvents)
	}
	if executions.commit.Audit == nil {
		t.Fatal("idempotent commit audit missing")
	}
	for _, event := range append(auditEvents, *executions.commit.Audit) {
		encoded := fmt.Sprint(event.Metadata)
		if event.Metadata["idempotency_scope"] != "record.create" || event.Metadata["idempotency_key_hash"] == nil || event.Metadata["request_fingerprint_hash"] == nil || strings.Contains(encoded, "create-key") || strings.Contains(encoded, "Different") {
			t.Fatalf("unsafe idempotency audit=%#v", event)
		}
	}
}

func TestCreateServiceWrapsCommitFailure(t *testing.T) {
	repository := &createRepositoryProbe{err: errors.New("store unavailable")}
	service := NewRecordCreateApplicationService(RecordCreateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}, nil
		},
		CanWrite:    func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
		NewRecordID: func(string) string { return "customer-1" },
	})

	_, err := service.Create(t.Context(), "customer", map[string]any{"name": "Acme"}, recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
	assertRecordCreateApplicationError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "commit record create"})
}

func TestCreateServiceMapsUniqueCommitFailureToStableConflict(t *testing.T) {
	repository := &createRepositoryProbe{err: mutation.MutationConflict("customer", "customer-1", mutation.MutationConflictUnique, errors.New("UNIQUE constraint failed: runtime_records.id"))}
	service := NewRecordCreateApplicationService(RecordCreateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}, nil
		},
		CanWrite:    func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
		NewRecordID: func(string) string { return "customer-1" },
	})

	_, err := service.Create(t.Context(), "customer", map[string]any{"name": "Acme"}, recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}))
	assertRecordCreateApplicationError(t, err, apperror.KindConflict, "backend.mutation.unique_conflict", map[string]string{"resource": "customer", "identifier": "customer-1"})
	if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
		t.Fatalf("application error leaked dialect detail: %v", err)
	}
}

func assertRecordCreateApplicationError(t *testing.T, err error, kind apperror.ErrorKind, code string, params map[string]string) {
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
