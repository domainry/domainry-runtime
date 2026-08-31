package record_test

import (
	"context"
	"fmt"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/query"
)

func TestContextualFieldPolicyEndToEndKeepsReadExportReportAuditAndWriteAligned(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	member := definitionmodel.ObjectSchema{Key: "member", Fields: []definitionmodel.FieldSchema{
		{Key: "name", Type: "text"}, {Key: "phone", Type: "phone"}, {Key: "health_note", Type: "select"},
	}}
	pack := definitionmodel.ObjectSchema{Key: "package", Fields: []definitionmodel.FieldSchema{
		{Key: "member_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "member"}, Config: map[string]any{"indexed": true}},
		{Key: "coach_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "coach"}, Config: map[string]any{"indexed": true}},
	}}
	coach := definitionmodel.ObjectSchema{Key: "coach", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	objects := []definitionmodel.ObjectSchema{member, pack, coach}
	for _, object := range objects {
		createRelationRLSTable(t, store, object)
	}
	repository := recordStore(store)
	insertRelationRLSRecord(t, repository, member, "member-1", map[string]any{"name": "Alice", "phone": "10000000004", "health_note": "allergy-a"})
	insertRelationRLSRecord(t, repository, member, "member-2", map[string]any{"name": "Bob", "phone": "10000000005", "health_note": "allergy-b"})
	insertRelationRLSRecord(t, repository, pack, "package-1", map[string]any{"member_id": "member-1", "coach_id": "coach-1"})
	insertRelationRLSRecord(t, repository, pack, "package-2", map[string]any{"member_id": "member-2", "coach_id": "coach-2"})

	relationship := &accessfixture.PredicateFixture{Operator: "eq", Path: []accessfixture.RelationSegmentFixture{{Direction: "reverse", RelationFieldKey: "member_id", TargetObjectKey: "package"}}, FieldKey: "coach_id", ValueSource: "actor_claim", ClaimKey: "business_profile_id"}
	allowRelated := accessfixture.FieldRuleFixture{Key: "related-clear", Priority: 100, Actions: []string{"read", "export", "report", "audit", "write"}, Effect: "allow", Predicate: relationship}
	maskOther := accessfixture.FieldRuleFixture{Key: "other-mask", Priority: 10, Actions: []string{"read", "export", "report", "audit"}, Effect: "mask", MaskStrategy: &accessfixture.MaskFixture{Type: "phone"}}
	hideOther := accessfixture.FieldRuleFixture{Key: "other-hide", Priority: 10, Actions: []string{"read", "export", "report", "audit"}, Effect: "hide", AuditDenial: true}
	role := accessfixture.Bundle{Key: "coach", Permissions: []string{"member.read", "member.export"}, RecordScope: "all_records", DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "member", Scope: "all_records", Read: true}}, FieldPolicies: []accessfixture.FieldPolicyFixture{
		{ObjectKey: "member", FieldKey: "name", Read: true, Export: true},
		{ObjectKey: "member", FieldKey: "phone", Read: true, Export: true, Policies: []accessfixture.FieldRuleFixture{allowRelated, maskOther}},
		{ObjectKey: "member", FieldKey: "health_note", Read: true, Export: true, Policies: []accessfixture.FieldRuleFixture{allowRelated, hideOther}},
	}}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary", UserID: "identity-coach-1"}, ActiveBusinessProfile: &profilebindingmodel.Reference{RecordID: "coach-1"}}, role)
	policy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{Objects: func() []definitionmodel.ObjectSchema { return objects }})
	fieldPolicy := recordservice.NewRecordContextualFieldPolicyDomainService(recordservice.RecordContextualFieldPolicyDependencies{Repository: repository, Objects: func() []definitionmodel.ObjectSchema { return objects }})
	auditDenials := []string{}
	reader := recordservice.NewRecordReadDomainService(recordservice.RecordReadDependencies{
		Repository: repository, Policy: policy, ContextualFieldPolicy: fieldPolicy,
		AuditFieldDenials: func(_ context.Context, _ definitionmodel.ObjectSchema, record recordmodel.Record, _ string, decisions []recordservice.RecordFieldPolicyDecision, _ principalmodel.Principal) {
			for _, decision := range decisions {
				auditDenials = append(auditDenials, record.ID+":"+decision.FieldKey+":"+decision.RuleKey)
			}
		},
	})
	page, err := reader.ListRecords(t.Context(), "member", recordmodel.RecordListQuery{Page: 1, PageSize: 20}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].Data["phone"] != "10000000004" || page.Items[0].Data["health_note"] != "allergy-a" {
		t.Fatalf("related member should be clear: %#v", page.Items)
	}
	if page.Items[1].Data["phone"] == "10000000005" || page.Items[1].Data["health_note"] != nil {
		t.Fatalf("unrelated member leaked sensitive fields: %#v", page.Items[1].Data)
	}
	if !containsText(auditDenials, "member-2:health_note:other-hide") {
		t.Fatalf("missing policy-driven CLS security audit: %#v", auditDenials)
	}
	if err := fieldPolicy.ValidateWrite(t.Context(), principal, member, recordmodel.Record{ID: "member-1", Data: map[string]any{"health_note": "allergy-a"}}, map[string]any{"health_note": "updated-a"}); err != nil {
		t.Fatalf("related coach contextual write denied: %v", err)
	}
	if err := fieldPolicy.ValidateWrite(t.Context(), principal, member, recordmodel.Record{ID: "member-2", Data: map[string]any{"health_note": "allergy-b"}}, map[string]any{"health_note": "updated-b"}); err == nil {
		t.Fatal("unrelated coach contextual write should be denied")
	}

	objectMap := map[string]definitionmodel.ObjectSchema{"member": member, "package": pack, "coach": coach}
	exporter := recordapplication.NewRecordExportApplicationService(recordapplication.RecordExportDependencies{
		Repository: repository, Objects: func() map[string]definitionmodel.ObjectSchema { return objectMap }, NormalizeQuery: policy.NormalizeListQuery, CanAccess: policy.CanAccessRecord,
		ProjectRecords: func(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, records []recordmodel.Record, action string) ([]recordmodel.Record, error) {
			projected, _, projectErr := fieldPolicy.ApplyReadPage(ctx, principal, object, records, action)
			return projected, projectErr
		},
	})
	csv, _, err := exporter.Export(t.Context(), "member", principal)
	if err != nil || !strings.Contains(string(csv), "10000000004") || strings.Contains(string(csv), "10000000005") || strings.Contains(string(csv), "allergy-b") {
		t.Fatalf("contextual export leaked or hid wrong value: csv=%s err=%v", csv, err)
	}

	reportAccess := contextualFieldReportAccess{policy: policy, fields: fieldPolicy}
	report := reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{{Key: "member-sensitive", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "member", Alias: "member"}, Dimensions: []reportmodel.ReportDatasetDimension{{Key: "health_note", Field: reportmodel.ReportDatasetField{SourceAlias: "member", FieldKey: "health_note"}}}}}}
		},
		Access: reportAccess, Records: contextualFieldReportRecords{repository: repository},
	})
	summary, err := report.Summary(t.Context(), "member-sensitive", principal)
	if err != nil || strings.Contains(fmt.Sprint(summary), "allergy-b") || !strings.Contains(fmt.Sprint(summary), "allergy-a") {
		t.Fatalf("contextual report projection mismatch: summary=%#v err=%v", summary, err)
	}

	auditProjection, _, err := fieldPolicy.ApplyReadPage(t.Context(), principal, member, []recordmodel.Record{
		{ID: "member-1", Data: map[string]any{"phone": "10000000004", "health_note": "allergy-a"}},
		{ID: "member-2", Data: map[string]any{"phone": "10000000005", "health_note": "allergy-b"}},
	}, "audit")
	if err != nil || auditProjection[0].Data["health_note"] != "allergy-a" || auditProjection[1].Data["health_note"] != nil || auditProjection[1].Data["phone"] == "10000000005" {
		t.Fatalf("audit presentation CLS mismatch: %#v err=%v", auditProjection, err)
	}

	selfPredicate := &accessfixture.PredicateFixture{Operator: "eq", FieldKey: "id", ValueSource: "actor_claim", ClaimKey: "business_profile_id"}
	selfAllow := accessfixture.FieldRuleFixture{Key: "self-clear", Priority: 100, Actions: []string{"read", "export", "report", "audit"}, Effect: "allow", Predicate: selfPredicate}
	memberRole := accessfixture.Bundle{Key: "member", Permissions: []string{"member.read", "member.export"}, RecordScope: "custom", DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "member", Scope: "custom", Read: true, AuditDenial: true, Predicate: selfPredicate}}, FieldPolicies: []accessfixture.FieldPolicyFixture{
		{ObjectKey: "member", FieldKey: "name", Read: true, Export: true},
		{ObjectKey: "member", FieldKey: "phone", Read: true, Export: true, Policies: []accessfixture.FieldRuleFixture{selfAllow, maskOther}},
		{ObjectKey: "member", FieldKey: "health_note", Read: true, Export: true, Policies: []accessfixture.FieldRuleFixture{selfAllow, hideOther}},
	}}
	memberPrincipal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary", UserID: "identity-member-1"}, ActiveBusinessProfile: &profilebindingmodel.Reference{RecordID: "member-1"}}, memberRole)
	scopeDenials := []string{}
	memberReader := recordservice.NewRecordReadDomainService(recordservice.RecordReadDependencies{
		Repository: repository, Policy: policy, ContextualFieldPolicy: fieldPolicy,
		AuditScopeDenial: func(_ context.Context, object definitionmodel.ObjectSchema, recordID string, _ principalmodel.Principal) {
			scopeDenials = append(scopeDenials, object.Key+":"+recordID)
		},
	})
	selfPage, err := memberReader.ListRecords(t.Context(), "member", recordmodel.RecordListQuery{Page: 1, PageSize: 20, Filters: map[string]any{"id__in": []any{"member-1", "member-2"}}}, memberPrincipal)
	if err != nil || selfPage.Total != 1 || len(selfPage.Items) != 1 || selfPage.Items[0].ID != "member-1" || selfPage.Items[0].Data["phone"] != "10000000004" || selfPage.Items[0].Data["health_note"] != "allergy-a" {
		t.Fatalf("member self RLS/CLS mismatch: page=%#v err=%v", selfPage, err)
	}
	if _, err := memberReader.GetRecord(t.Context(), "member", "member-2", memberPrincipal); err == nil {
		t.Fatal("member direct ID guess should be concealed")
	}
	if !containsText(scopeDenials, "member:member-2") {
		t.Fatalf("configured RLS detail denial was not audited: %#v", scopeDenials)
	}
}

