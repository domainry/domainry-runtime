package record

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func recordTestHasWritableSDKField(principal principalmodel.Principal, objectKey, fieldKey string) bool {
	if principal.AccessBundle == nil {
		return false
	}
	for _, policy := range principal.AccessBundle.FieldPolicies {
		if string(policy.Resource) == objectKey && policy.Field == fieldKey && policy.Write {
			return true
		}
	}
	return false
}

func TestRecordEffectAuthorizationPrincipalRequiresActionOwnedObjectAuthority(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "approver"}}, accessfixture.Bundle{
		Key: "approver", Permissions: []string{"order.approve"},
		FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "order", FieldKey: "status", Write: false}},
	})
	assertUnchanged := func(name string, ctx context.Context) {
		t.Helper()
		got := recordEffectAuthorizationPrincipal(ctx, principal, "order", "update")
		if permissions := got.PermissionKeys(); len(permissions) != 1 || permissions[0] != "order.approve" {
			t.Fatalf("%s unexpectedly elevated principal: %#v", name, got)
		}
	}
	assertUnchanged("ordinary HTTP", t.Context())
	assertUnchanged("missing object authority", recordmutation.WithMutationInvocation(t.Context(), recordmutation.MutationInvocation{Source: transactionmodel.MutationSourceAction, EffectAuthority: map[string][]string{"ledger": {"amount"}}}))
	assertUnchanged("empty object authority", recordmutation.WithMutationInvocation(t.Context(), recordmutation.MutationInvocation{Source: transactionmodel.MutationSourceAction, EffectAuthority: map[string][]string{"order": {" "}}}))
	assertUnchanged("non Action source", recordmutation.WithMutationInvocation(t.Context(), recordmutation.MutationInvocation{Source: transactionmodel.MutationSourceWorkflow, EffectAuthority: map[string][]string{"order": {"status"}}}))
	mixedCtx := recordmutation.WithMutationInvocation(t.Context(), recordmutation.MutationInvocation{Source: transactionmodel.MutationSourceAction, ActionKey: "order.approve", EffectAuthority: map[string][]string{"order": {"status", " "}}})
	mixed := recordEffectAuthorizationPrincipal(mixedCtx, principal, "order", "update")
	if !recordTestHasWritableSDKField(mixed, "order", "status") || recordTestHasWritableSDKField(mixed, "order", " ") {
		t.Fatalf("blank authority field was not skipped: %#v", mixed.AccessBundle.FieldPolicies)
	}

	ctx := recordmutation.WithMutationInvocation(t.Context(), recordmutation.MutationInvocation{Source: transactionmodel.MutationSourceAction, ActionKey: "order.approve", EffectAuthority: map[string][]string{"order": {"status"}}})
	got := recordEffectAuthorizationPrincipal(ctx, principal, " order ", "update")
	if got.RoleKey != principal.RoleKey || !got.HasPermission("order.update") ||
		!recordTestHasWritableSDKField(got, "order", "status") || principal.HasPermission("order.update") {
		t.Fatalf("authorized=%#v original=%#v", got, principal)
	}
	for _, permission := range got.PermissionKeys() {
		if permission == "runtime.appschema.validate_application_definition" {
			t.Fatalf("Action effect authority expanded record scope: %#v", got.AccessBundle)
		}
	}
	guarded := got
	bundle := *guarded.AccessBundle
	bundle.Guardrails = append(bundle.Guardrails, identitysdk.Guardrail{Key: "deny-status", Resource: "order", Action: "update", Field: "status", Effect: identitysdk.EffectDeny})
	guarded.AccessBundle = &bundle
	object := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}
	if recordpolicy.RecordCanWriteObjectFieldForPrincipal(guarded, object, object.Fields[0]) {
		t.Fatal("Action effect authority bypassed a deny-only field guardrail")
	}
}

