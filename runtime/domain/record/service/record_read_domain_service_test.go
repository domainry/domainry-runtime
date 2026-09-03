// Read domain service tests.
package service

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type readPolicyProbe struct {
	object definitionmodel.ObjectSchema
	allow  bool
}

func (p readPolicyProbe) ObjectForAction(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
	return p.object, nil
}
func (p readPolicyProbe) EnsureReportSnapshotAccess(definitionmodel.ObjectSchema, string, principalmodel.Principal) error {
	return nil
}
func (p readPolicyProbe) NormalizeListQuery(_ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
	return query
}
func (p readPolicyProbe) CanAccessRecord(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
	return p.allow
}

type readRepositoryProbe struct {
	recordrepository.RecordRepository
	page      recordmodel.RecordPageResult
	record    recordmodel.Record
	found     bool
	err       error
	lastQuery recordmodel.RecordListQuery
}

func (r *readRepositoryProbe) ListRecords(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	r.lastQuery = query
	if len(r.page.Items) == 0 && r.found {
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{r.record}, Total: 1}, r.err
	}
	return r.page, r.err
}

func (r *readRepositoryProbe) GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	return r.record, r.found, r.err
}

type identityDirectoryNoop struct{}

func (identityDirectoryNoop) FindUser(context.Context, identitysdk.UserLookup) (identitysdk.User, bool, error) {
	return identitysdk.User{}, false, nil
}
func (identityDirectoryNoop) FindOrganizationUnit(context.Context, identitysdk.OrganizationUnitLookup) (identitysdk.OrganizationUnit, bool, error) {
	return identitysdk.OrganizationUnit{}, false, nil
}
func (identityDirectoryNoop) ListUsers(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.User, error) {
	return nil, nil
}
func (identityDirectoryNoop) ListRoles(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.Role, error) {
	return nil, nil
}
func (identityDirectoryNoop) ListUserRoleAssignments(context.Context, identitysdk.UserRoleAssignmentQuery) ([]identitysdk.UserRoleAssignment, error) {
	return nil, nil
}

func TestReadServiceDoesNotLeakFilteredRecordCount(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "sales_order"}
	repository := &readRepositoryProbe{page: recordmodel.RecordPageResult{
		Items:   []recordmodel.Record{{ID: "order-1"}},
		Total:   1,
		HasNext: true,
	}}
	service := NewRecordReadDomainService(RecordReadDependencies{
		Repository: repository,
		Policy:     readPolicyProbe{object: object, allow: false},
	})

	page, err := service.ListRecords(t.Context(), object.Key, recordmodel.RecordListQuery{}, principalmodel.Principal{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 || page.Total != 0 || page.HasNext {
		t.Fatalf("filtered page leaked inaccessible count: %#v", page)
	}
}

func TestReadServiceGetRecordPreservesStructuredErrors(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer"}
	tests := []struct {
		name       string
		repository *readRepositoryProbe
		allow      bool
		kind       apperror.ErrorKind
		code       string
	}{
		{name: "not found", repository: &readRepositoryProbe{}, allow: true, kind: apperror.KindNotFound, code: "backend.record.not_found"},
		{name: "outside scope is concealed", repository: &readRepositoryProbe{}, kind: apperror.KindNotFound, code: "backend.record.not_found"},
		{name: "repository", repository: &readRepositoryProbe{err: errors.New("store unavailable")}, allow: true, kind: apperror.KindInternal, code: "backend.internal"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewRecordReadDomainService(RecordReadDependencies{Repository: test.repository, Policy: readPolicyProbe{object: object, allow: test.allow}})
			_, err := service.GetRecord(t.Context(), object.Key, "customer-1", principalmodel.Principal{})
			var appErr *apperror.AppError
			if !errors.As(err, &appErr) || appErr.Kind != test.kind || appErr.Code != test.code {
				t.Fatalf("error = %#v, want kind=%s code=%s", err, test.kind, test.code)
			}
		})
	}
}

