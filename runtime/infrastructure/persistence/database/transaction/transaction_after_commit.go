package transaction

import (
	"context"
	"sync"

	"github.com/domainry/domainry-foundation/logging"
	"github.com/domainry/domainry-foundation/requestcontext"
	transactioncontract "github.com/domainry/domainry-runtime/runtime/domain/transaction/contract"
	"go.uber.org/zap"
)

type afterCommitFailureStore struct {
	mu       sync.RWMutex
	failures []transactioncontract.AfterCommitFailure
}

func (store *afterCommitFailureStore) append(ctx context.Context, hook transactioncontract.AfterCommitHook, cause error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.failures = append(store.failures, transactioncontract.AfterCommitFailure{
		Name: hook.Name, Purpose: hook.Purpose, DurableRecovery: hook.DurableRecovery,
		RequestID: requestcontext.RequestID(ctx), Cause: cause,
	})
}

func (store *afterCommitFailureStore) snapshot() []transactioncontract.AfterCommitFailure {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return append([]transactioncontract.AfterCommitFailure(nil), store.failures...)
}

func (u *SQLUnitOfWork[Ports]) runAfterCommitHooks(ctx context.Context, hooks []transactioncontract.AfterCommitHook) {
	for _, hook := range hooks {
		if err := hook.Run(ctx); err != nil {
			u.afterCommitFailures.append(ctx, hook, err)
			fields := logging.ErrorFields(err, logging.Failure{Stage: "after_commit_hook", Retryable: hook.DurableRecovery})
			fields = append(fields,
				zap.String("hook_name", hook.Name),
				zap.String("hook_purpose", string(hook.Purpose)),
				zap.Bool("durable_recovery", hook.DurableRecovery),
			)
			logging.FromContext(ctx).Warn("after_commit_hook_failed", fields...)
		}
	}
}

func (u *SQLUnitOfWork[Ports]) AfterCommitFailures() []transactioncontract.AfterCommitFailure {
	if u == nil || u.afterCommitFailures == nil {
		return nil
	}
	return u.afterCommitFailures.snapshot()
}
