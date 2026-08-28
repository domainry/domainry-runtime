package service

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestQueryPolicyObjectPermissionMatrix(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer"}
	service := NewRecordQueryPolicyDomainService(RecordQueryPolicyDependencies{
		Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{object} },
	})
	tests := []struct {
		name      string
		principal principalmodel.Principal
		kind      apperror.ErrorKind
		code      string
	}{
		{name: "permission", principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, kind: apperror.KindForbidden, code: "backend.permission.denied"},
		{name: "data", principal: accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"customer.read"}, DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer"}}}), kind: apperror.KindForbidden, code: "backend.permission.denied"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.ObjectForAction(test.principal, "customer", "read")
			assertRecordAppError(t, err, test.kind, test.code, nil)
		})
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{
		Permissions:  []string{"customer.read"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Read: true}},
	})
	if got, err := service.ObjectForAction(principal, " customer ", "read"); err != nil || got.Key != object.Key {
		t.Fatalf("object=%#v err=%v", got, err)
	}
}

func TestQueryPolicyCustomScopeCompilationAndFallbackEdges(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "owner_id", Type: "user"}}}
	service := NewRecordQueryPolicyDomainService(RecordQueryPolicyDependencies{Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{object} }})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user-1"}}, accessfixture.Bundle{
		Permissions:  []string{"customer.read", "customer.update"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: object.Key, Scope: "custom", Read: true, Write: true}},
	})

	query := service.NormalizeListQuery(object, recordmodel.RecordListQuery{}, principal)
	if query.ScopeExpression == nil || query.ScopeDiagnostic != nil {
		t.Fatalf("default custom fixture did not compile through SDK: %#v", query)
	}
	allowed, err := service.CanAccessPersistedRecordScope(t.Context(), principal, object, recordmodel.Record{}, false)
	if err != nil || !allowed {
		t.Fatalf("SDK unrestricted predicate denied candidate record allowed=%v err=%v", allowed, err)
	}

	accessfixture.Mutate(&principal, func(role *accessfixture.Bundle) {
		role.DataPolicies[0].Predicate = &accessfixture.PredicateFixture{Operator: "invalid"}
	})
	query = service.NormalizeListQuery(object, recordmodel.RecordListQuery{}, principal)
	if query.ScopeDiagnostic == nil {
		t.Fatalf("invalid custom expression query=%#v", query)
	}
	if allowed, err = service.CanAccessPersistedRecordScope(t.Context(), principal, object, recordmodel.Record{}, true); allowed || apperror.CodeOf(err) != "backend.policy.expression_invalid" {
		t.Fatalf("invalid write expression allowed=%v err=%v", allowed, err)
	}

	accessfixture.Mutate(&principal, func(role *accessfixture.Bundle) {
		role.DataPolicies[0].Predicate = &accessfixture.PredicateFixture{Operator: "eq", FieldKey: "owner_id", ValueSource: "actor_claim", ClaimKey: "user_id"}
	})
	query = service.NormalizeListQuery(object, recordmodel.RecordListQuery{}, principal)
	if query.ScopeExpression == nil || query.ScopeDiagnostic != nil {
		t.Fatalf("valid custom expression query=%#v", query)
	}
	allowed, err = service.CanAccessPersistedRecordScope(t.Context(), principal, object, recordmodel.Record{Data: map[string]any{"owner_id": "user-1"}}, false)
	if err != nil || !allowed {
		t.Fatalf("direct custom expression allowed=%v err=%v", allowed, err)
	}

	accessfixture.Set(&principal, accessfixture.Bundle{
		Permissions:  []string{"customer.read", "customer.update"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: object.Key, Scope: "all_records", Read: true, Write: true}},
	})
	for _, write := range []bool{false, true} {
		allowed, err = service.CanAccessPersistedRecordScope(t.Context(), principal, object, recordmodel.Record{Data: map[string]any{"owner_id": "user-1"}}, write)
		if err != nil || !allowed {
			t.Fatalf("fallback write=%v allowed=%v err=%v", write, allowed, err)
		}
	}
	blankReports := []reportmodel.ReportSchema{{Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: " ", SourceType: "snapshot"}}}}
	if keys := RecordReportObjectKeySet(blankReports); len(keys) != 0 {
		t.Fatalf("blank report keys=%#v", keys)
	}

}