func TestReadServiceActionReadsPreserveSensitiveBusinessFieldsAfterScopeAuthorization(t *testing.T) {
	object := definitionmodel.ObjectSchema{
		Key: "refund",
		Fields: []definitionmodel.FieldSchema{
			{Key: "amount", Type: "number", Required: true, Config: map[string]any{"sensitive": true}},
		},
	}
	record := recordmodel.Record{ID: "refund-1", Data: map[string]any{"amount": "128.50"}}
	repository := &readRepositoryProbe{
		found:  true,
		record: record,
		page:   recordmodel.RecordPageResult{Items: []recordmodel.Record{record}, Total: 1},
	}
	service := NewRecordReadDomainService(RecordReadDependencies{
		Repository: repository,
		Policy:     readPolicyProbe{object: object, allow: true},
	})
	principal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: object.Key, Read: true, Scope: "all"}},
	})

	external, err := service.GetRecord(t.Context(), object.Key, record.ID, principal)
	if err != nil {
		t.Fatal(err)
	}
	if external.Data["amount"] == "128.50" {
		t.Fatalf("external read exposed sensitive amount: %#v", external.Data)
	}
	internal, err := service.GetRecordForAction(t.Context(), object.Key, record.ID, principal)
	if err != nil {
		t.Fatal(err)
	}
	if internal.Data["amount"] != "128.50" {
		t.Fatalf("Action read lost canonical amount: %#v", internal.Data)
	}
	page, err := service.ListRecordsForAction(t.Context(), object.Key, recordmodel.RecordListQuery{}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Data["amount"] != "128.50" {
		t.Fatalf("Action list lost canonical amount: %#v", page.Items)
	}
}

func TestReadServiceAuditsConfiguredDetailScopeDenialOnly(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer"}
	audited := []string{}
	service := NewRecordReadDomainService(RecordReadDependencies{
		Repository: &readRepositoryProbe{},
		Policy:     readPolicyProbe{object: object, allow: true},
		AuditScopeDenial: func(_ context.Context, gotObject definitionmodel.ObjectSchema, recordID string, _ principalmodel.Principal) {
			audited = append(audited, gotObject.Key+":"+recordID)
		},
	})
	principal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{Permissions: []string{"customer.read"}, DataPolicies: []accessfixture.DataPolicyFixture{{
		ObjectKey: object.Key, Read: true, AuditDenial: true,
	}}})
	if _, err := service.GetRecord(t.Context(), object.Key, "customer-1", principal); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("get error = %v", err)
	}
	if !reflect.DeepEqual(audited, []string{"customer:customer-1"}) {
		t.Fatalf("audited detail denials = %#v", audited)
	}
	if _, err := service.GetRecordForUpdate(t.Context(), object.Key, "customer-locked", principal); apperror.KindOf(err) != apperror.KindNotFound ||
		apperror.ParamsOf(err)["object_key"] != object.Key ||
		apperror.ParamsOf(err)["record_id"] != "customer-locked" {
		t.Fatalf("locking denial error = %#v", err)
	}
	if len(audited) != 1 {
		t.Fatalf("locking read synchronously wrote denial audit: %#v", audited)
	}
	if _, err := service.ListRecords(t.Context(), object.Key, recordmodel.RecordListQuery{}, principal); err != nil {
		t.Fatal(err)
	}
	if len(audited) != 1 {
		t.Fatalf("empty list produced denial audit noise: %#v", audited)
	}
	accessfixture.Mutate(&principal, func(role *accessfixture.Bundle) {
		role.DataPolicies[0].AuditDenial = false
	})
	if _, err := service.GetRecord(t.Context(), object.Key, "customer-2", principal); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("get without audit policy error = %v", err)
	}
	if len(audited) != 1 {
		t.Fatalf("disabled policy produced denial audit: %#v", audited)
	}
}

func TestReadServiceBuildsIdentityProfileReferences(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "employee_profile", Fields: []definitionmodel.FieldSchema{{Key: "identity_user", Type: "relation"}}}
	repository := &readRepositoryProbe{page: recordmodel.RecordPageResult{Total: 3}}
	service := NewRecordReadDomainService(RecordReadDependencies{
		Repository: repository,
		Policy:     readPolicyProbe{object: object, allow: true},
		IdentityProfileExtensions: func() []profilebindingmodel.Binding {
			return []profilebindingmodel.Binding{{ObjectKey: object.Key, IdentityRelationField: "identity_user"}}
		},
	})

	references, err := service.IdentityProfileReferences(t.Context(), "u1", principalmodel.Principal{})
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 1 || references[0].RecordCount != 3 || references[0].DeletePolicy != "retain_and_block_identity_hard_delete" {
		t.Fatalf("references = %#v", references)
	}
}
