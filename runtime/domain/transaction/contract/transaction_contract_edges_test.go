package contract

import (
	"context"
	"errors"
	"testing"
)

func TestRegisterAfterCommitRejectsMissingRegistryAndInvalidHooks(t *testing.T) {
	if err := RegisterAfterCommit(t.Context(), AfterCommitHook{}); !errors.Is(err, ErrAfterCommitRegistryMissing) {
		t.Fatalf("missing registry error = %v", err)
	}
	ctx, registry := WithAfterCommitRegistry(t.Context())
	run := func(context.Context) error { return nil }
	for _, hook := range []AfterCommitHook{
		{Purpose: AfterCommitDurableWorkWakeup, DurableRecovery: true, Run: run},
		{Name: "hook", Purpose: AfterCommitDurableWorkWakeup, DurableRecovery: true},
		{Name: "hook", Purpose: "unsupported", DurableRecovery: true, Run: run},
	} {
		if err := RegisterAfterCommit(ctx, hook); !errors.Is(err, ErrAfterCommitHookInvalid) {
			t.Fatalf("invalid hook %#v error = %v", hook, err)
		}
	}
	if err := RegisterAfterCommit(ctx, AfterCommitHook{Name: "hook", Purpose: AfterCommitDurableWorkWakeup, Run: run}); !errors.Is(err, ErrAfterCommitRecoveryRequired) {
		t.Fatalf("missing recovery error = %v", err)
	}
	if hooks := (*AfterCommitRegistry)(nil).Hooks(); hooks != nil {
		t.Fatalf("nil registry hooks = %#v", hooks)
	}
	for _, purpose := range []AfterCommitPurpose{AfterCommitDurableWorkWakeup, AfterCommitCacheInvalidation, AfterCommitLocalProjectionRefresh} {
		if err := RegisterAfterCommit(ctx, AfterCommitHook{Name: " hook ", Purpose: purpose, DurableRecovery: true, Run: run}); err != nil {
			t.Fatalf("purpose %q error = %v", purpose, err)
		}
	}
	if hooks := registry.Hooks(); len(hooks) != 3 || hooks[0].Name != "hook" {
		t.Fatalf("registered hooks = %#v", hooks)
	}
}

func TestTransactionRetryIdentityHandlesNilAndIncompleteContexts(t *testing.T) {
	complete := TransactionRetryIdentity{
		IdempotencyScope: TransactionIdempotencyScope{WorkspaceID: "workspace", UseCase: "record.update", ResourceType: "record", Key: "key"},
		CorrelationID:    "correlation",
	}
	if ctx := WithTransactionRetryIdentity(nil, complete); ctx != nil {
		t.Fatalf("nil context became %#v", ctx)
	}
	ctx := t.Context()
	if got := WithTransactionRetryIdentity(ctx, TransactionRetryIdentity{}); got != ctx {
		t.Fatal("incomplete identity changed context")
	}
	if identity, ok := TransactionRetryIdentityFromContext(nil); ok || identity.IsComplete() {
		t.Fatalf("nil context identity=%#v ok=%v", identity, ok)
	}
	stored := WithTransactionRetryIdentity(t.Context(), complete)
	if identity, ok := TransactionRetryIdentityFromContext(stored); !ok || !identity.IsComplete() || identity.CorrelationID != "correlation" {
		t.Fatalf("stored identity=%#v ok=%v", identity, ok)
	}
}
