package runtime

import (
	"context"
	"time"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
)

func (a *Runtime) startAccountErasureWorker(ctx context.Context) {
	if a == nil || a.lifecycleBinding == nil {
		return
	}
	binding, ok := a.lifecycleBinding.(lifecyclesdk.AccountErasureBinding)
	if !ok || binding.AccountErasures() == nil {
		return
	}
	queue := binding.AccountErasures()
	a.startControlledWorker(ctx, "account_erasure", func(workerCtx context.Context) <-chan struct{} {
		return workerplatform.StartAdaptiveLoop(workerCtx, "account_erasure", time.Second, 30*time.Second, func() bool {
			return runLoggedRuntimeWorkerTickWork(workerCtx, a.worker.Control, "approved account erasure worker failed", func() (bool, error) {
				count, err := queue.ProcessApprovedAccountErasures(workerCtx, a.worker.WorkerID.String(), 10, a.worker.Clock.Now(),
					lifecycleaccess.NewSystemScope(lifecycleaccess.SystemScopeGlobal, "process committed account erasure approvals"))
				return count > 0, err
			})
		})
	})
}
