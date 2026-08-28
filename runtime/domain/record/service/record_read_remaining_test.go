package service

import (
	"context"
	"errors"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"testing"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type readPolicyErrorProbe struct {
	objectErr   error
	snapshotErr error
}

func (p readPolicyErrorProbe) ObjectForAction(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
	return definitionmodel.ObjectSchema{Key: "employee_profile"}, p.objectErr
}
func (p readPolicyErrorProbe) EnsureReportSnapshotAccess(definitionmodel.ObjectSchema, string, principalmodel.Principal) error {
	return p.snapshotErr
}
func (readPolicyErrorProbe) NormalizeListQuery(_ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
	return query
}
func (readPolicyErrorProbe) CanAccessRecord(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
	return true
}

func TestReadServicePropagatesPolicyRepositoryAndReferenceErrors(t *testing.T) {
	policyErr := errors.New("policy failed")
	snapshotErr := errors.New("snapshot failed")
	for _, test := range []struct {
		name   string
		policy RecordQueryPolicy
		repo   *readRepositoryProbe
		want   error
	}{
		{name: "object", policy: readPolicyErrorProbe{objectErr: policyErr}, repo: &readRepositoryProbe{}, want: policyErr},
		{name: "snapshot", policy: readPolicyErrorProbe{snapshotErr: snapshotErr}, repo: &readRepositoryProbe{}, want: snapshotErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := NewRecordReadDomainService(RecordReadDependencies{Repository: test.repo, Policy: test.policy})
			if _, err := service.ListRecords(t.Context(), "employee_profile", recordmodel.RecordListQuery{}, principalmodel.Principal{}); !errors.Is(err, test.want) {
				t.Fatalf("list error=%v want=%v", err, test.want)
			}
			if _, err := service.GetRecord(t.Context(), "employee_profile", "profile-1", principalmodel.Principal{}); !errors.Is(err, test.want) {
				t.Fatalf("get error=%v want=%v", err, test.want)
			}
		})
	}
	repository := &readRepositoryProbe{err: errors.New("store failed")}
	service := NewRecordReadDomainService(RecordReadDependencies{Repository: repository, Policy: readPolicyErrorProbe{}})
	if _, err := service.ListRecords(t.Context(), "employee_profile", recordmodel.RecordListQuery{}, principalmodel.Principal{}); err == nil {
		t.Fatal("list repository error was ignored")
	}

	service = NewRecordReadDomainService(RecordReadDependencies{
		Repository: repository,
		Policy:     readPolicyErrorProbe{objectErr: policyErr},
		IdentityProfileExtensions: func() []profilebindingmodel.Binding {
			return []profilebindingmodel.Binding{{ObjectKey: "employee_profile", IdentityRelationField: "identity_user"}}
		},
	})
	if references, err := service.IdentityProfileReferences(t.Context(), "user-1", principalmodel.Principal{}); !errors.Is(err, policyErr) || references != nil {
		t.Fatalf("references=%#v err=%v", references, err)
	}
}

func TestReadServiceContextualPolicyAuditAndErrorEdges(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "secret", Type: "text"}}}
	record := recordmodel.Record{ID: "customer-1", Data: map[string]any{"secret": "classified"}}
	repository := &readRepositoryProbe{page: recordmodel.RecordPageResult{Items: []recordmodel.Record{record}, Total: 1}}
	denialRule := accessfixture.FieldRuleFixture{Key: "deny-secret", Actions: []string{"read"}, Effect: "deny", AuditDenial: true}
	principal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: object.Key, FieldKey: "secret", Read: true, Policies: []accessfixture.FieldRuleFixture{denialRule}}}})
	contextual := NewRecordContextualFieldPolicyDomainService(RecordContextualFieldPolicyDependencies{})
	audits := 0
	service := NewRecordReadDomainService(RecordReadDependencies{
		Repository: repository, Policy: readPolicyProbe{object: object, allow: true}, ContextualFieldPolicy: contextual,
		AuditFieldDenials: func(context.Context, definitionmodel.ObjectSchema, recordmodel.Record, string, []RecordFieldPolicyDecision, principalmodel.Principal) {
			audits++
		},
	})
	page, err := service.ListRecords(t.Context(), object.Key, recordmodel.RecordListQuery{ScopeExpression: &recordmodel.RecordScopeExpression{Operator: "in", FieldKey: "id"}}, principal)
	if err != nil || len(page.Items) != 1 || page.Items[0].Data["secret"] != nil || audits != 1 {
		t.Fatalf("page=%#v audits=%d err=%v", page, audits, err)
	}
	if _, err := service.applyFieldPolicy(t.Context(), principal, object, record, "read"); err != nil || audits != 2 {
		t.Fatalf("single audits=%d err=%v", audits, err)
	}
	noAudit := NewRecordReadDomainService(RecordReadDependencies{Repository: repository, Policy: readPolicyProbe{object: object, allow: true}, ContextualFieldPolicy: contextual})
	if _, err := noAudit.ListRecords(t.Context(), object.Key, recordmodel.RecordListQuery{}, principal); err != nil {
		t.Fatalf("list denial without audit err=%v", err)
	}
	if _, err := noAudit.applyFieldPolicy(t.Context(), principal, object, record, "read"); err != nil {
		t.Fatalf("single denial without audit err=%v", err)
	}
	allowedPrincipal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: object.Key, FieldKey: "secret", Read: true}}})
	if _, err := service.ListRecords(t.Context(), object.Key, recordmodel.RecordListQuery{}, allowedPrincipal); err != nil {
		t.Fatalf("list without denials err=%v", err)
	}
	if _, err := service.applyFieldPolicy(t.Context(), allowedPrincipal, object, record, "read"); err != nil {
		t.Fatalf("single without denials err=%v", err)
	}

	invalid := principal
	accessfixture.Mutate(&invalid, func(role *accessfixture.Bundle) {
		role.FieldPolicies[0].Policies[0].Predicate = &accessfixture.PredicateFixture{Operator: "invalid"}
	})
	if _, err := service.ListRecords(t.Context(), object.Key, recordmodel.RecordListQuery{}, invalid); err == nil {
		t.Fatal("list contextual policy error ignored")
	}
	if _, err := service.applyFieldPolicy(t.Context(), invalid, object, record, "read"); err == nil {
		t.Fatal("single contextual policy error ignored")
	}
}
