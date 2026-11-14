package record

import (
	"context"
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

func TestOwnedUpdateUsesExactAuthorizedParentDespiteDifferentChildOwner(t *testing.T) {
	for _, entry := range []string{"conditional", "locked", "planned", "update"} {
		t.Run(entry, func(t *testing.T) {
			parent := definitionmodel.ObjectSchema{Key: "case"}
			child := definitionmodel.ObjectSchema{Key: "work", AuthorizationParent: &definitionmodel.ObjectAuthorizationParent{ObjectKey: parent.Key, RelationField: "case"}, Fields: []definitionmodel.FieldSchema{
				{Key: "case", Type: "relation", Required: true, Validation: definitionmodel.FieldValidation{Target: parent.Key}}, {Key: "status", Type: "text"}, {Key: "revision", Type: "integer"},
			}}
			original := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "ingress"}}, accessfixture.Bundle{Permissions: []string{"case_link.resume"}})
			original.AccessBundle.DataPolicies = []identitysdk.DataPolicy{{Key: "resume.own", Resource: "case_link", Action: "resume", Effect: identitysdk.EffectAllow, Predicate: identitysdk.Predicate{Fact: "owner_user_id", Operator: identitysdk.OperatorEqual, Value: "$subject.id"}}}
			owner := recordmodel.Record{WorkspaceID: "workspace-a", RecordID: "case-a", OwnerUserID: "ingress"}
			before := recordmodel.Record{WorkspaceID: "workspace-a", RecordID: "work-a", OwnerUserID: "agent", UpdatedAt: "2026-11-14T00:00:00Z", Data: map[string]any{"case": owner.RecordID, "status": "waiting", "revision": int64(1)}}
			policy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{parent, child} }})
			for _, scenario := range []string{"allowed", "direct child scope", "foreign parent", "no parent plan", "foreign workspace", "deleted parent", "undeclared effect", "outside Action", "source denied", "child denied", "child row denied", "hook redirects parent", "self effect redirects parent"} {
				t.Run(scenario, func(t *testing.T) {
					principal, currentOwner, current := original, owner, before
					current.Data = map[string]any{"case": owner.RecordID, "status": "waiting", "revision": int64(1)}
					invocation := recordmutation.MutationInvocation{Source: transactionmodel.MutationSourceAction, ActionKey: "case_link.resume", ActionResource: "case_link", ActionOperation: "resume", EffectAuthority: map[string][]string{child.Key: {"status", "revision", "case"}}}
					switch scenario {
					case "direct child scope":
						current.OwnerUserID = principal.UserID
					case "foreign parent":
						current.Data["case"] = "case-b"
					case "foreign workspace":
						currentOwner.WorkspaceID = "workspace-b"
					case "deleted parent":
						currentOwner.Deleted = true
					case "undeclared effect":
						invocation.EffectAuthority = nil
					case "outside Action":
						invocation.Source = transactionmodel.MutationSourceWorkflow
					case "source denied", "child denied":
						bundle := *original.AccessBundle
						resource, action := identitysdk.ResourceType("case_link"), identitysdk.Action("resume")
						if scenario == "child denied" {
							resource, action = identitysdk.ResourceType(child.Key), "update"
						}
						bundle.FunctionGrants = append(append([]identitysdk.FunctionGrant(nil), bundle.FunctionGrants...), identitysdk.FunctionGrant{Resource: resource, Action: action, Effect: identitysdk.EffectDeny})
						principal.AccessBundle = &bundle
					case "child row denied":
						bundle := *original.AccessBundle
						bundle.DataPolicies = append(append([]identitysdk.DataPolicy(nil), bundle.DataPolicies...), identitysdk.DataPolicy{Key: "work.deny", Resource: "work", Action: "update", Effect: identitysdk.EffectDeny, Predicate: identitysdk.Predicate{Fact: "case", Operator: identitysdk.OperatorEqual, Value: "case-a"}})
						principal.AccessBundle = &bundle
					}
					ctx := recordmutation.WithMutationInvocation(t.Context(), invocation)
					if scenario != "no parent plan" && scenario != "direct child scope" {
						ctx = recordservice.RecordWithPlannedRelations(ctx, map[string]map[string]recordmodel.Record{parent.Key: {owner.RecordID: currentOwner}})
					}
					repository := &updateEdgeRepository{record: current, found: true}
					deps := recordUpdateEdgeDependencies(repository)
					deps.ObjectForAction, deps.ScopeForAction = policy.ObjectForAction, policy.MutationScopeExpression
					deps.CanAccessScope = func(ctx context.Context, p principalmodel.Principal, o definitionmodel.ObjectSchema, r recordmodel.Record, _ bool) (bool, error) {
						return policy.CanAccessRecordAction(ctx, p, o, r, "update")
					}
					if scenario == "hook redirects parent" {
						deps.RunBefore = func(_ context.Context, _, _, _ string, _, _, next map[string]any, _ principalmodel.Principal) error {
							next["case"] = "case-b"
							return nil
						}
					}
					if scenario == "self effect redirects parent" {
						deps.ApplySelfEffects = func(_ context.Context, _ definitionmodel.ObjectSchema, _, next map[string]any, _ string, _ principalmodel.Principal) (bool, error) {
							next["case"] = "case-b"
							return true, nil
						}
					}
					service := NewRecordUpdateApplicationService(deps)
					input := transactionmodel.ConditionalUpdateInput{Patch: map[string]any{"status": "succeeded"}, Arithmetic: []transactionmodel.MutationArithmetic{{Field: "revision", Operation: "increment", Operand: int64(1)}}}
					var plan transactionmodel.MutationPlan
					var row recordmodel.Record
					var err error
					switch entry {
					case "conditional":
						plan, row, err = service.PlanConditionalUpdateMutation(ctx, child.Key, current.RecordID, input, principal)
					case "locked":
						plan, row, err = service.PlanConditionalUpdateLockedRecord(ctx, child.Key, current, input, principal)
					case "planned":
						plan, row, err = service.PlanUpdateMutation(ctx, child.Key, current.RecordID, input.Patch, principal)
					case "update":
						row, err = service.Update(ctx, child.Key, current.RecordID, input.Patch, principal)
					}
					if scenario != "allowed" && scenario != "direct child scope" {
						if apperror.KindOf(err) != apperror.KindForbidden {
							t.Fatalf("unsafe update accepted: row=%+v err=%v", row, err)
						}
						return
					}
					if entry == "update" && err == nil {
						if len(row.Data) != 0 {
							t.Fatal("update leaked fields without read permission")
						}
						row = repository.commit.Record
					}
					if err != nil || row.Data["status"] != "succeeded" || row.UpdateBy != principal.UserID || row.OwnerUserID != current.OwnerUserID || row.OwnerOrgID != "" {
						t.Fatalf("owned update failed or changed ownership: %+v %v", row, err)
					}
					commit := plan.CanonicalCommit()
					if entry == "update" {
						commit = repository.commit
					}
					expectedField, expectedID := "case", owner.RecordID
					if scenario == "direct child scope" {
						expectedField, expectedID = "owner_user_id", principal.UserID
					}
					if commit.AuthorizationScope == nil || commit.AuthorizationScope.FieldKey != expectedField || len(commit.AuthorizationScope.Values) != 1 || commit.AuthorizationScope.Values[0] != expectedID {
						t.Fatalf("parent scope missing from SQL commit: %+v", commit.AuthorizationScope)
					}
					if original.HasPermission("work.update") {
						t.Fatal("child authority escaped Action")
					}
				})
			}
		})
	}
}
