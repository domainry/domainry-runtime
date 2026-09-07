package record

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordruntime "github.com/domainry/domainry-runtime/runtime/domain/record/runtime"
)

func recordImportEdgeObject() definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{
		{Key: "name", Type: "text", Required: true, Unique: true},
		{Key: "age", Type: "number"},
	}}
}

func recordImportEdgePrincipal() principalmodel.Principal {
	return recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
}

func recordImportEdgeService(repository *importRepositoryProbe) *RecordImportApplicationService {
	object := recordImportEdgeObject()
	return NewRecordImportApplicationService(RecordImportDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanWrite: func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
	})
}

func TestImportPreviewFormatAndRowValidationEdges(t *testing.T) {
	principal := recordImportEdgePrincipal()
	service := recordImportEdgeService(&importRepositoryProbe{existing: map[string]bool{"customer:name:Existing": true}})
	tooManyHeaders := strings.TrimSuffix(strings.Repeat("unknown,", recordImportMaxColumns+1), ",") + "\n"

	for _, test := range []struct {
		name      string
		raw       string
		wantCode  string
		invalid   int
		duplicate int
	}{
		{name: "header required", raw: "", wantCode: "backend.import.header_required"},
		{name: "invalid csv", raw: "name\n\"unterminated", wantCode: "backend.import.invalid_csv"},
		{name: "too many columns", raw: tooManyHeaders, wantCode: "backend.import.too_many_columns"},
		{name: "unknown field", raw: "missing\nvalue\n", invalid: 1},
		{name: "invalid number", raw: "name,age\nAcme,not-a-number\n", invalid: 1},
		{name: "required field", raw: "name,age\n,5\n", invalid: 1},
		{name: "existing duplicate", raw: "name\nExisting\n", invalid: 1, duplicate: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			preview, err := service.Preview(t.Context(), "customer", []byte(test.raw), principal)
			if test.wantCode != "" {
				if apperror.CodeOf(err) != test.wantCode {
					t.Fatalf("err=%v code=%q want=%q", err, apperror.CodeOf(err), test.wantCode)
				}
				return
			}
			if err != nil || preview.InvalidRows != test.invalid || preview.DuplicateRows != test.duplicate || preview.CanApply {
				t.Fatalf("preview=%#v err=%v", preview, err)
			}
		})
	}
}

func TestImportPreviewScopeAndRelationFailuresBecomeRowIssues(t *testing.T) {
	failure := recordCodedError{code: "relation.invalid", params: map[string]string{"field": "owner"}}
	for _, stage := range []string{"scope", "relation"} {
		t.Run(stage, func(t *testing.T) {
			service := recordImportEdgeService(&importRepositoryProbe{existing: map[string]bool{}})
			if stage == "scope" {
				service.dependencies.CanWrite = func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return false }
			} else {
				service.dependencies.ValidateRelations = func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) error {
					return failure
				}
			}
			preview, err := service.Preview(t.Context(), "customer", []byte("name\nAcme\n"), recordImportEdgePrincipal())
			if err != nil || preview.InvalidRows != 1 || len(preview.ErrorRows) != 1 {
				t.Fatalf("preview=%#v err=%v", preview, err)
			}
			wantCode := "backend.record.outside_scope"
			if stage == "relation" {
				wantCode = "relation.invalid"
			}
			found := false
			for _, issue := range preview.ErrorRows[0].Issues {
				found = found || issue.Code == wantCode
			}
			if !found {
				t.Fatalf("issues=%#v want=%q", preview.ErrorRows[0].Issues, wantCode)
			}
		})
	}
}