type contextualFieldReportAccess struct {
	policy *recordservice.RecordQueryPolicyDomainService
	fields *recordservice.RecordContextualFieldPolicyDomainService
}

func (a contextualFieldReportAccess) ReportObjectForAction(_ context.Context, principal principalmodel.Principal, objectKey, action string) (definitionmodel.ObjectSchema, error) {
	return a.policy.ObjectForAction(principal, objectKey, action)
}

func (a contextualFieldReportAccess) NormalizeReportListQuery(_ context.Context, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, principal principalmodel.Principal) recordmodel.RecordListQuery {
	return a.policy.NormalizeListQuery(object, query, principal)
}

func (a contextualFieldReportAccess) CanAccessReportRecord(_ context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record) bool {
	return a.policy.CanAccessRecord(principal, object, record)
}

func (a contextualFieldReportAccess) ProjectReportRecordFields(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, records []recordmodel.Record) ([]recordmodel.Record, error) {
	projected, _, err := a.fields.ApplyReadPage(ctx, principal, object, records, "report")
	return projected, err
}

type contextualFieldReportRecords struct {
	repository recordrepository.RecordRepository
}

func (r contextualFieldReportRecords) ListReportRecords(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return r.repository.ListRecords(ctx, workspaceID, object, query)
}

func containsText(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
