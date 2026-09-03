package recordmutation

import (
	"context"
	"errors"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func TestMutationPlannerBuildsCanonicalContextFromPrincipalAndInvocation(t *testing.T) {
	planner := NewMutationPlannerApplicationService(func(context.Context, principalmodel.Principal) (string, error) { return "published-17", nil })
	ctx := requestcontext.WithCorrelationID(t.Context(), "correlation-1")
	ctx = WithMutationInvocation(ctx, MutationInvocation{Source: transactionmodel.MutationSourceAction, ActionKey: "order.pay", IdempotencyKey: "idem-1", EffectAuthority: map[string][]string{"order": {"status"}}, AssuranceEvidence: map[string]string{"mfa": "verified"}})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a", UserID: "user-a"}, RequestID: "request-1"}, accessfixture.Bundle{
		Key: "cashier", Permissions: []string{"order.update"}, DataPolicies: accessfixture.DataPoliciesForPermissions([]string{"order.update"}, "owner"),
	})
	plan, err := planner.Plan(ctx, principal, transactionmodel.RecordMutationCommit{Operation: "update", Object: definitionmodel.ObjectSchema{Key: "order"}, Record: recordmodel.Record{ID: "order-1", Data: map[string]any{"status": "paid"}}}, map[string]any{"status": "draft"})
	if err != nil {
		t.Fatal(err)
	}
	got := plan.Context()
	if got.Source() != transactionmodel.MutationSourceAction || got.ActionKey() != "order.pay" || got.ApplicationSchemaRevision() != "published-17" || got.CorrelationID() != "correlation-1" || got.IdempotencyKey() != "idem-1" || got.RoleKey() != "cashier" || !got.AllowsEffect("order", "status") || got.AssuranceEvidence()["mfa"] != "verified" {
		t.Fatalf("context=%+v", got)
	}
}

func TestMutationPlannerUsesDeterministicObjectRevisionWithoutRepositoryResolver(t *testing.T) {
	planner := NewMutationPlannerApplicationService(nil)
	commit := transactionmodel.RecordMutationCommit{Operation: "create", Object: definitionmodel.ObjectSchema{Key: "order"}, Record: recordmodel.Record{ID: "order-1"}}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}}
	first, err := planner.Plan(t.Context(), principal, commit, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := planner.Plan(t.Context(), principal, commit, nil)
	if err != nil || first.Context().ApplicationSchemaRevision() != second.Context().ApplicationSchemaRevision() || !strings.HasPrefix(first.Context().ApplicationSchemaRevision(), "object:") || first.Context().Source() != transactionmodel.MutationSourceHTTP || first.Context().CorrelationID() == "" {
		t.Fatalf("first=%+v second=%+v err=%v", first.Context(), second.Context(), err)
	}
}

func TestMutationPlannerRejectsMissingPublishedRevision(t *testing.T) {
	planner := NewMutationPlannerApplicationService(func(context.Context, principalmodel.Principal) (string, error) { return "", nil })
	_, err := planner.Plan(t.Context(), principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}}, transactionmodel.RecordMutationCommit{Operation: "create", Object: definitionmodel.ObjectSchema{Key: "order"}, Record: recordmodel.Record{ID: "order-1"}}, nil)
	var typed *MutationPlannerError
	if !errors.As(err, &typed) || typed.Code != "backend.mutation.metadata_revision_unavailable" {
		t.Fatalf("error=%#v", err)
	}
}

func TestMutationPlannerEnforcesPublishedObjectWritePolicy(t *testing.T) {
	planner := NewMutationPlannerApplicationService(func(context.Context, principalmodel.Principal) (string, error) { return "revision-1", nil })
	commit := transactionmodel.RecordMutationCommit{Operation: "update", Object: definitionmodel.ObjectSchema{Key: "payment", Config: map[string]any{"write_policy": "action_only"}}, Record: recordmodel.Record{ID: "payment-1", Data: map[string]any{"status": "settled"}}}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}}
	_, err := planner.Plan(t.Context(), principal, commit, map[string]any{"status": "pending"})
	var typed *MutationPlannerError
	if !errors.As(err, &typed) || typed.Code != "backend.mutation.action_required" || typed.Field != "payment" {
		t.Fatalf("direct error=%#v", err)
	}
	actionCtx := WithMutationInvocation(t.Context(), MutationInvocation{Source: transactionmodel.MutationSourceAction, ActionKey: "payment.settle", EffectAuthority: map[string][]string{"payment": {"status"}}})
	if _, err := planner.Plan(actionCtx, principal, commit, map[string]any{"status": "pending"}); err != nil {
		t.Fatalf("published Action must be allowed: %v", err)
	}
}

func TestMutationPlannerEnforcesLifecyclePolicyForEveryInvocationSource(t *testing.T) {
	planner := NewMutationPlannerApplicationService(func(context.Context, principalmodel.Principal) (string, error) { return "revision-1", nil })
	object := definitionmodel.ObjectSchema{Key: "ledger_entry", LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleAppendOnly}}
	commit := transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: recordmodel.Record{ID: "entry-1"}}
	for _, source := range []transactionmodel.MutationSource{transactionmodel.MutationSourceHTTP, transactionmodel.MutationSourceAction, transactionmodel.MutationSourceAutomation, transactionmodel.MutationSourceWorkflow, transactionmodel.MutationSourceInternal} {
		ctx := WithMutationInvocation(t.Context(), MutationInvocation{Source: source})
		_, err := planner.Plan(ctx, principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}}, commit, map[string]any{"amount": 10})
		if code := apperror.CodeOf(err); code != "backend.record.lifecycle_append_only" {
			t.Fatalf("source=%s code=%q err=%v", source, code, err)
		}
	}
}