func TestImportApplyRejectsInvalidRowsAndStopsOnCreateFailure(t *testing.T) {
	service := recordImportEdgeService(&importRepositoryProbe{existing: map[string]bool{}})
	service.dependencies.CreateRecord = func(context.Context, string, map[string]any, principalmodel.Principal) (recordmodel.Record, error) {
		return recordmodel.Record{}, errors.New("must not create invalid preview")
	}
	if _, err := service.Apply(t.Context(), "customer", []byte("missing\nvalue\n"), recordImportEdgePrincipal()); apperror.CodeOf(err) != "backend.import.invalid_rows" {
		t.Fatalf("invalid apply err=%v code=%q", err, apperror.CodeOf(err))
	}

	failure := errors.New("create failed")
	service.dependencies.CreateRecord = func(context.Context, string, map[string]any, principalmodel.Principal) (recordmodel.Record, error) {
		return recordmodel.Record{}, failure
	}
	if _, err := service.Apply(t.Context(), "customer", []byte("name\nAcme\n"), recordImportEdgePrincipal()); !errors.Is(err, failure) {
		t.Fatalf("create err=%v", err)
	}
}

func TestImportApplyObservesCancellationBetweenRows(t *testing.T) {
	service := recordImportEdgeService(&importRepositoryProbe{existing: map[string]bool{}})
	ctx, cancel := context.WithCancel(t.Context())
	createCalls := 0
	service.dependencies.CreateRecord = func(context.Context, string, map[string]any, principalmodel.Principal) (recordmodel.Record, error) {
		createCalls++
		cancel()
		return recordmodel.Record{ID: "created"}, nil
	}
	if _, err := service.Apply(ctx, "customer", []byte("name\nAcme\nBeta\n"), recordImportEdgePrincipal()); !errors.Is(err, context.Canceled) || createCalls != 1 {
		t.Fatalf("err=%v createCalls=%d", err, createCalls)
	}
}

type importCompletionErrorProbe struct {
	importExecutionProbe
	err error
}

func (p *importCompletionErrorProbe) CompleteRecordMutationExecution(context.Context, recordmodel.RecordMutationCompletion) (recordmodel.RecordMutationExecution, error) {
	return recordmodel.RecordMutationExecution{}, p.err
}

func TestImportIdempotentDependencyAndCompletionFailures(t *testing.T) {
	principal := recordImportEdgePrincipal()
	service := recordImportEdgeService(&importRepositoryProbe{existing: map[string]bool{}})
	if _, _, err := service.ApplyIdempotent(t.Context(), "customer", []byte("name\nAcme\n"), "operation-1", principal); apperror.CodeOf(err) != "backend.idempotency.receipt_unavailable" {
		t.Fatalf("missing runtime err=%v code=%q", err, apperror.CodeOf(err))
	}

	failure := errors.New("complete failed")
	executions := &importCompletionErrorProbe{err: failure}
	service.dependencies.Execution = recordruntime.NewRecordMutationExecutionRuntime(executions)
	service.dependencies.CreateIdempotent = func(context.Context, string, map[string]any, string, principalmodel.Principal) (recordmodel.Record, bool, error) {
		return recordmodel.Record{ID: "created"}, false, nil
	}
	if _, replay, err := service.ApplyIdempotent(t.Context(), "customer", []byte("name\nAcme\n"), "operation-1", principal); replay || !errors.Is(err, failure) {
		t.Fatalf("replay=%v err=%v", replay, err)
	}
}