func TestRecordEffectAuthorizationPrincipalScopesCreateAndRejectsOtherVerbs(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "member"}}, accessfixture.Bundle{Key: "member", Permissions: []string{"application.register"}})
	ctx := recordmutation.WithMutationInvocation(t.Context(), recordmutation.MutationInvocation{
		Source: transactionmodel.MutationSourceAction, ActionKey: "application.register", EffectAuthority: map[string][]string{"application": {"number", "status", "policy_value"}},
	})
	created := recordEffectAuthorizationPrincipal(ctx, principal, "application", "create")
	if !created.HasPermission("application.create") || principal.HasPermission("application.create") {
		t.Fatalf("created=%#v original=%#v", created, principal)
	}
	for _, action := range []string{"delete", "restore", "import", ""} {
		got := recordEffectAuthorizationPrincipal(ctx, principal, "application", action)
		if got.HasPermission("application.create") || !got.HasPermission("application.register") {
			t.Fatalf("action %q unexpectedly elevated principal: %#v", action, got)
		}
	}
}

func TestActionEffectAuthorityAllowsInternalUpdateWithoutObjectUpdatePermission(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}, {Key: "secret", Type: "text"}}}
	repository := &updateEdgeRepository{found: true, record: recordmodel.Record{ID: "order-1", UpdatedAt: "revision-1", Data: map[string]any{"status": "pending", "secret": "sealed"}}}
	dependencies := recordUpdateEdgeDependencies(repository)
	dependencies.ObjectForAction = func(principal principalmodel.Principal, objectKey, action string) (definitionmodel.ObjectSchema, error) {
		if objectKey != object.Key || action != "update" {
			t.Fatalf("object authorization target=%s action=%s", objectKey, action)
		}
		if principal.HasPermission("order.update") {
			return object, nil
		}
		return definitionmodel.ObjectSchema{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.permission.denied"}
	}
	dependencies.CanAccess = func(principal principalmodel.Principal, _ definitionmodel.ObjectSchema, _ recordmodel.Record) bool {
		return principal.HasPermission("order.update")
	}
	dependencies.CanWrite = func(principal principalmodel.Principal, _ definitionmodel.ObjectSchema, _ map[string]any) bool {
		return principal.HasPermission("order.update")
	}
	service := NewRecordUpdateApplicationService(dependencies)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "approver"}}, accessfixture.Bundle{
		Key: "approver", Permissions: []string{"order.approve"},
		FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "order", FieldKey: "status", Write: false}},
	})
	ctx := recordmutation.WithMutationInvocation(t.Context(), recordmutation.MutationInvocation{
		Source: transactionmodel.MutationSourceAction, ActionKey: "order.approve", EffectAuthority: map[string][]string{"order": {"status"}},
	})
	updated, err := service.Update(ctx, object.Key, "order-1", map[string]any{"status": "approved"}, principal)
	if err != nil || repository.commit.Record.Data["status"] != "approved" || principal.HasPermission("order.update") || repository.commit.Audit != nil {
		t.Fatalf("updated=%#v principal=%#v commit=%#v err=%v", updated, principal, repository.commit, err)
	}

	repository.record = recordmodel.Record{ID: "order-1", UpdatedAt: "revision-2", Data: map[string]any{"status": "approved", "secret": "sealed"}}
	_, err = service.Update(ctx, object.Key, "order-1", map[string]any{"secret": "exposed"}, principal)
	if apperror.CodeOf(err) != "backend.mutation.effect_authority_denied" {
		t.Fatalf("out-of-effect update error=%v", err)
	}
}

