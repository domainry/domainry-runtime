package record

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type recordDisplayRepository struct {
	recordrepository.RecordRepository
	record recordmodel.Record
	found  bool
	err    error
}

func (r *recordDisplayRepository) GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	return r.record, r.found, r.err
}

func (r *recordDisplayRepository) ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	if r.err != nil {
		return recordmodel.RecordPageResult{}, r.err
	}
	if !r.found {
		return recordmodel.RecordPageResult{}, nil
	}
	return recordmodel.RecordPageResult{Items: []recordmodel.Record{r.record}, Total: 1}, nil
}

func TestRecordMutationPlanApplicationErrorRemainingMappings(t *testing.T) {
	if recordMutationPlanApplicationError(nil) != nil {
		t.Fatal("nil planner error changed")
	}
	tests := []struct {
		err      error
		wantKind apperror.ErrorKind
		wantCode string
	}{
		{&recordmutation.MutationPlannerError{Code: "backend.mutation.action_required", Field: "status"}, apperror.KindForbidden, "backend.mutation.action_required"},
		{&recordmutation.MutationPlannerError{Code: "backend.mutation.predicate_invalid", Field: "status"}, apperror.KindBadRequest, "backend.mutation.predicate_invalid"},
		{&recordmutation.MutationPlannerError{Code: "backend.mutation.other", Field: "status"}, apperror.KindInternal, "backend.mutation.other"},
		{&transactionmodel.MutationPlanError{Code: "backend.mutation.effect_authority_denied", Field: "status"}, apperror.KindForbidden, "backend.mutation.effect_authority_denied"},
		{&transactionmodel.MutationPlanError{Code: "backend.mutation.plan_invalid", Field: "status"}, apperror.KindBadRequest, "backend.mutation.plan_invalid"},
	}
	for _, test := range tests {
		got := recordMutationPlanApplicationError(test.err)
		if apperror.KindOf(got) != test.wantKind || apperror.CodeOf(got) != test.wantCode {
			t.Fatalf("input=%v got=%v kind=%s code=%s", test.err, got, apperror.KindOf(got), apperror.CodeOf(got))
		}
	}
	plain := errors.New("plain")
	if recordMutationPlanApplicationError(plain) != plain {
		t.Fatal("plain error was translated")
	}
}

func TestRecordScopeAllowsActionRemainingBoundaries(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	objects := map[string]definitionmodel.ObjectSchema{object.Key: object}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}, RecordScope: "all_records"})
	newService := func(repository recordrepository.RecordRepository) *RecordApplicationService {
		queryPolicy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{
			Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{object} },
		})
		return NewRecordApplicationService(RecordApplicationDependencies{
			Repository: repository, QueryPolicy: queryPolicy,
			SchemaMap: func() map[string]definitionmodel.ObjectSchema { return objects },
		})
	}

	if _, err := newService(&recordDisplayRepository{}).RecordScopeAllowsAction(t.Context(), object.Key, "record-1", "read", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization err=%v", err)
	}
	for _, ids := range [][2]string{{"", "record-1"}, {object.Key, ""}} {
		if _, err := newService(&recordDisplayRepository{}).RecordScopeAllowsAction(t.Context(), ids[0], ids[1], "read", principal); apperror.CodeOf(err) != "backend.permissions.record_context_required" {
			t.Fatalf("ids=%v err=%v", ids, err)
		}
	}

	noSchema := newService(&recordDisplayRepository{})
	noSchema.schemaMap = nil
	if _, err := noSchema.RecordScopeAllowsAction(t.Context(), object.Key, "record-1", "read", principal); apperror.CodeOf(err) != "backend.permissions.schema_unavailable" {
		t.Fatalf("schema err=%v", err)
	}
	missingObject := newService(&recordDisplayRepository{})
	missingObject.schemaMap = func() map[string]definitionmodel.ObjectSchema { return map[string]definitionmodel.ObjectSchema{} }
	if _, err := missingObject.RecordScopeAllowsAction(t.Context(), object.Key, "record-1", "read", principal); apperror.CodeOf(err) != "backend.object.not_found" {
		t.Fatalf("object err=%v", err)
	}

	noDomain := newService(&recordDisplayRepository{})
	noDomain.RecordDomainService = nil
	if _, err := noDomain.RecordScopeAllowsAction(t.Context(), object.Key, "record-1", "read", principal); apperror.CodeOf(err) != "backend.permissions.record_store_unavailable" {
		t.Fatalf("domain err=%v", err)
	}
	nilRepository := newService(nil)
	if _, err := nilRepository.RecordScopeAllowsAction(t.Context(), object.Key, "record-1", "read", principal); apperror.CodeOf(err) != "backend.permissions.record_store_unavailable" {
		t.Fatalf("repository err=%v", err)
	}

	repository := &recordDisplayRepository{err: errors.New("load failed")}
	if _, err := newService(repository).RecordScopeAllowsAction(t.Context(), object.Key, "record-1", "read", principal); apperror.CodeOf(err) != "backend.permissions.record_lookup_failed" {
		t.Fatalf("lookup err=%v", err)
	}
	repository.err, repository.found = nil, false
	if allowed, err := newService(repository).RecordScopeAllowsAction(t.Context(), object.Key, "record-1", "read", principal); err != nil || allowed {
		t.Fatalf("missing allowed=%v err=%v", allowed, err)
	}
	repository.found = true
	repository.record = recordmodel.Record{ID: "record-1", Data: map[string]any{"name": "Acme"}}
	if allowed, err := newService(repository).RecordScopeAllows(t.Context(), object.Key, "record-1", principal); err != nil || !allowed {
		t.Fatalf("wrapper allowed=%v err=%v", allowed, err)
	}
	for _, action := range []string{"read", "export", "update"} {
		if allowed, err := newService(repository).RecordScopeAllowsAction(t.Context(), object.Key, "record-1", action, principal); err != nil || !allowed {
			t.Fatalf("action=%s allowed=%v err=%v", action, allowed, err)
		}
	}

	service := newService(repository)
	if _, err := service.GetRecordForUpdate(t.Context(), object.Key, "record-1", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("locking authorization err=%v", err)
	}
	if record, err := service.GetRecordForUpdate(t.Context(), object.Key, "record-1", principal); err != nil || record.ID != "record-1" {
		t.Fatalf("locking record=%+v err=%v", record, err)
	}
	if page, err := service.ListRecordsForAction(t.Context(), object.Key, recordmodel.RecordListQuery{}, principal); err != nil || len(page.Items) != 1 {
		t.Fatalf("action page=%+v err=%v", page, err)
	}
	if record, err := service.GetRecordForAction(t.Context(), object.Key, "record-1", principal); err != nil || record.ID != "record-1" {
		t.Fatalf("action record=%+v err=%v", record, err)
	}
	if record, err := service.GetRecordForUpdateForAction(t.Context(), object.Key, "record-1", principal); err != nil || record.ID != "record-1" {
		t.Fatalf("action locking record=%+v err=%v", record, err)
	}
}