func TestQueryPolicyReportSnapshotAccessMatrix(t *testing.T) {
	snapshot := definitionmodel.ObjectSchema{Key: "sales_snapshot"}
	service := NewRecordQueryPolicyDomainService(RecordQueryPolicyDependencies{
		Reports: func() []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{{Key: "sales", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "sales_snapshot", Alias: "sales_snapshot", SourceType: "snapshot"}}}}
		},
	})
	if err := service.EnsureReportSnapshotAccess(definitionmodel.ObjectSchema{Key: "customer"}, "read", principalmodel.Principal{}); err != nil {
		t.Fatal(err)
	}
	assertRecordAppError(t, service.EnsureReportSnapshotAccess(snapshot, "read", principalmodel.Principal{}), apperror.KindForbidden, "backend.role.unknown", nil)
	assertRecordAppError(t, service.EnsureReportSnapshotAccess(snapshot, "read", principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}), apperror.KindForbidden, "backend.report.permission_denied", nil)
	permissionOnly := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{
		Permissions:  []string{"sales_snapshot.read"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "sales_snapshot"}},
	})
	assertRecordAppError(t, service.EnsureReportSnapshotAccess(snapshot, "read", permissionOnly), apperror.KindForbidden, "backend.report.permission_denied", nil)
	accessfixture.Set(&permissionOnly, accessfixture.Bundle{
		Permissions:  []string{"sales_snapshot.read"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "sales_snapshot", Read: true, Scope: "all_records"}},
	})
	if err := service.EnsureReportSnapshotAccess(snapshot, "read", permissionOnly); err != nil {
		t.Fatal(err)
	}
	keys := RecordReportObjectKeySet(service.reports())
	if len(keys) != 1 {
		t.Fatalf("snapshot keys=%#v", keys)
	}
	if reports := NewRecordQueryPolicyDomainService(RecordQueryPolicyDependencies{}).reports(); reports != nil {
		t.Fatalf("nil report dependency returned %#v", reports)
	}
}

func TestQueryPolicyFallbackAndReportingOwnerEdges(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "deal", Fields: []definitionmodel.FieldSchema{{Key: "owner", Type: "user", Config: map[string]any{"scope_owner": true}}}}
	service := NewRecordQueryPolicyDomainService(RecordQueryPolicyDependencies{})
	all := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "manager"}}, accessfixture.Bundle{
		Permissions:  []string{"deal.read", "deal.update"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "deal", Scope: "all_records", Read: true, Write: true}},
	})
	if !service.CanAccessRecord(all, object, recordmodel.Record{Data: map[string]any{"owner": "other"}}) {
		t.Fatal("all-record fallback denied read")
	}
	if !service.CanWriteRecordScope(all, object, map[string]any{"owner": "other"}) {
		t.Fatal("all-record fallback denied write")
	}
	reporting := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "manager", ReportingUserIDs: []string{"employee"}}}, accessfixture.Bundle{
		Permissions:  []string{"deal.read", "deal.update"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "deal", Read: true, Write: true, Scope: "subordinates"}},
	})
	if service.CanAccessRecord(reporting, definitionmodel.ObjectSchema{Key: "deal"}, recordmodel.Record{Data: map[string]any{}}) {
		t.Fatal("reporting scope accepted object without owner field")
	}
	if service.CanAccessRecord(reporting, object, recordmodel.Record{Data: map[string]any{"owner": ""}}) {
		t.Fatal("reporting scope accepted empty owner")
	}
	if !service.CanAccessRecord(reporting, object, recordmodel.Record{Data: map[string]any{"owner": "employee"}}) {
		t.Fatal("reporting scope denied subordinate")
	}
	if objects := service.objects(); objects != nil {
		t.Fatalf("nil object dependency returned %#v", objects)
	}
	if views := service.views(); views != nil {
		t.Fatalf("nil view dependency returned %#v", views)
	}
}
