package transactionmodel

import (
	"errors"
	"reflect"
	"testing"
)

func TestMutationContextCapturesImmutableIdentitySourceAuthorityAndAssurance(t *testing.T) {
	permissions := []string{" order.update ", "order.read", "order.update"}
	authority := map[string][]string{"order": {"status", " total ", "status"}}
	assurance := map[string]string{"method": " otp ", "nonce": "nonce-1"}
	context, err := NewMutationContext(MutationContextInput{
		WorkspaceID: " workspace-a ", ActorID: "user-a", RoleKey: "operator", Permissions: permissions,
		IdentityVersion: "identity-v7", Source: MutationSourceAction, ActionKey: " order.submit ",
		RequestID: "request-1", IdempotencyKey: "idem-1", CorrelationID: "correlation-1", CausationID: "cause-1",
		ApplicationSchemaRevision: "revision-9", EffectAuthority: authority, AssuranceEvidence: assurance,
	})
	if err != nil {
		t.Fatal(err)
	}
	permissions[0], authority["order"][0], assurance["method"] = "escalated", "secret", "bypassed"
	if context.WorkspaceID() != "workspace-a" || context.ActorID() != "user-a" || context.RoleKey() != "operator" || context.Source() != MutationSourceAction || context.ActionKey() != "order.submit" || context.ApplicationSchemaRevision() != "revision-9" {
		t.Fatalf("context identity/source=%+v", context)
	}
	if !reflect.DeepEqual(context.Permissions(), []string{"order.read", "order.update"}) || !context.AllowsEffect("order", "status") || !context.AllowsEffect("order", "total") || context.AllowsEffect("order", "secret") || context.AssuranceEvidence()["method"] != "otp" {
		t.Fatalf("context snapshots permissions=%v authority=%v assurance=%v", context.Permissions(), context.EffectAuthority(), context.AssuranceEvidence())
	}
	exposed := context.EffectAuthority()
	exposed["order"][0] = "secret"
	if context.AllowsEffect("order", "secret") {
		t.Fatal("effect authority getter exposed mutable state")
	}
}

func TestMutationContextValidatesSourceIdentityAndRequiredSnapshotCoordinates(t *testing.T) {
	base := MutationContextInput{WorkspaceID: "workspace", CorrelationID: "correlation", ApplicationSchemaRevision: "revision", Source: MutationSourceHTTP}
	for _, test := range []struct {
		name  string
		input MutationContextInput
		field string
	}{
		{name: "workspace", input: MutationContextInput{Source: MutationSourceHTTP, CorrelationID: "correlation", ApplicationSchemaRevision: "revision"}, field: "workspace_id"},
		{name: "source", input: MutationContextInput{WorkspaceID: "workspace", CorrelationID: "correlation", ApplicationSchemaRevision: "revision", Source: "unknown"}, field: "source"},
		{name: "correlation", input: MutationContextInput{WorkspaceID: "workspace", ApplicationSchemaRevision: "revision", Source: MutationSourceHTTP}, field: "correlation_id"},
		{name: "revision", input: MutationContextInput{WorkspaceID: "workspace", CorrelationID: "correlation", Source: MutationSourceHTTP}, field: "metadata_revision"},
		{name: "action key", input: MutationContextInput{WorkspaceID: "workspace", CorrelationID: "correlation", ApplicationSchemaRevision: "revision", Source: MutationSourceAction}, field: "action_key"},
		{name: "workflow key", input: MutationContextInput{WorkspaceID: "workspace", CorrelationID: "correlation", ApplicationSchemaRevision: "revision", Source: MutationSourceWorkflow}, field: "workflow_key"},
		{name: "automation key", input: MutationContextInput{WorkspaceID: "workspace", CorrelationID: "correlation", ApplicationSchemaRevision: "revision", Source: MutationSourceAutomation}, field: "automation_key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewMutationContext(test.input)
			var typed *MutationContextError
			if !errors.As(err, &typed) || typed.Code != "backend.mutation.context_invalid" || typed.Field != test.field {
				t.Fatalf("error=%#v", err)
			}
		})
	}
	for _, source := range []MutationSource{MutationSourceHTTP, MutationSourceProjectHTTP, MutationSourceImport, MutationSourceScheduler, MutationSourceIntegration, MutationSourceInternal} {
		input := base
		input.Source = source
		if _, err := NewMutationContext(input); err != nil {
			t.Fatalf("source=%s err=%v", source, err)
		}
	}
}

func TestMutationContextWildcardEffectAuthority(t *testing.T) {
	context, err := NewMutationContext(MutationContextInput{WorkspaceID: "workspace", CorrelationID: "correlation", ApplicationSchemaRevision: "revision", Source: MutationSourceInternal, EffectAuthority: map[string][]string{"projection": {"*"}}})
	if err != nil || !context.AllowsEffect("projection", "any_field") || context.AllowsEffect("other", "any_field") {
		t.Fatalf("context=%+v err=%v", context, err)
	}
}

func TestTransactionModelRemainingValueAndCloneEdges(t *testing.T) {
	optimistic := RecordMutationCommit{ExpectedUpdatedAt: "legacy", Optimistic: OptimisticPrecondition{ExpectedUpdatedAt: "canonical"}}
	if optimistic.OptimisticUpdatedAt() != "canonical" || (RecordMutationCommit{ExpectedUpdatedAt: "legacy"}).OptimisticUpdatedAt() != "legacy" {
		t.Fatal("optimistic timestamp fallback mismatch")
	}
	context, err := NewMutationContext(MutationContextInput{
		WorkspaceID: "workspace", ActorID: "actor", RoleKey: "role", Permissions: []string{"", "read", "read"},
		IdentityVersion: "v1", Source: MutationSourceWorkflow, WorkflowKey: "workflow",
		RequestID: "request", IdempotencyKey: "idem", CorrelationID: "correlation", CausationID: "cause", ApplicationSchemaRevision: "revision",
		EffectAuthority:   map[string][]string{"": {"ignored"}, "order": {"status"}},
		AssuranceEvidence: map[string]string{"": "ignored", " method ": " otp "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if context.IdentityVersion() != "v1" || context.WorkflowKey() != "workflow" || context.AutomationKey() != "" || context.RequestID() != "request" || context.IdempotencyKey() != "idem" || context.CorrelationID() != "correlation" || context.CausationID() != "cause" {
		t.Fatalf("context getters=%+v", context)
	}
	if !context.HasEffectAuthority() || len(context.EffectAuthority()) != 1 || context.AssuranceEvidence()["method"] != "otp" {
		t.Fatalf("authority=%v assurance=%v", context.EffectAuthority(), context.AssuranceEvidence())
	}
	if (MutationContext{}).HasEffectAuthority() {
		t.Fatal("empty authority reported present")
	}
	if (&MutationContextError{Code: "code", Field: "field"}).Error() != "code: field" {
		t.Fatal("mutation context error formatting mismatch")
	}
}
