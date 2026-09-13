package composition

import (
	"context"
	"reflect"
	"testing"
	"time"

	auth "github.com/domainry/domainry-identity-sdk/authorization"
	evaluator "github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func TestRelationReadEffectScope(t *testing.T) {
	own := auth.Predicate{Fact: "id", Operator: auth.OperatorEqual, Value: "private-booking"}
	workflow := auth.Predicate{Fact: "id", Operator: auth.OperatorEqual, Value: "workflow-booking"}
	makePrincipal := func() principalmodel.Principal {
		principal := principalmodel.Principal{}
		principal.AccessBundle = &auth.AccessBundle{
			ContractVersion:       auth.CurrentPolicyBundleVersion,
			AuthorizationRevision: "revision",
			ExpiresAt:             time.Now().Add(time.Hour),
			Subject:               auth.Subject{WorkspaceID: "workspace", SubjectID: "worker"},
			FunctionGrants: []auth.FunctionGrant{
				{Resource: "ledger_entry", Action: "create", Effect: auth.EffectAllow},
				{Resource: "booking", Action: "read", Effect: auth.EffectAllow},
				{Resource: "booking", Action: "manage_booking_lifecycle", Effect: auth.EffectAllow},
			},
			DataPolicies: []auth.DataPolicy{
				{Key: "private", Resource: "booking", Action: "read", Effect: auth.EffectAllow, Predicate: own},
				{Key: "workflow", Resource: "booking", Action: "manage_booking_lifecycle", Effect: auth.EffectAllow, Predicate: workflow},
			},
		}
		return principal
	}
	invocation := recordmutation.MutationInvocation{
		Source: transactionmodel.MutationSourceAction, ActionResource: "booking", ActionOperation: "manage_booking_lifecycle", ReadEffectAuthority: map[string]bool{"booking": true},
	}
	actionContext := recordmutation.WithMutationInvocation(context.Background(), invocation)
	filter := func(principal principalmodel.Principal) []auth.Predicate {
		compiled, err := evaluator.CompileRecordFilter(*principal.AccessBundle, "booking", "read", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return compiled.Allow
	}
	object := definitionmodel.ObjectSchema{Key: "booking"}
	adapter := recordQueryPolicyAdapter{service: recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{
		Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{object} },
	})}

	t.Run("relation validator adapter uses the declared Action read scope", func(t *testing.T) {
		principal := makePrincipal()
		record := recordmodel.Record{ID: "workflow-booking"}
		allowed, err := adapter.canAccessPersistedRecord(actionContext, principal, object, record)
		if err != nil || !allowed {
			t.Fatalf("declared Action relation read allowed=%v error=%v", allowed, err)
		}
		allowed, err = adapter.canAccessPersistedRecord(context.Background(), principal, object, record)
		if err != nil || allowed {
			t.Fatalf("ordinary relation read allowed=%v error=%v", allowed, err)
		}
	})

	t.Run("explicit action read replaces browse scope without mutating caller", func(t *testing.T) {
		principal := makePrincipal()
		original := filter(principal)
		authorized, err := relationReadEffectPrincipal(actionContext, principal, "booking")
		if err != nil {
			t.Fatal(err)
		}
		if reflect.DeepEqual(filter(authorized), original) || !reflect.DeepEqual(filter(principal), original) {
			t.Fatal("relation scope was not isolated")
		}
		if !reflect.DeepEqual(filter(authorized), []auth.Predicate{workflow}) {
			t.Fatalf("unexpected relation scope: %+v", filter(authorized))
		}
	})

	t.Run("ordinary read remains private", func(t *testing.T) {
		principal := makePrincipal()
		authorized, err := relationReadEffectPrincipal(context.Background(), principal, "booking")
		if err != nil || authorized.AccessBundle != principal.AccessBundle {
			t.Fatal("ordinary scope changed", err)
		}
	})

	t.Run("undeclared reference does not inherit authority", func(t *testing.T) {
		principal := makePrincipal()
		authorized, err := relationReadEffectPrincipal(actionContext, principal, "member")
		if err != nil || authorized.AccessBundle != principal.AccessBundle {
			t.Fatal("undeclared reference gained authority", err)
		}
	})

	t.Run("another action does not borrow effect", func(t *testing.T) {
		principal := makePrincipal()
		other := invocation
		other.ActionOperation = "cancel"
		other.ReadEffectAuthority = nil
		authorized, err := relationReadEffectPrincipal(recordmutation.WithMutationInvocation(context.Background(), other), principal, "booking")
		if err != nil || authorized.AccessBundle != principal.AccessBundle {
			t.Fatal("unrelated action gained authority", err)
		}
	})

	t.Run("explicit function deny stays denied", func(t *testing.T) {
		principal := makePrincipal()
		principal.AccessBundle.FunctionGrants = append(principal.AccessBundle.FunctionGrants, auth.FunctionGrant{Resource: "booking", Action: "read", Effect: auth.EffectDeny})
		if _, err := relationReadEffectPrincipal(actionContext, principal, "booking"); err == nil {
			t.Fatal("explicit deny was bypassed")
		}
	})

	t.Run("target record deny remains in derived bundle", func(t *testing.T) {
		principal := makePrincipal()
		principal.AccessBundle.DataPolicies = append(principal.AccessBundle.DataPolicies, auth.DataPolicy{Key: "deny-workflow", Resource: "booking", Action: "read", Effect: auth.EffectDeny, Predicate: workflow})
		authorized, err := relationReadEffectPrincipal(actionContext, principal, "booking")
		if err != nil {
			t.Fatal(err)
		}
		compiled, err := evaluator.CompileRecordFilter(*authorized.AccessBundle, "booking", "read", time.Now())
		if err != nil || !reflect.DeepEqual(compiled.Deny, []auth.Predicate{workflow}) {
			t.Fatal("record deny was lost", err)
		}
	})

	t.Run("invalid source authority fails closed", func(t *testing.T) {
		principal := makePrincipal()
		principal.AccessBundle.FunctionGrants = principal.AccessBundle.FunctionGrants[:2]
		if _, err := relationReadEffectPrincipal(actionContext, principal, "booking"); err == nil {
			t.Fatal("missing source authority was accepted")
		}
	})
}
