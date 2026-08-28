package workflow

import (
	"context"
	"errors"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	"testing"
)

func TestWorkflowWorkerStoreImplementsContractAndCancelsSQL(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	repository := NewWorkflowWorkerStore(store)
	var _ workflowcontract.WorkflowWorkerStore = repository
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := repository.ListExecutions(ctx, "workspace-a", 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("execution query error=%v, want context.Canceled", err)
	}
	if _, err := repository.ListTasks(ctx, "workspace-a", "", "", "open", 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("task query error=%v, want context.Canceled", err)
	}
}