func TestImportIdempotentRemainingDependencyAndCancellationEdges(t *testing.T) {
	principal := recordImportEdgePrincipal()
	service := recordImportEdgeService(&importRepositoryProbe{existing: map[string]bool{}})
	service.dependencies.Execution = recordruntime.NewRecordMutationExecutionRuntime(&importExecutionProbe{})
	if _, _, err := service.ApplyIdempotent(t.Context(), "customer", []byte("name\nAcme\n"), "operation-1", principal); apperror.CodeOf(err) != "backend.idempotency.receipt_unavailable" {
		t.Fatalf("missing create idempotent err=%v code=%q", err, apperror.CodeOf(err))
	}

	service.dependencies.CreateIdempotent = func(context.Context, string, map[string]any, string, principalmodel.Principal) (recordmodel.Record, bool, error) {
		return recordmodel.Record{}, false, nil
	}
	if _, _, err := service.ApplyIdempotent(t.Context(), "customer", []byte("name\nAcme\n"), " ", principal); apperror.CodeOf(err) != idempotency.ErrorCodeMissingKey {
		t.Fatalf("blank operation err=%v code=%q", err, apperror.CodeOf(err))
	}

	ctx, cancel := context.WithCancel(t.Context())
	createCalls := 0
	service.dependencies.Execution = recordruntime.NewRecordMutationExecutionRuntime(&importExecutionProbe{})
	service.dependencies.CreateIdempotent = func(context.Context, string, map[string]any, string, principalmodel.Principal) (recordmodel.Record, bool, error) {
		createCalls++
		cancel()
		return recordmodel.Record{}, false, nil
	}
	if _, _, err := service.ApplyIdempotent(ctx, "customer", []byte("name\nAcme\nBeta\n"), "operation-2", principal); !errors.Is(err, context.Canceled) || createCalls != 1 {
		t.Fatalf("between rows err=%v createCalls=%d", err, createCalls)
	}

	ctx, cancel = context.WithCancel(t.Context())
	createCalls = 0
	service.dependencies.Execution = recordruntime.NewRecordMutationExecutionRuntime(&importExecutionProbe{})
	service.dependencies.CreateIdempotent = func(context.Context, string, map[string]any, string, principalmodel.Principal) (recordmodel.Record, bool, error) {
		createCalls++
		if createCalls == recordImportBatchSize {
			cancel()
		}
		return recordmodel.Record{}, false, nil
	}
	var raw strings.Builder
	raw.WriteString("name\n")
	for index := 0; index < recordImportBatchSize; index++ {
		fmt.Fprintf(&raw, "customer-%d\n", index)
	}
	if _, _, err := service.ApplyIdempotent(ctx, "customer", []byte(raw.String()), "operation-3", principal); !errors.Is(err, context.Canceled) || createCalls != recordImportBatchSize {
		t.Fatalf("batch yield err=%v createCalls=%d", err, createCalls)
	}
}

func TestImportApplyAndIdempotentAuthorizationAndInvalidPreview(t *testing.T) {
	failure := errors.New("permission failed")
	service := NewRecordImportApplicationService(RecordImportDependencies{
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return definitionmodel.ObjectSchema{}, failure
		},
	})
	principal := recordImportEdgePrincipal()
	if _, err := service.Apply(t.Context(), "customer", []byte("name\nAcme\n"), principal); !errors.Is(err, failure) {
		t.Fatalf("apply permission err=%v", err)
	}
	if _, _, err := service.ApplyIdempotent(t.Context(), "customer", []byte("name\nAcme\n"), "operation-1", principal); !errors.Is(err, failure) {
		t.Fatalf("idempotent permission err=%v", err)
	}

	service = recordImportEdgeService(&importRepositoryProbe{existing: map[string]bool{}})
	if _, _, err := service.ApplyIdempotent(t.Context(), "customer", []byte("missing\nvalue\n"), "operation-1", principal); apperror.CodeOf(err) != "backend.import.invalid_rows" {
		t.Fatalf("invalid idempotent preview err=%v code=%q", err, apperror.CodeOf(err))
	}
}

func TestImportPreviewHeaderFieldPermissionAndShortRowEdges(t *testing.T) {
	service := recordImportEdgeService(&importRepositoryProbe{existing: map[string]bool{}})
	principal := recordImportEdgePrincipal()
	if _, err := service.Preview(t.Context(), "customer", []byte("\""), principal); apperror.CodeOf(err) != "backend.import.invalid_csv" {
		t.Fatalf("invalid header err=%v code=%q", err, apperror.CodeOf(err))
	}

	restricted := principal
	accessfixture.Set(&restricted, accessfixture.Bundle{
		Permissions:   []string{"customer.import"},
		FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "name", Read: true, Write: false}},
	})
	preview, err := service.Preview(t.Context(), "customer", []byte("name\nAcme\n"), restricted)
	if err != nil || !preview.CanApply || preview.ValidRows != 1 || preview.Rows[0].Data["name"] != "Acme" {
		t.Fatalf("restricted preview=%#v err=%v", preview, err)
	}

	if _, err = service.Preview(t.Context(), "customer", []byte("name,age\nAcme\n"), principal); apperror.CodeOf(err) != "backend.import.invalid_csv" {
		t.Fatalf("short row err=%v code=%q", err, apperror.CodeOf(err))
	}
}