func TestRecordActionDisplayFacadesRejectUnknownPrincipal(t *testing.T) {
	service := &RecordApplicationService{}
	if _, err := service.ListRecordsForAction(t.Context(), "customer", recordmodel.RecordListQuery{}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("list err=%v", err)
	}
	if _, err := service.GetRecordForAction(t.Context(), "customer", "record", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("get err=%v", err)
	}
	if _, err := service.GetRecordForUpdateForAction(t.Context(), "customer", "record", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("get update err=%v", err)
	}
}

func TestProjectRecordFieldsRemainingPolicyAndAuditEdges(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "secret", Type: "text"}}}
	records := []recordmodel.Record{
		{ID: "one", Data: map[string]any{"secret": "a"}},
		{ID: "two", Data: map[string]any{"secret": "b"}},
		{ID: "three", Data: map[string]any{}},
	}
	service := &RecordApplicationService{}
	projected, err := service.ProjectRecordFields(t.Context(), principalmodel.Principal{}, object, records, "read")
	if err != nil || len(projected) != 3 {
		t.Fatalf("nil policy projected=%v err=%v", projected, err)
	}

	repository := &recordDisplayRepository{}
	service = NewRecordApplicationService(RecordApplicationDependencies{
		Repository: repository,
		SchemaMap: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{object.Key: object}
		},
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{
		FieldPolicies: []accessfixture.FieldPolicyFixture{{
			ObjectKey: object.Key, FieldKey: "secret",
			Policies: []accessfixture.FieldRuleFixture{{Key: "invalid", Actions: []string{"read"}, Effect: "invalid"}},
		}},
	})
	if _, err := service.ProjectRecordFields(t.Context(), principal, object, records, "read"); apperror.CodeOf(err) != "backend.field_policy.invalid_effect" {
		t.Fatalf("policy err=%v", err)
	}

	audited := []string{}
	service.audit = func(_ context.Context, event, _, recordID string, _ principalmodel.Principal, _ string, _, _, _ map[string]any) {
		audited = append(audited, event+":"+recordID)
	}
	accessfixture.Set(&principal, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{{
		ObjectKey: object.Key, FieldKey: "secret",
		Policies: []accessfixture.FieldRuleFixture{{Key: "hidden", Actions: []string{"read"}, Effect: "hide", AuditDenial: true}},
	}}})
	projected, err = service.ProjectRecordFields(t.Context(), principal, object, records, "read")
	if err != nil || len(projected) != 3 || len(audited) != 2 || len(projected[0].Data) != 0 {
		t.Fatalf("projected=%v audited=%v err=%v", projected, audited, err)
	}
}
