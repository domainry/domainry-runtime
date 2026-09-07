package record

import accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordruntime "github.com/domainry/domainry-runtime/runtime/domain/record/runtime"
)

type recordCreateEmptyCodeError struct{}

func (recordCreateEmptyCodeError) Error() string                  { return "empty code" }
func (recordCreateEmptyCodeError) ErrorCode() string              { return " " }
func (recordCreateEmptyCodeError) ErrorParams() map[string]string { return nil }

func TestRecordCreateAuthorizationSchedulerNilPayloadAndWritableEdges(t *testing.T) {
	principal := recordUpdateFinalPrincipal()
	if _, err := NewRecordCreateApplicationService(RecordCreateDependencies{}).Create(t.Context(), "customer", nil, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization error = %v", err)
	}
	repository := &createRepositoryProbe{}
	dependencies := recordCreateEdgeDependencies(repository)
	dependencies.ObjectForAction = func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
		return definitionmodel.ObjectSchema{Key: "record_timer", Config: map[string]any{"record_timer_runtime": true}}, nil
	}
	if _, err := NewRecordCreateApplicationService(dependencies).Create(t.Context(), "record_timer", nil, principal); apperror.CodeOf(err) != "backend.record_timer.runtime_api_required" {
		t.Fatalf("scheduler guard error = %v", err)
	}

	dependencies = RecordCreateDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return definitionmodel.ObjectSchema{Key: "empty"}, nil
		},
		NewRecordID: func(string) string { return "empty-1" },
	}
	if created, err := NewRecordCreateApplicationService(dependencies).Create(t.Context(), "empty", nil, principal); err != nil || created.Data == nil {
		t.Fatalf("nil payload create=%#v err=%v", created, err)
	}

	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	dependencies = recordCreateEdgeDependencies(repository)
	dependencies.ObjectForAction = func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
		return object, nil
	}
	denied := principal
	accessfixture.Set(&denied, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: object.Key, FieldKey: "name", Read: true, Write: false}}})
	if _, err := NewRecordCreateApplicationService(dependencies).Create(t.Context(), object.Key, map[string]any{"name": "Acme"}, denied); err != nil || repository.commit.Record.Data["name"] != "Acme" {
		t.Fatalf("field policy blocked an authorized create: err=%v commit=%#v", err, repository.commit)
	}
}

func TestRecordCreateSecondNormalizationValidationAndReplayAuditNilEdges(t *testing.T) {
	principal := recordUpdateFinalPrincipal()
	repository := &createRepositoryProbe{}
	numberObject := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "age", Type: "number"}}}
	dependencies := recordCreateEdgeDependencies(repository)
	dependencies.ObjectForAction = func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
		return numberObject, nil
	}
	dependencies.CanWrite = nil
	dependencies.RunBefore = func(_ context.Context, _, _, _ string, _, _ map[string]any, candidate map[string]any, _ principalmodel.Principal) error {
		candidate["age"] = "invalid"
		return nil
	}
	if _, err := NewRecordCreateApplicationService(dependencies).Create(t.Context(), numberObject.Key, map[string]any{"age": 1}, principal); apperror.CodeOf(err) != "backend.validation.number" {
		t.Fatalf("second normalization error = %v", err)
	}

	requiredObject := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true}}}
	dependencies = recordCreateEdgeDependencies(repository)
	dependencies.ObjectForAction = func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
		return requiredObject, nil
	}
	if _, err := NewRecordCreateApplicationService(dependencies).Create(t.Context(), requiredObject.Key, map[string]any{}, principal); apperror.CodeOf(err) != "backend.validation.required" {
		t.Fatalf("record validation error = %v", err)
	}

	replay := recordmodel.Record{ID: "existing", Data: map[string]any{"name": "Acme"}}
	dependencies = recordCreateEdgeDependencies(repository)
	dependencies.FindReplay = func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) (recordmodel.Record, bool, error) {
		return replay, true, nil
	}
	dependencies.Audit = nil
	if created, err := NewRecordCreateApplicationService(dependencies).Create(t.Context(), "customer", map[string]any{"name": "Acme"}, principal); err != nil || created.ID != replay.ID {
		t.Fatalf("unaudited replay=%#v err=%v", created, err)
	}
}

func TestRecordCreateIdempotencyBeginCommitAndPostCommitFailureEdges(t *testing.T) {
	principal := recordUpdateFinalPrincipal()
	edgeErr := errors.New("idempotency failed")
	repository := &createRepositoryProbe{}
	executions := &createExecutionProbe{beginErr: edgeErr}
	dependencies := recordCreateEdgeDependencies(repository)
	dependencies.ExecutionRuntime = recordruntime.NewRecordMutationExecutionRuntime(executions)
	dependencies.Audit = nil
	if _, err := NewRecordCreateApplicationService(dependencies).CreateIdempotent(t.Context(), "customer", map[string]any{"name": "Acme"}, "key", principal); !errors.Is(err, edgeErr) {
		t.Fatalf("begin error = %v", err)
	}

	executions = &createExecutionProbe{commitErr: edgeErr}
	dependencies.ExecutionRuntime = recordruntime.NewRecordMutationExecutionRuntime(executions)
	if _, err := NewRecordCreateApplicationService(dependencies).CreateIdempotent(t.Context(), "customer", map[string]any{"name": "Acme"}, "commit", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("idempotent commit error = %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	repository = &createRepositoryProbe{onCommit: cancel}
	dependencies = recordCreateEdgeDependencies(repository)
	created, err := NewRecordCreateApplicationService(dependencies).Create(ctx, "customer", map[string]any{"name": "Acme"}, principal)
	if !errors.Is(err, context.Canceled) || created.ID != "customer-1" {
		t.Fatalf("post-commit create=%#v err=%v", created, err)
	}

	service := NewRecordCreateApplicationService(RecordCreateDependencies{})
	service.auditIdempotency(t.Context(), "event", "customer", "", principal, idempotency.AuditFacts{Scope: "record.create"})
	if apperror.CodeOf(recordCreateErrorFrom(apperror.KindBadRequest, recordCreateEmptyCodeError{})) != "backend.bad_request" {
		t.Fatal("empty coded error did not use fallback")
	}
}