func TestImportPreviewIgnoredColumnsOptionalScopeAndWritableDefault(t *testing.T) {
	principal := recordImportEdgePrincipal()
	service := recordImportEdgeService(&importRepositoryProbe{existing: map[string]bool{}})
	service.dependencies.CanWrite = nil
	preview, err := service.Preview(t.Context(), "customer", []byte(" ,name__display,name\nignored,Shown,Acme\n"), principal)
	if err != nil || !preview.CanApply || len(preview.Rows) != 1 || len(preview.Rows[0].RawValues) != 1 || preview.Rows[0].Data["name"] != "Acme" {
		t.Fatalf("ignored columns preview=%#v err=%v", preview, err)
	}
	domainObject := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{
		{Key: "status", Type: "status", Config: map[string]any{"options": []string{"open"}}},
	}}
	service = NewRecordImportApplicationService(RecordImportDependencies{
		Repository: &importRepositoryProbe{existing: map[string]bool{}},
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return domainObject, nil
		},
	})
	preview, err = service.Preview(t.Context(), "customer", []byte("status\nunknown\n"), principal)
	if err != nil || preview.InvalidRows != 1 || preview.ErrorRows[0].Issues[0].Code != "backend.import.invalid_value_domain_option" {
		t.Fatalf("value domain preview=%#v err=%v", preview, err)
	}

	ownerObject := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{
		{Key: "name", Type: "text", Required: true},
		{Key: "owner", Type: "user", Config: map[string]any{"auto_assign_current_user": true}},
	}}
	restricted := principal
	restricted.UserID = "user-1"
	accessfixture.Set(&restricted, accessfixture.Bundle{
		Permissions: []string{"customer.import"},
		FieldPolicies: []accessfixture.FieldPolicyFixture{
			{ObjectKey: "customer", FieldKey: "name", Read: true, Write: true},
			{ObjectKey: "customer", FieldKey: "owner", Read: true, Write: false},
		},
	})
	service = NewRecordImportApplicationService(RecordImportDependencies{
		Repository: &importRepositoryProbe{existing: map[string]bool{}},
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return ownerObject, nil
		},
	})
	preview, err = service.Preview(t.Context(), "customer", []byte("name\nAcme\n"), restricted)
	if err != nil || preview.InvalidRows != 0 || preview.ValidRows != 1 {
		t.Fatalf("writable default preview=%#v err=%v", preview, err)
	}
	if _, exists := preview.Rows[0].Data["owner"]; exists {
		t.Fatalf("business owner field was implicitly assigned: %#v", preview.Rows[0].Data)
	}
}

func TestImportErrorIgnoresBlankParameterKey(t *testing.T) {
	err := recordImportError(apperror.KindBadRequest, "backend.import.invalid_rows", nil, " ", "ignored")
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Params != nil {
		t.Fatalf("err=%#v", err)
	}
}

func TestImportApplyCancellationAtBatchYield(t *testing.T) {
	service := recordImportEdgeService(&importRepositoryProbe{existing: map[string]bool{}})
	ctx, cancel := context.WithCancel(t.Context())
	var csv strings.Builder
	csv.WriteString("name\n")
	for index := 0; index < recordImportBatchSize; index++ {
		fmt.Fprintf(&csv, "customer-%d\n", index)
	}
	createCalls := 0
	service.dependencies.CreateRecord = func(context.Context, string, map[string]any, principalmodel.Principal) (recordmodel.Record, error) {
		createCalls++
		if createCalls == recordImportBatchSize {
			cancel()
		}
		return recordmodel.Record{ID: "created"}, nil
	}
	if _, err := service.Apply(ctx, "customer", []byte(csv.String()), recordImportEdgePrincipal()); !errors.Is(err, context.Canceled) || createCalls != recordImportBatchSize {
		t.Fatalf("err=%v createCalls=%d", err, createCalls)
	}
	if err := recordImportBatchYield(t.Context(), recordImportBatchSize); err != nil {
		t.Fatalf("successful batch yield err=%v", err)
	}
}
