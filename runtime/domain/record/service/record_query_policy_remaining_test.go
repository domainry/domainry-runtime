package service

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
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
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: object.Key, Read: true, Write: true, Predicate: &accessfixture.PredicateFixture{Operator: "eq", FieldKey: "owner_id", ValueSource: "actor_claim", ClaimKey: "user_id"}}},
	})

	query := service.NormalizeListQuery(object, recordmodel.RecordListQuery{}, principal)
	if query.ScopeExpression == nil || query.AuthorizationDiagnostic != nil {
		t.Fatalf("explicit SDK predicate did not compile: %#v", query)
	}
	allowed, err := service.CanAccessPersistedRecordScope(t.Context(), principal, object, recordmodel.Record{Data: map[string]any{"owner_id": "user-1"}}, false)
	if err != nil || !allowed {
		t.Fatalf("SDK predicate denied matching candidate record allowed=%v err=%v", allowed, err)
	}

	accessfixture.Mutate(&principal, func(role *accessfixture.Bundle) {
		role.DataPolicies[0].Predicate = &accessfixture.PredicateFixture{Operator: "invalid"}
	})
	query = service.NormalizeListQuery(object, recordmodel.RecordListQuery{}, principal)
	if query.AuthorizationDiagnostic == nil {
		t.Fatalf("invalid custom expression query=%#v", query)
	}
	if allowed, err = service.CanAccessPersistedRecordScope(t.Context(), principal, object, recordmodel.Record{}, true); allowed || apperror.CodeOf(err) != "backend.policy.expression_invalid" {
		t.Fatalf("invalid write expression allowed=%v err=%v", allowed, err)
	}

	accessfixture.Mutate(&principal, func(role *accessfixture.Bundle) {
		role.DataPolicies[0].Predicate = &accessfixture.PredicateFixture{Operator: "eq", FieldKey: "owner_id", ValueSource: "actor_claim", ClaimKey: "user_id"}
	})
	query = service.NormalizeListQuery(object, recordmodel.RecordListQuery{}, principal)
	if query.ScopeExpression == nil || query.AuthorizationDiagnostic != nil {
		t.Fatalf("valid custom expression query=%#v", query)
	}
	allowed, err = service.CanAccessPersistedRecordScope(t.Context(), principal, object, recordmodel.Record{Data: map[string]any{"owner_id": "user-1"}}, false)
	if err != nil || !allowed {
		t.Fatalf("direct custom expression allowed=%v err=%v", allowed, err)
	}

	accessfixture.Set(&principal, accessfixture.Bundle{
		Permissions:  []string{"customer.read", "customer.update"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: object.Key, Scope: "all", Read: true, Write: true}},
	})
	for _, write := range []bool{false, true} {
		allowed, err = service.CanAccessPersistedRecordScope(t.Context(), principal, object, recordmodel.Record{Data: map[string]any{"owner_id": "user-1"}}, write)
		if err != nil || !allowed {
			t.Fatalf("fallback write=%v allowed=%v err=%v", write, allowed, err)
		}
	}
}

func TestQueryPolicyReportSnapshotAccessMatrix(t *testing.T) {
	snapshot := definitionmodel.ObjectSchema{Key: "sales_snapshot"}
	service := NewRecordQueryPolicyDomainService(RecordQueryPolicyDependencies{
		ReportObjects: func() map[string]struct{} { return map[string]struct{}{"sales_snapshot": {}} },
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
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "sales_snapshot", Read: true, Scope: "all"}},
	})
	if err := service.EnsureReportSnapshotAccess(snapshot, "read", permissionOnly); err != nil {
		t.Fatal(err)
	}
	if err := NewRecordQueryPolicyDomainService(RecordQueryPolicyDependencies{}).EnsureReportSnapshotAccess(snapshot, "read", principalmodel.Principal{}); err != nil {
		t.Fatalf("nil Report object projection must not classify snapshots: %v", err)
	}
}

func TestQueryPolicyFallbackAndReportingOwnerEdges(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "deal", Fields: []definitionmodel.FieldSchema{{Key: "owner", Type: "user"}}}
	service := NewRecordQueryPolicyDomainService(RecordQueryPolicyDependencies{})
	all := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "manager"}}, accessfixture.Bundle{
		Permissions:  []string{"deal.read", "deal.update"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "deal", Scope: "all", Read: true, Write: true}},
	})
	if !service.CanAccessRecord(all, object, recordmodel.Record{Data: map[string]any{"owner": "other"}}) {
		t.Fatal("all-record fallback denied read")
	}
	if !service.CanWriteRecordScope(all, object, map[string]any{"owner": "other"}) {
		t.Fatal("all-record fallback denied write")
	}

	if objects := service.objects(); objects != nil {
		t.Fatalf("nil object dependency returned %#v", objects)
	}
}
