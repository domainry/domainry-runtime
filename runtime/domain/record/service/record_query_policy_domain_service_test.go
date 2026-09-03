// Query-policy domain service tests.
package service

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"errors"
	"reflect"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestQueryPolicyServiceObjectPermissionErrorsRemainStructured(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer"}
	service := NewRecordQueryPolicyDomainService(RecordQueryPolicyDependencies{Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{object} }})
	_, err := service.ObjectForAction(principalmodel.Principal{}, "customer", "read")
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Kind != apperror.KindForbidden || appErr.Code != "backend.role.unknown" {
		t.Fatalf("err=%#v", err)
	}
	_, err = service.ObjectForAction(accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{}), "missing", "read")
	if !errors.As(err, &appErr) || appErr.Kind != apperror.KindNotFound || appErr.Code != "backend.object.not_found" {
		t.Fatalf("err=%#v", err)
	}
}

func TestQueryPolicyServiceNormalizesExplicitListQuery(t *testing.T) {
	service := NewRecordQueryPolicyDomainService(RecordQueryPolicyDependencies{})
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}, {Key: "owner_id", Type: "user"}}}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user-1", WorkspaceID: "workspace-1"}}, accessfixture.Bundle{
		Permissions:  []string{"customer.read"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all", Read: true}},
	})

	query := service.NormalizeListQuery(object, recordmodel.RecordListQuery{PageSize: 50, SearchFields: []string{"name"}, Filters: map[string]any{"owner_id": "user-1"}, Sort: []recordmodel.RecordSortRule{{Field: "name", Direction: "desc"}}}, principal)
	if query.Page != 1 || query.PageSize != 50 || !reflect.DeepEqual(query.SearchFields, []string{"name"}) {
		t.Fatalf("pagination/search normalization = %#v", query)
	}
	if got := query.Filters["owner_id"]; got != "user-1" {
		t.Fatalf("explicit owner filter = %#v", got)
	}
	if len(query.Sort) != 2 || query.Sort[0].Field != "name" || query.Sort[0].Direction != "desc" || query.Sort[1].Field != "id" || query.Sort[1].Direction != "asc" {
		t.Fatalf("sort normalization = %#v", query.Sort)
	}
	if query.AuthorizationMode != recordmodel.RecordQueryAuthorizationUnrestricted || query.RootObjectKey != "" || query.ScopeExpression != nil {
		t.Fatalf("authorization projection = %#v", query)
	}
}

func TestQueryPolicyServiceNormalizesBoundedInFilters(t *testing.T) {
	service := NewRecordQueryPolicyDomainService(RecordQueryPolicyDependencies{})
	object := definitionmodel.ObjectSchema{Key: "employee_profile", Fields: []definitionmodel.FieldSchema{{Key: "identity_user", Type: "relation", Config: map[string]any{"object_key": "identity_user"}}}}
	query := service.NormalizeListQuery(object, recordmodel.RecordListQuery{Filters: map[string]any{
		"identity_user__in": []any{"u2", "u1", "u1", ""},
		"id__in":            []string{"p2", "p1"},
		"unknown__in":       []any{"leak"},
	}}, accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{
		Permissions:  []string{"employee_profile.read"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "employee_profile", Scope: "all", Read: true}},
	}))
	if !reflect.DeepEqual(query.Filters["identity_user__in"], []any{"u2", "u1"}) || !reflect.DeepEqual(query.Filters["id__in"], []any{"p2", "p1"}) {
		t.Fatalf("normalized batch filters = %#v", query.Filters)
	}
	if _, exists := query.Filters["unknown__in"]; exists {
		t.Fatalf("unknown batch filter survived normalization: %#v", query.Filters)
	}
}

func TestQueryPolicyServiceAuthorizesCreateCandidateThroughPersistedRelationScope(t *testing.T) {
	order := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "warehouse_id", Type: "relation"}}}
	reservation := definitionmodel.ObjectSchema{Key: "reservation", Fields: []definitionmodel.FieldSchema{{Key: "order_id", Type: "relation", Config: map[string]any{"object_key": "order"}}}}
	predicate := &accessfixture.PredicateFixture{
		Operator: "in",
		Path: []accessfixture.RelationSegmentFixture{{
			Direction: "forward", RelationFieldKey: "order_id", TargetObjectKey: "order",
		}},
		FieldKey: "warehouse_id", ValueSource: "literal", Values: []string{"warehouse-north"},
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{
		Permissions:  []string{"reservation.read", "reservation.update"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "reservation", Read: true, Write: true, Predicate: predicate}},
	})
	called := false
	service := NewRecordQueryPolicyDomainService(RecordQueryPolicyDependencies{
		Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{reservation, order} },
		CandidateScopeMatches: func(_ context.Context, workspaceID string, candidate recordmodel.Record, expression recordmodel.RecordScopeExpression) (bool, error) {
			called = true
			if workspaceID != "workspace-a" || candidate.Data["order_id"] != "order-north" || len(expression.Path) != 1 || !reflect.DeepEqual(expression.Values, []string{"warehouse-north"}) {
				t.Fatalf("candidate scope workspace=%s candidate=%#v expression=%#v", workspaceID, candidate, expression)
			}
			return true, nil
		},
	})
	allowed, err := service.CanWriteCandidateScope(t.Context(), principal, reservation, recordmodel.Record{ID: "reservation-new", Data: map[string]any{"order_id": "order-north"}})
	if err != nil || !allowed || !called {
		t.Fatalf("allowed=%v called=%v err=%v", allowed, called, err)
	}
	called = false
	allowed, err = service.CanAccessPersistedRecordScope(t.Context(), principal, reservation, recordmodel.Record{ID: "reservation-existing", Data: map[string]any{"order_id": "order-north"}}, false)
	if err != nil || !allowed || !called {
		t.Fatalf("persisted allowed=%v called=%v err=%v", allowed, called, err)
	}

	service = NewRecordQueryPolicyDomainService(RecordQueryPolicyDependencies{Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{reservation, order} }})
	allowed, err = service.CanWriteCandidateScope(t.Context(), principal, reservation, recordmodel.Record{ID: "reservation-new", Data: map[string]any{"order_id": "order-north"}})
	if allowed || apperror.CodeOf(err) != "backend.policy.candidate_scope_evaluator_unavailable" {
		t.Fatalf("missing evaluator allowed=%v err=%v", allowed, err)
	}
}
