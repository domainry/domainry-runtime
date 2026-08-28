package recordmutation

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/mutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

type mutationPlanStoreProbe struct {
	workspace string
	commits   []transactionmodel.RecordMutationCommit
	single    int
	batch     int
	err       error
}

type mutationCommitterScriptedContext struct {
	calls int
}

func (*mutationCommitterScriptedContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*mutationCommitterScriptedContext) Done() <-chan struct{}       { return nil }
func (c *mutationCommitterScriptedContext) Err() error {
	c.calls++
	if c.calls >= 3 {
		return context.Canceled
	}
	return nil
}
func (*mutationCommitterScriptedContext) Value(any) any { return nil }

func (p *mutationPlanStoreProbe) CommitRecordMutation(_ context.Context, workspace string, commit transactionmodel.RecordMutationCommit) error {
	p.workspace, p.commits, p.single = workspace, []transactionmodel.RecordMutationCommit{commit}, p.single+1
	return p.err
}

func TestMutationKernelPlansAndCommitsThroughCanonicalBoundaries(t *testing.T) {
	store := &mutationPlanStoreProbe{}
	kernel := NewMutationKernelApplicationService(store, func(context.Context, principalmodel.Principal) (string, error) { return "revision-1", nil })
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a", UserID: "user"}}
	commit := transactionmodel.RecordMutationCommit{Operation: "create", Object: definitionmodel.ObjectSchema{Key: "order"}, Record: recordmodel.Record{ID: "order-1"}}
	plan, err := kernel.Plan(t.Context(), principal, commit, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kernel.Commit(t.Context(), principal, commit, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := kernel.CommitPlan(t.Context(), plan, nil); err != nil {
		t.Fatal(err)
	}
	if err := kernel.CommitBatch(t.Context(), []transactionmodel.MutationPlan{plan}, nil); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("store unavailable")
	store.err = wantErr
	if _, err := kernel.Commit(t.Context(), principal, commit, nil, nil); !errors.Is(err, wantErr) {
		t.Fatalf("commit error=%v", err)
	}
	planning := NewMutationKernelApplicationService(store, func(context.Context, principalmodel.Principal) (string, error) { return "", wantErr })
	if _, err := planning.Commit(t.Context(), principal, commit, nil, nil); !errors.Is(err, wantErr) {
		t.Fatalf("planning error=%v", err)
	}
}

func (p *mutationPlanStoreProbe) CommitRecordMutationBatch(_ context.Context, workspace string, commits []transactionmodel.RecordMutationCommit) error {
	p.workspace, p.commits, p.batch = workspace, commits, p.batch+1
	return p.err
}

func TestMutationCommitterUsesSingleAndBatchStoreBoundaries(t *testing.T) {
	store := &mutationPlanStoreProbe{}
	service := NewMutationCommitterApplicationService(store)
	first := mutationCommitterTestPlan(t, "workspace-a", "order-1")
	second := mutationCommitterTestPlan(t, "workspace-a", "order-2")
	if err := service.CommitMutationPlan(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if store.single != 1 || store.workspace != "workspace-a" || store.commits[0].Record.ID != "order-1" {
		t.Fatalf("single probe=%+v", store)
	}
	if err := service.CommitMutationBatch(context.Background(), []transactionmodel.MutationPlan{first, second}, nil); err != nil {
		t.Fatal(err)
	}
	if store.batch != 1 || len(store.commits) != 2 || store.commits[1].Record.ID != "order-2" {
		t.Fatalf("batch probe=%+v", store)
	}
}

func TestMutationCommitterReceiptOwnsAtomicBatchCommit(t *testing.T) {
	service := NewMutationCommitterApplicationService(nil)
	plans := []transactionmodel.MutationPlan{mutationCommitterTestPlan(t, "workspace-a", "order-1")}
	called := false
	err := service.CommitMutationBatch(context.Background(), plans, func(_ context.Context, commits []transactionmodel.RecordMutationCommit) error {
		called = len(commits) == 1 && commits[0].Record.ID == "order-1"
		return nil
	})
	if err != nil || !called {
		t.Fatalf("called=%v err=%v", called, err)
	}
}

func TestMutationCommitterReceiptOwnsAtomicSingleCommit(t *testing.T) {
	var service *MutationCommitterApplicationService
	plan := mutationCommitterTestPlan(t, "workspace-a", "order-1")
	called := false
	err := service.CommitMutationPlanWithReceipt(t.Context(), plan, func(_ context.Context, commit transactionmodel.RecordMutationCommit) error {
		called = commit.Record.ID == "order-1"
		return nil
	})
	if err != nil || !called {
		t.Fatalf("called=%v err=%v", called, err)
	}
}

func TestMutationCommitterRejectsInvalidInvocationAndCancellation(t *testing.T) {
	service := NewMutationCommitterApplicationService(&mutationPlanStoreProbe{})
	for _, test := range []struct {
		name  string
		call  func() error
		field string
	}{
		{name: "empty batch", call: func() error { return service.CommitMutationBatch(context.Background(), nil, nil) }, field: "plans"},
		{name: "missing workspace", call: func() error {
			return service.CommitMutationPlan(context.Background(), transactionmodel.MutationPlan{})
		}, field: "workspace_id"},
		{name: "mixed workspace", call: func() error {
			return service.CommitMutationBatch(context.Background(), []transactionmodel.MutationPlan{mutationCommitterTestPlan(t, "workspace-a", "order-1"), mutationCommitterTestPlan(t, "workspace-b", "order-2")}, nil)
		}, field: "workspace_mismatch"},
		{name: "missing store", call: func() error {
			return NewMutationCommitterApplicationService(nil).CommitMutationPlan(context.Background(), mutationCommitterTestPlan(t, "workspace-a", "order-1"))
		}, field: "store_unavailable"},
		{name: "nil service single", call: func() error {
			var nilService *MutationCommitterApplicationService
			return nilService.CommitMutationPlan(context.Background(), mutationCommitterTestPlan(t, "workspace-a", "order-1"))
		}, field: "store_unavailable"},
		{name: "missing batch store", call: func() error {
			return NewMutationCommitterApplicationService(nil).CommitMutationBatch(context.Background(), []transactionmodel.MutationPlan{mutationCommitterTestPlan(t, "workspace-a", "order-1")}, nil)
		}, field: "store_unavailable"},
		{name: "nil service batch", call: func() error {
			var nilService *MutationCommitterApplicationService
			return nilService.CommitMutationBatch(context.Background(), []transactionmodel.MutationPlan{mutationCommitterTestPlan(t, "workspace-a", "order-1")}, nil)
		}, field: "store_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var typed *MutationCommitterError
			err := test.call()
			if !errors.As(err, &typed) || typed.Code != "backend.mutation.commit_invalid" || typed.Field != test.field || err.Error() == "" {
				t.Fatalf("error=%#v", err)
			}
		})
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.CommitMutationPlan(cancelled, mutationCommitterTestPlan(t, "workspace-a", "order-1")); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%#v", err)
	}
	if err := service.CommitMutationBatch(cancelled, []transactionmodel.MutationPlan{mutationCommitterTestPlan(t, "workspace-a", "order-1")}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("batch error=%#v", err)
	}
}

func TestMutationCommitterRetriesOnlyBoundedTransientFailures(t *testing.T) {
	service := NewMutationCommitterApplicationService(nil)
	plan := mutationCommitterTestPlan(t, "workspace-a", "order-1")
	attempts := 0
	err := service.CommitMutationBatch(t.Context(), []transactionmodel.MutationPlan{plan}, func(context.Context, []transactionmodel.RecordMutationCommit) error {
		attempts++
		if attempts < 3 {
			return mutation.TransactionTransient("record", "order-1", mutation.TransactionTransientSerializationFailure, errors.New("retry"))
		}
		return nil
	})
	if err != nil || attempts != canonicalMutationCommitMaxAttempts {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}

	attempts = 0
	want := mutation.MutationConflict("record", "order-1", mutation.MutationConflictOptimistic, nil)
	err = service.CommitMutationBatch(t.Context(), []transactionmodel.MutationPlan{plan}, func(context.Context, []transactionmodel.RecordMutationCommit) error {
		attempts++
		return want
	})
	if !errors.Is(err, want) || attempts != 1 {
		t.Fatalf("non-transient attempts=%d err=%v", attempts, err)
	}

	attempts = 0
	transient := mutation.TransactionTransient("record", "order-1", mutation.TransactionTransientSerializationFailure, errors.New("retry exhausted"))
	err = service.CommitMutationBatch(t.Context(), []transactionmodel.MutationPlan{plan}, func(context.Context, []transactionmodel.RecordMutationCommit) error {
		attempts++
		return transient
	})
	if !errors.Is(err, transient) || attempts != canonicalMutationCommitMaxAttempts {
		t.Fatalf("exhausted attempts=%d err=%v", attempts, err)
	}
}

func TestMutationCommitterCancellationDuringAndBetweenRetries(t *testing.T) {
	service := NewMutationCommitterApplicationService(nil)
	plan := mutationCommitterTestPlan(t, "workspace-a", "order-1")

	cancelled, cancel := context.WithCancel(t.Context())
	attempts := 0
	err := service.CommitMutationBatch(cancelled, []transactionmodel.MutationPlan{plan}, func(context.Context, []transactionmodel.RecordMutationCommit) error {
		attempts++
		cancel()
		return mutation.TransactionTransient("record", "order-1", mutation.TransactionTransientDeadlock, errors.New("retry"))
	})
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("during delay attempts=%d err=%v", attempts, err)
	}

	scripted := &mutationCommitterScriptedContext{}
	attempts = 0
	err = service.CommitMutationBatch(scripted, []transactionmodel.MutationPlan{plan}, func(context.Context, []transactionmodel.RecordMutationCommit) error {
		attempts++
		return mutation.TransactionTransient("record", "order-1", mutation.TransactionTransientLockTimeout, errors.New("retry"))
	})
	if !errors.Is(err, context.Canceled) || attempts != 1 || scripted.calls < 3 {
		t.Fatalf("between attempts=%d context calls=%d err=%v", attempts, scripted.calls, err)
	}
}

func mutationCommitterTestPlan(t *testing.T, workspace, recordID string) transactionmodel.MutationPlan {
	t.Helper()
	mutationContext, err := transactionmodel.NewMutationContext(transactionmodel.MutationContextInput{WorkspaceID: workspace, Source: transactionmodel.MutationSourceHTTP, CorrelationID: "correlation-1", MetadataRevision: "revision-1"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := transactionmodel.NewMutationPlan(mutationContext, transactionmodel.RecordMutationCommit{Operation: "create", Object: definitionmodel.ObjectSchema{Key: "order"}, Record: recordmodel.Record{ID: recordID}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
