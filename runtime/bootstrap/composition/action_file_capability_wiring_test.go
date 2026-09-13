package composition

import (
	"context"
	"database/sql"
	"testing"

	lifecyclemodulehost "github.com/domainry/domainry-lifecycle-sdk/modulehost"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type actionFileCapabilityExecutorStub struct{}

func (*actionFileCapabilityExecutorStub) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, nil
}

func (*actionFileCapabilityExecutorStub) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, nil
}

func (*actionFileCapabilityExecutorStub) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}

func TestActionFileCapabilitySharesActionTransactionWithLifecycleStore(t *testing.T) {
	executor := &actionFileCapabilityExecutorStub{}
	called := false
	port := bindActionCreateDerivedFile(func(ctx context.Context, workspaceID string, request runtimeext.DerivedFileRequest) (runtimeext.DerivedFileEvidence, error) {
		called = true
		if workspaceID != "workspace-a" || request.IdempotencyKey != "derived-a" {
			t.Fatal("file capability request changed while binding the transaction")
		}
		if got := lifecyclemodulehost.ExecutorFromContext(ctx, nil); got != executor {
			t.Fatal("lifecycle file store did not receive the Action transaction executor")
		}
		return runtimeext.DerivedFileEvidence{}, nil
	})
	ctx := database.WithActionExecutionTransaction(t.Context(), executor)
	if _, err := port(ctx, "workspace-a", runtimeext.DerivedFileRequest{IdempotencyKey: "derived-a"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("file capability port was not called")
	}
}

func TestActionFileCapabilityBindingsPreserveUnavailablePorts(t *testing.T) {
	if bindActionVerifyFileClean(nil) != nil || bindActionOpenVerifiedFile(nil) != nil || bindActionIssueFileDownload(nil) != nil || bindActionCreateDerivedFile(nil) != nil {
		t.Fatal("an unavailable file capability became callable")
	}
}