func TestMutationPlannerContextHelpersAndRemainingPlanBranches(t *testing.T) {
	if WithMutationInvocation(nil, MutationInvocation{}) != nil {
		t.Fatal("nil invocation context must remain nil")
	}
	if invocation, ok := MutationInvocationFromContext(nil); ok || invocation.Source != "" {
		t.Fatalf("nil invocation=%#v ok=%v", invocation, ok)
	}
	predicates := []transactionmodel.MutationPredicate{{Field: "status", Operator: "eq", Value: "draft"}}
	if WithMutationPredicates(nil, predicates) != nil || MutationPredicatesFromContext(nil) != nil {
		t.Fatal("nil predicate context must remain nil")
	}
	predicateContext := WithMutationPredicates(t.Context(), predicates)
	predicates[0].Field = "changed"
	fromContext := MutationPredicatesFromContext(predicateContext)
	if len(fromContext) != 1 || fromContext[0].Field != "status" {
		t.Fatalf("stored predicates=%#v", fromContext)
	}
	fromContext[0].Field = "mutated"
	if again := MutationPredicatesFromContext(predicateContext); again[0].Field != "status" {
		t.Fatalf("predicate context leaked result mutation: %#v", again)
	}

	commit := transactionmodel.RecordMutationCommit{
		Operation: "update",
		Object:    definitionmodel.ObjectSchema{Key: "order"},
		Record:    recordmodel.Record{ID: "order-1", Data: map[string]any{"status": "active"}},
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a", UserID: "user-a"}}
	planner := NewMutationPlannerApplicationService(func(context.Context, principalmodel.Principal) (string, error) {
		return "revision-1", nil
	})
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := planner.Plan(cancelled, principal, commit, map[string]any{"status": "draft"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled plan error=%v", err)
	}
	emptyInvocation := WithMutationInvocation(t.Context(), MutationInvocation{})
	plan, err := planner.Plan(emptyInvocation, principal, commit, map[string]any{"status": "draft"})
	if err != nil || plan.Context().Source() != transactionmodel.MutationSourceHTTP {
		t.Fatalf("empty invocation source=%q err=%v", plan.Context().Source(), err)
	}
	actionOnly := commit
	actionOnly.Object.Config = map[string]any{"write_policy": "action_only"}
	internalContext := WithMutationInvocation(t.Context(), MutationInvocation{Source: transactionmodel.MutationSourceInternal})
	if _, err := planner.Plan(internalContext, principal, actionOnly, map[string]any{"status": "draft"}); err != nil {
		t.Fatalf("internal action-only mutation error=%v", err)
	}
	if _, err := planner.Plan(t.Context(), principal, transactionmodel.RecordMutationCommit{
		Operation: "merge", Object: commit.Object, Record: commit.Record,
	}, nil); err == nil {
		t.Fatal("unsupported operation must fail the canonical plan")
	}
	if _, err := planner.Plan(t.Context(), principalmodel.Principal{}, commit, map[string]any{"status": "draft"}); err == nil {
		t.Fatal("missing workspace must fail mutation context construction")
	}
}

func TestMutationPlannerRemainingRevisionAndErrorBranches(t *testing.T) {
	commit := transactionmodel.RecordMutationCommit{
		Operation: "create",
		Object:    definitionmodel.ObjectSchema{Key: "order"},
		Record:    recordmodel.Record{ID: "order-1"},
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}}
	revisionErr := errors.New("revision unavailable")
	planner := NewMutationPlannerApplicationService(func(context.Context, principalmodel.Principal) (string, error) {
		return "", revisionErr
	})
	if _, err := planner.Plan(t.Context(), principal, commit, nil); !errors.Is(err, revisionErr) {
		t.Fatalf("revision resolver error=%v", err)
	}

	var nilPlanner *MutationPlannerApplicationService
	plan, err := nilPlanner.Plan(t.Context(), principal, commit, nil)
	if err != nil || !strings.HasPrefix(plan.Context().ApplicationSchemaRevision(), "object:") {
		t.Fatalf("nil planner fallback revision=%q err=%v", plan.Context().ApplicationSchemaRevision(), err)
	}
	unencodable := commit
	unencodable.Object.Config = map[string]any{"unsupported": func() {}}
	if _, err := nilPlanner.Plan(t.Context(), principal, unencodable, nil); err == nil {
		t.Fatal("unencodable object revision must fail")
	} else {
		var typed *MutationPlannerError
		if !errors.As(err, &typed) || typed.Err == nil || typed.Code != "backend.mutation.metadata_revision_unavailable" {
			t.Fatalf("unencodable revision error=%#v", err)
		}
	}

	wrapped := &MutationPlannerError{Code: "backend.test", Err: revisionErr}
	if !strings.Contains(wrapped.Error(), revisionErr.Error()) || !errors.Is(wrapped, revisionErr) {
		t.Fatalf("wrapped planner error=%v", wrapped)
	}
	field := &MutationPlannerError{Code: "backend.test", Field: "status"}
	if field.Error() != "backend.test: status" {
		t.Fatalf("field planner error=%q", field.Error())
	}
	plain := &MutationPlannerError{Code: "backend.test"}
	if plain.Error() != "backend.test" || plain.Unwrap() != nil {
		t.Fatalf("plain planner error=%q unwrap=%v", plain.Error(), plain.Unwrap())
	}
	if coordinate := firstMutationCoordinate(" ", " request-1 ", "request-2"); coordinate != "request-1" {
		t.Fatalf("first coordinate=%q", coordinate)
	}
	if coordinate := firstMutationCoordinate(" ", ""); coordinate != "" {
		t.Fatalf("empty coordinate=%q", coordinate)
	}
}
