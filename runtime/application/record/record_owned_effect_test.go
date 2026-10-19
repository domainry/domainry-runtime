package record

import (
	"context"
	"reflect"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestOwnedEffectUsesAuthorizedParentPlanWithoutCopyingSourceACL(t *testing.T) {
	parent := definitionmodel.ObjectSchema{Key: "deal", Fields: []definitionmodel.FieldSchema{{Key: "owner", Type: "user"}}}
	child := definitionmodel.ObjectSchema{Key: "stage_event", AuthorizationParent: &definitionmodel.ObjectAuthorizationParent{ObjectKey: "deal", RelationField: "deal"}, Fields: []definitionmodel.FieldSchema{
		{Key: "deal", Type: "relation", Required: true, Validation: definitionmodel.FieldValidation{Target: "deal"}}, {Key: "stage", Type: "text"},
	}}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "seller"}}, accessfixture.Bundle{Permissions: []string{"deal.advance"}})
	principal.AccessBundle.DataPolicies = []identitysdk.DataPolicy{{Key: "deal.advance.own", Resource: "deal", Action: "advance", Effect: identitysdk.EffectAllow, Predicate: identitysdk.Predicate{Fact: "owner", Operator: identitysdk.OperatorEqual, Value: "$subject.id"}}}
	original := *principal.AccessBundle
	invocation := recordmutation.MutationInvocation{Source: transactionmodel.MutationSourceAction, ActionKey: "deal.advance", ActionResource: "deal", ActionOperation: "advance", EffectAuthority: map[string][]string{"stage_event": {"deal", "stage"}}}
	owner := recordmodel.Record{WorkspaceID: "workspace-a", ID: "deal-a", Data: map[string]any{"owner": "seller"}}
	contextFor := func(invocation recordmutation.MutationInvocation, owner recordmodel.Record) context.Context {
		ctx := recordmutation.WithMutationInvocation(t.Context(), invocation)
		return recordservice.RecordWithPlannedRelations(ctx, map[string]map[string]recordmodel.Record{"deal": {"deal-a": owner}})
	}
	policy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{parent, child} }})
	dependencies := recordCreateEdgeDependencies(&createRepositoryProbe{})
	dependencies.Objects = func() map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"deal": parent, "stage_event": child}
	}
	dependencies.ObjectForAction = policy.ObjectForAction
	dependencies.CanWriteCandidate = func(ctx context.Context, authorized principalmodel.Principal, object definitionmodel.ObjectSchema, candidate recordmodel.Record) (bool, error) {
		return policy.CanAccessRecordAction(ctx, authorized, object, candidate, "create")
	}
	service := NewRecordCreateApplicationService(dependencies)
	ctx := contextFor(invocation, owner)
	plan, record, err := service.PlanCreateMutation(ctx, child.Key, map[string]any{"deal": "deal-a", "stage": "proposal"}, "event-a", principal)
	if err != nil || record.ID != "event-a" || plan.CanonicalCommit().Record.WorkspaceID != "workspace-a" {
		t.Fatalf("owned event failed: record=%+v err=%v", record, err)
	}
	authorized, err := recordOwnedEffectAuthorizationPrincipal(ctx, principal, child)
	if err != nil {
		t.Fatal(err)
	}
	for _, dataPolicy := range authorized.AccessBundle.DataPolicies {
		if dataPolicy.Resource == "stage_event" && dataPolicy.Action == "create" && (dataPolicy.Predicate.Fact != "deal" || len(dataPolicy.Predicate.Path) != 0) {
			t.Fatalf("source ACL or schema-only grant escaped into child authority: %+v", dataPolicy)
		}
	}
	if !reflect.DeepEqual(*principal.AccessBundle, original) || principal.HasPermission("stage_event.create") {
		t.Fatal("derived authority mutated caller")
	}
	for _, test := range []struct {
		name       string
		owner      recordmodel.Record
		id         string
		invocation recordmutation.MutationInvocation
	}{
		{"wrong parent", owner, "deal-b", invocation},
		{"other workspace", recordmodel.Record{WorkspaceID: "workspace-b", ID: "deal-a"}, "deal-a", invocation},
		{"missing workspace", recordmodel.Record{ID: "deal-a"}, "deal-a", invocation},
		{"deleted parent", recordmodel.Record{WorkspaceID: "workspace-a", ID: "deal-a", Deleted: true}, "deal-a", invocation},
		{"no parent plan", recordmodel.Record{}, "deal-a", invocation},
		{"not Action", owner, "deal-a", recordmutation.MutationInvocation{Source: transactionmodel.MutationSourceWorkflow}},
		{"undeclared effect", owner, "deal-a", recordmutation.MutationInvocation{Source: transactionmodel.MutationSourceAction, ActionResource: "deal", ActionOperation: "advance"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := service.PlanCreateMutation(contextFor(test.invocation, test.owner), child.Key, map[string]any{"deal": test.id, "stage": "proposal"}, "event-b", principal)
			if apperror.KindOf(err) != apperror.KindForbidden {
				t.Fatalf("invalid owned effect accepted: %v", err)
			}
		})
	}
	t.Run("hook cannot redirect parent", func(t *testing.T) {
		dependencies.RunBefore = func(_ context.Context, _, _, _ string, _, _, data map[string]any, _ principalmodel.Principal) error {
			data["deal"] = "deal-b"
			return nil
		}
		_, _, err := NewRecordCreateApplicationService(dependencies).PlanCreateMutation(ctx, child.Key, map[string]any{"deal": "deal-a", "stage": "proposal"}, "event-c", principal)
		if apperror.CodeOf(err) != "backend.mutation.owned_parent_denied" {
			t.Fatalf("redirected effect accepted: %v", err)
		}
	})
	t.Run("source deny remains authoritative", func(t *testing.T) {
		denied := principal
		bundle := *principal.AccessBundle
		bundle.FunctionGrants = append(append([]identitysdk.FunctionGrant(nil), bundle.FunctionGrants...), identitysdk.FunctionGrant{Resource: "deal", Action: "advance", Effect: identitysdk.EffectDeny})
		denied.AccessBundle = &bundle
		_, _, err := service.PlanCreateMutation(ctx, child.Key, map[string]any{"deal": "deal-a", "stage": "proposal"}, "event-d", denied)
		if apperror.KindOf(err) != apperror.KindForbidden {
			t.Fatalf("source deny bypassed: %v", err)
		}
	})
}