func TestActionEffectAuthorityAllowsGovernedConditionalUpdateWithoutObjectUpdatePermission(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}
	repository := &updateEdgeRepository{found: true, record: recordmodel.Record{
		ID: "order-1", UpdatedAt: "revision-1", Data: map[string]any{"status": "pending"},
	}}
	dependencies := recordUpdateEdgeDependencies(repository)
	dependencies.ObjectForAction = func(principal principalmodel.Principal, objectKey, action string) (definitionmodel.ObjectSchema, error) {
		if objectKey != object.Key || action != "update" {
			t.Fatalf("object authorization target=%s action=%s", objectKey, action)
		}
		if principal.HasPermission("order.update") {
			return object, nil
		}
		return definitionmodel.ObjectSchema{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.permission.denied"}
	}
	dependencies.CanAccessScope = func(_ context.Context, principal principalmodel.Principal, _ definitionmodel.ObjectSchema, _ recordmodel.Record, _ bool) (bool, error) {
		return principal.HasPermission("order.update"), nil
	}
	service := NewRecordUpdateApplicationService(dependencies)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "approver"}}, accessfixture.Bundle{Key: "approver", Permissions: []string{"order.approve"}})
	ctx := recordmutation.WithMutationInvocation(t.Context(), recordmutation.MutationInvocation{
		Source: transactionmodel.MutationSourceAction, ActionKey: "order.approve",
		EffectAuthority: map[string][]string{"order": {"status"}},
	})
	plan, updated, err := service.PlanConditionalUpdateMutation(ctx, object.Key, "order-1", transactionmodel.ConditionalUpdateInput{
		Patch: map[string]any{"status": "approved"},
	}, principal)
	if err != nil || updated.Data["status"] != "approved" || plan.CanonicalCommit().Record.Data["status"] != "approved" {
		t.Fatalf("plan=%#v updated=%#v err=%v", plan, updated, err)
	}
	if principal.HasPermission("order.update") {
		t.Fatalf("original principal was mutated: %#v", principal)
	}
}

func TestActionEffectAuthorityAllowsGovernedInternalCreateWithoutObjectCreatePermission(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "mobile", Type: "text"}, {Key: "status", Type: "text"}, {Key: "policy_value", Type: "number"}}}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "member"}}, accessfixture.Bundle{
		Key: "member", Permissions: []string{"customer.register"},
		FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "status", Write: false}},
	})
	dependencies := recordCreateEdgeDependencies(&createRepositoryProbe{})
	dependencies.ObjectForAction = func(got principalmodel.Principal, objectKey, action string) (definitionmodel.ObjectSchema, error) {
		if objectKey != object.Key || action != "create" {
			t.Fatalf("object authorization target=%s action=%s", objectKey, action)
		}
		if got.HasPermission("customer.create") {
			return object, nil
		}
		return definitionmodel.ObjectSchema{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.permission.denied"}
	}
	dependencies.CanWriteCandidate = func(_ context.Context, got principalmodel.Principal, _ definitionmodel.ObjectSchema, candidate recordmodel.Record) (bool, error) {
		hasCreate := got.HasPermission("customer.create")
		hasStatusWrite := recordTestHasWritableSDKField(got, "customer", "status")
		if !hasCreate || !hasStatusWrite || candidate.Data["status"] != "pending" {
			t.Fatalf("Action authorization or server-owned candidate was not preserved: principal=%#v candidate=%#v", got, candidate)
		}
		return true, nil
	}
	service := NewRecordCreateApplicationService(dependencies)
	ctx := recordmutation.WithMutationInvocation(t.Context(), recordmutation.MutationInvocation{
		Source: transactionmodel.MutationSourceAction, ActionKey: "customer.register", EffectAuthority: map[string][]string{"customer": {"mobile", "status", "policy_value"}},
	})
	plan, candidate, err := service.PlanCreateMutation(ctx, object.Key, map[string]any{"mobile": "10000000001", "status": "pending", "policy_value": 20}, "customer-1", principal)
	if err != nil || candidate.ID != "customer-1" || plan.CanonicalCommit().Record.ID != candidate.ID || principal.HasPermission("customer.create") {
		t.Fatalf("plan=%#v candidate=%#v principal=%#v err=%v", plan, candidate, principal, err)
	}

	_, _, err = service.PlanCreateMutation(t.Context(), object.Key, map[string]any{"mobile": "10000000001", "status": "pending", "policy_value": 20}, "customer-2", principal)
	if apperror.CodeOf(err) != "backend.permission.denied" {
		t.Fatalf("ordinary create unexpectedly received Action effect authority: %v", err)
	}
}
