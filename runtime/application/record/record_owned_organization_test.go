package record

import (
	"context"
	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
	"testing"
)

func ownedOrganizationFixture(t *testing.T) (RecordCreateDependencies, principalmodel.Principal, recordmutation.MutationInvocation, map[string]map[string]recordmodel.Record) {
	t.Helper()
	parent := definitionmodel.ObjectSchema{Key: "case", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	child := definitionmodel.ObjectSchema{Key: "event", AuthorizationParent: &definitionmodel.ObjectAuthorizationParent{ObjectKey: "case", RelationField: "case"}, Fields: []definitionmodel.FieldSchema{{Key: "case", Type: "relation", Required: true, Validation: definitionmodel.FieldValidation{Target: "case"}}, {Key: "body", Type: "text"}}}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "reviewer", OrgID: "reviewer-home"}}, accessfixture.Bundle{Permissions: []string{"case.review"}})
	invocation := recordmutation.MutationInvocation{Source: transactionmodel.MutationSourceAction, ActionKey: "case.review", ActionResource: "case", ActionOperation: "review", EffectAuthority: map[string][]string{"event": {"case", "body"}}}
	owners := map[string]map[string]recordmodel.Record{"case": {"one": {WorkspaceID: "workspace", RecordID: "one", OwnerOrgID: "support-one"}, "two": {WorkspaceID: "workspace", RecordID: "two", OwnerOrgID: "support-two"}}}
	policy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{parent, child} }})
	dependencies := recordCreateEdgeDependencies(&createRepositoryProbe{})
	dependencies.Objects = func() map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"case": parent, "event": child}
	}
	dependencies.ObjectForAction = policy.ObjectForAction
	dependencies.CanWriteCandidate = func(ctx context.Context, p principalmodel.Principal, o definitionmodel.ObjectSchema, r recordmodel.Record) (bool, error) {
		return policy.CanAccessRecordAction(ctx, p, o, r, "create")
	}
	return dependencies, principal, invocation, owners
}

func TestOwnedCreateInheritsExactParentOrganizationAcrossRootsAndKeepsActor(t *testing.T) {
	dependencies, principal, invocation, owners := ownedOrganizationFixture(t)
	ctx := recordservice.RecordWithPlannedRelations(recordmutation.WithMutationInvocation(t.Context(), invocation), owners)
	for _, id := range []string{"one", "two"} {
		plan, created, err := NewRecordCreateApplicationService(dependencies).PlanCreateMutation(ctx, "event", map[string]any{"case": id, "body": "reviewed"}, "event-"+id, principal)
		if err != nil {
			t.Fatal(err)
		}
		commit := plan.CanonicalCommit().Record
		if created.OwnerOrgID != owners["case"][id].OwnerOrgID || commit.OwnerOrgID != created.OwnerOrgID || commit.OwnerUserID != "reviewer" || commit.CreateBy != "reviewer" || commit.WorkspaceID != "workspace" {
			t.Fatal("child ownership or actor was replaced", created, commit)
		}
	}
}
func TestOwnedCreateRejectsConflictingTargetAndLateParentRedirects(t *testing.T) {
	for _, name := range []string{"same target", "conflicting target", "empty parent organization", "other authorized parent", "other parent same organization", "input parent map changed"} {
		t.Run(name, func(t *testing.T) {
			dependencies, principal, invocation, owners := ownedOrganizationFixture(t)
			want := owners["case"]["one"].OwnerOrgID
			switch name {
			case "same target":
				invocation.TargetOrganizationID = want
			case "conflicting target":
				invocation.TargetOrganizationID = "different"
			case "empty parent organization":
				p := owners["case"]["one"]
				p.OwnerOrgID = ""
				owners["case"]["one"] = p
				want = ""
			case "other parent same organization":
				p := owners["case"]["two"]
				p.OwnerOrgID = want
				owners["case"]["two"] = p
			}
			if name == "other authorized parent" || name == "other parent same organization" {
				dependencies.RunBefore = func(_ context.Context, _, _, _ string, _, _, data map[string]any, _ principalmodel.Principal) error {
					data["case"] = "two"
					return nil
				}
			}
			if name == "input parent map changed" {
				dependencies.RunBefore = func(_ context.Context, _, _, _ string, _, _, _ map[string]any, _ principalmodel.Principal) error {
					p := owners["case"]["one"]
					p.OwnerOrgID = "changed"
					owners["case"]["one"] = p
					return nil
				}
			}
			ctx := recordservice.RecordWithPlannedRelations(recordmutation.WithMutationInvocation(t.Context(), invocation), owners)
			plan, created, err := NewRecordCreateApplicationService(dependencies).PlanCreateMutation(ctx, "event", map[string]any{"case": "one"}, "event", principal)
			allowed := name == "same target" || name == "empty parent organization" || name == "input parent map changed"
			if allowed {
				if err != nil || created.OwnerOrgID != want || plan.CanonicalCommit().Record.OwnerOrgID != want {
					t.Fatal(name, created, err)
				}
			} else if apperror.CodeOf(err) != "backend.mutation.owned_parent_denied" {
				t.Fatal("redirected ownership accepted", name, created, err)
			}
		})
	}
}
