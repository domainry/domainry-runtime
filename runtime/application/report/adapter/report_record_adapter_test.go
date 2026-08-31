package adapter

import (
	"context"
	"errors"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	pipelineapplication "github.com/domainry/domainry-runtime/runtime/application/pipeline"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
)

type reportRecordAdapterRepository struct {
	recordrepository.RecordRepository
	page             recordmodel.RecordPageResult
	updated          recordmodel.Record
	updateConditions map[string]any
	updateErr        error
}

func (r *reportRecordAdapterRepository) UpdateRecordWhere(_ context.Context, _ string, _ definitionmodel.ObjectSchema, record recordmodel.Record, conditions map[string]any) (bool, error) {
	r.updated, r.updateConditions = record, conditions
	return r.updateErr == nil, r.updateErr
}

func (r *reportRecordAdapterRepository) ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return r.page, nil
}

func TestReportRecordAdapterDelegatesRecordBoundaries(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}, {Key: "status", Type: "text"}, {Key: "retired", Type: "text", DisabledAt: "2026-08-10T00:00:00Z"}}}
	objects := map[string]definitionmodel.ObjectSchema{"customer": object}
	repository := &reportRecordAdapterRepository{page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "customer-1", Data: map[string]any{"name": "Acme"}}}}}
	objectForKey := func(_ context.Context, key string) (definitionmodel.ObjectSchema, bool) {
		value, ok := objects[key]
		return value, ok
	}
	queryPolicy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{object} }})
	pipeline := pipelineapplication.NewPipelineApplicationService(pipelineapplication.PipelineDependencies{Repository: repository, Object: objectForKey, CanAccess: queryPolicy.CanAccessRecord})
	validation := recordservice.NewRecordValidationDomainService(recordservice.RecordValidationDependencies{Repository: repository, Object: objectForKey, CanAccess: queryPolicy.CanAccessRecord})
	application := recordapplication.NewRecordApplicationService(recordapplication.RecordApplicationDependencies{
		Repository: repository, QueryPolicy: queryPolicy, Pipeline: pipeline, Validation: validation,
		ScopeOwnerFactDerivation: recordservice.NewRecordScopeOwnerFactDerivationDomainService(recordservice.RecordScopeOwnerFactDerivationDependencies{}),
		SchemaMap:                func() map[string]definitionmodel.ObjectSchema { return objects }, IdentityProfileExtensions: func() []profilebindingmodel.Binding { return nil },
	})
	adapter := NewReportRecordAdapter(application, repository, func() map[string]definitionmodel.ObjectSchema { return objects })
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{
		Key: "report-reader", Permissions: []string{"customer.read", "customer.export"}, RecordScope: "all_records",
		FieldPolicies: []accessfixture.FieldPolicyFixture{
			{ObjectKey: "customer", FieldKey: "name", Read: true, Export: true},
			{ObjectKey: "customer", FieldKey: "status", Read: true, Export: true},
			{ObjectKey: "customer", FieldKey: "id", Read: true, Export: true},
			{ObjectKey: "customer", FieldKey: "created_at", Read: true, Export: true},
			{ObjectKey: "customer", FieldKey: "updated_at", Read: true, Export: true},
		},
	})

	resolved, err := adapter.ReportObjectForAction(t.Context(), principal, "customer", "read")
	if err != nil || resolved.Key != "customer" {
		t.Fatalf("object=%#v err=%v", resolved, err)
	}
	query := adapter.NormalizeReportListQuery(t.Context(), object, recordmodel.RecordListQuery{}, principal)
	if query.Page <= 0 || query.PageSize <= 0 {
		t.Fatalf("query=%#v", query)
	}
	if !adapter.CanAccessReportRecord(t.Context(), principal, object, repository.page.Items[0]) {
		t.Fatal("admin record access denied")
	}
	projected, err := adapter.ProjectReportRecordFields(t.Context(), principal, object, repository.page.Items)
	if err != nil || len(projected) != 1 || projected[0].ID != "customer-1" {
		t.Fatalf("projected=%#v err=%v", projected, err)
	}
	if masked, err := adapter.AuthorizeReportExportField(t.Context(), principal, "customer", "name"); err != nil || masked {
		t.Fatalf("name masked=%v err=%v", masked, err)
	}
	if masked, err := adapter.AuthorizeReportExportField(t.Context(), principal, "customer", "id"); err != nil || masked {
		t.Fatalf("id masked=%v err=%v", masked, err)
	}
	for _, fieldKey := range []string{"created_at", "updated_at"} {
		if masked, err := adapter.AuthorizeReportExportField(t.Context(), principal, "customer", fieldKey); err != nil || masked {
			t.Fatalf("%s masked=%v err=%v", fieldKey, masked, err)
		}
	}
	maskedPrincipal := principal
	accessfixture.Set(&maskedPrincipal, accessfixture.Bundle{
		Key: "masked-report-reader", Permissions: []string{"customer.export", "customer.read"}, RecordScope: "all_records",
		FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "name", Read: true, Export: true, Masked: true}},
	})
	if masked, err := adapter.AuthorizeReportExportField(t.Context(), maskedPrincipal, "customer", "name"); err != nil || !masked {
		t.Fatalf("masked name=%v err=%v", masked, err)
	}
	if err := adapter.AuthorizeReportObjectSQLField(t.Context(), maskedPrincipal, object, "name"); apperror.CodeOf(err) != "backend.report.object_sql_field_masked" {
		t.Fatalf("object SQL masked field err=%v", err)
	}
	contextualPrincipal := maskedPrincipal
	accessfixture.Set(&contextualPrincipal, accessfixture.Bundle{
		Key: "contextual-report-reader", Permissions: []string{"customer.export", "customer.read"}, RecordScope: "all_records",
		FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "name", Read: true, Policies: []accessfixture.FieldRuleFixture{{Key: "owner-only", Actions: []string{"read"}, Effect: "allow"}}}},
	})
	if err := adapter.AuthorizeReportObjectSQLField(t.Context(), contextualPrincipal, object, "name"); apperror.CodeOf(err) != "backend.report.object_sql_field_contextual" {
		t.Fatalf("object SQL contextual field err=%v", err)
	}
	readDeniedPrincipal := maskedPrincipal
	accessfixture.Set(&readDeniedPrincipal, accessfixture.Bundle{
		Key: "read-denied", Permissions: []string{"customer.read"}, RecordScope: "all_records",
		FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "name", Read: false}},
	})
	if err := adapter.AuthorizeReportObjectSQLField(t.Context(), readDeniedPrincipal, object, "name"); apperror.CodeOf(err) != "backend.report.object_sql_field_denied" {
		t.Fatalf("object SQL unreadable field err=%v", err)
	}
	if err := adapter.AuthorizeReportObjectSQLField(t.Context(), principal, object, "retired"); apperror.CodeOf(err) != "backend.report.object_sql_field_not_found" {
		t.Fatalf("object SQL disabled field err=%v", err)
	}
	deniedPrincipal := maskedPrincipal
	accessfixture.Set(&deniedPrincipal, accessfixture.Bundle{
		Key: "export-denied", Permissions: []string{"customer.read", "customer.export"}, RecordScope: "all_records",
		FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "name", Read: true, Export: false}, {ObjectKey: "customer", FieldKey: "id", Read: true, Export: false}},
	})
	if _, err := adapter.AuthorizeReportExportField(t.Context(), deniedPrincipal, "customer", "name"); err == nil {
		t.Fatal("denied business field accepted")
	}
	if _, err := adapter.AuthorizeReportExportField(t.Context(), deniedPrincipal, "customer", "id"); err == nil {
		t.Fatal("denied system field accepted")
	}
	if _, err := adapter.AuthorizeReportExportField(t.Context(), principal, "customer", "missing"); err == nil {
		t.Fatal("missing export field accepted")
	}
	if _, err := adapter.AuthorizeReportExportField(t.Context(), principal, "customer", "retired"); err == nil {
		t.Fatal("disabled export field accepted")
	}
	if _, err := adapter.AuthorizeReportExportField(t.Context(), principal, "missing", "id"); err == nil {
		t.Fatal("missing export object accepted")
	}
	page, err := adapter.ListReportRecords(t.Context(), "workspace-a", object, recordmodel.RecordListQuery{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	if _, err := adapter.GetReportRecord(t.Context(), "missing", "record", principal); err == nil {
		t.Fatal("missing object get accepted")
	}
	if _, err := adapter.ListReportRecordsForPrincipal(t.Context(), "missing", recordmodel.RecordListQuery{}, principal); err == nil {
		t.Fatal("missing object list accepted")
	}
	if _, err := adapter.CreateReportRecord(t.Context(), "missing", map[string]any{}, "create-key", principal); err == nil {
		t.Fatal("missing object create accepted")
	}
	if _, err := adapter.UpdateReportRecord(t.Context(), "missing", "record", map[string]any{}, "update-key", principal); err == nil {
		t.Fatal("missing object update accepted")
	}
	if err := adapter.TransitionReportExportAuditStatus(t.Context(), "workspace-a", "customer", "audit-1", "status", "prepared", "denied"); err != nil {
		t.Fatalf("transition status: %v", err)
	}
	if repository.updated.ID != "audit-1" || repository.updated.Data["status"] != "denied" || repository.updateConditions["status"] != "prepared" {
		t.Fatalf("updated=%#v conditions=%#v", repository.updated, repository.updateConditions)
	}
	for name, candidate := range map[string]func() error{
		"missing object": func() error {
			return adapter.TransitionReportExportAuditStatus(t.Context(), "workspace-a", "missing", "audit-1", "status", "prepared", "denied")
		},
		"missing field": func() error {
			return adapter.TransitionReportExportAuditStatus(t.Context(), "workspace-a", "customer", "audit-1", "missing", "prepared", "denied")
		},
		"empty record": func() error {
			return adapter.TransitionReportExportAuditStatus(t.Context(), "workspace-a", "customer", "", "status", "prepared", "denied")
		},
		"empty from": func() error {
			return adapter.TransitionReportExportAuditStatus(t.Context(), "workspace-a", "customer", "audit-1", "status", "", "denied")
		},
		"empty target": func() error {
			return adapter.TransitionReportExportAuditStatus(t.Context(), "workspace-a", "customer", "audit-1", "status", "prepared", "")
		},
		"nil adapter": func() error {
			return (&ReportRecordAdapter{}).TransitionReportExportAuditStatus(t.Context(), "workspace-a", "customer", "audit-1", "status", "prepared", "denied")
		},
	} {
		if err := candidate(); err == nil {
			t.Fatalf("%s transition accepted", name)
		}
	}
	repository.updateErr = errors.New("update failed")
	if err := adapter.TransitionReportExportAuditStatus(t.Context(), "workspace-a", "customer", "audit-1", "status", "prepared", "denied"); !errors.Is(err, repository.updateErr) {
		t.Fatalf("update error=%v", err)
	}
}

func TestReportRecordAdapterRejectsDatasetPushdownForCLS(t *testing.T) {
	adapter := &ReportRecordAdapter{}
	object := definitionmodel.ObjectSchema{Key: "entry", Fields: []definitionmodel.FieldSchema{{Key: "amount", Type: "currency"}}}
	plain := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "entry", FieldKey: "amount", Read: true}}})
	if !adapter.CanPushdownReportDataset(t.Context(), plain, []definitionmodel.ObjectSchema{object}) {
		t.Fatal("unrestricted fields should permit dataset pushdown")
	}
	denied := plain
	accessfixture.Set(&denied, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "entry", FieldKey: "amount", Read: false}}})
	if adapter.CanPushdownReportDataset(t.Context(), denied, []definitionmodel.ObjectSchema{object}) {
		t.Fatal("unreadable field must force projected fallback")
	}
	masked := plain
	accessfixture.Set(&masked, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "entry", FieldKey: "amount", Read: true, Masked: true}}})
	if adapter.CanPushdownReportDataset(t.Context(), masked, []definitionmodel.ObjectSchema{object}) {
		t.Fatal("masked field must force projected fallback")
	}
	contextual := plain
	accessfixture.Set(&contextual, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "entry", FieldKey: "amount", Read: true, Policies: []accessfixture.FieldRuleFixture{{Key: "owner-only", Actions: []string{"read"}, Effect: "allow"}}}}})
	if adapter.CanPushdownReportDataset(t.Context(), contextual, []definitionmodel.ObjectSchema{object}) {
		t.Fatal("contextual field policy must force projected fallback")
	}
	unrelated := plain
	accessfixture.Set(&unrelated, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "other", FieldKey: "amount", Policies: []accessfixture.FieldRuleFixture{{Key: "other"}}}, {ObjectKey: "entry", FieldKey: "amount", Read: true}}})
	if !adapter.CanPushdownReportDataset(t.Context(), unrelated, []definitionmodel.ObjectSchema{object}) {
		t.Fatal("unrelated or empty field policies should not disable pushdown")
	}
}
