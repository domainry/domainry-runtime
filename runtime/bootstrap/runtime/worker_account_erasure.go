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
		return workerplatform.StartAdaptiveLoop(workerCtx, runtimeWorkerIdentity("account_erasure"), time.Second, 30*time.Second, func(tickCtx context.Context) (workerplatform.TickResult, error) {
			return runControlledRuntimeWorkerTick(tickCtx, a.worker.Control, func(operationCtx context.Context) (workerplatform.TickResult, error) {
				count, err := queue.ProcessApprovedAccountErasures(operationCtx, a.worker.WorkerID.String(), 10, a.worker.Clock.Now(),
					lifecycleaccess.NewSystemScope(lifecycleaccess.SystemScopeGlobal, "process committed account erasure approvals"))
				return workerplatform.TickResult{Worked: count > 0, ProcessedCount: count}, err
			})
		})
	})
}
