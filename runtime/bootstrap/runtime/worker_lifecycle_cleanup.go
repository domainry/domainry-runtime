package runtime

import (
	"context"
	"errors"
	"time"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
)

func (a *Runtime) startLifecycleCleanupWorker(ctx context.Context) {
	if a == nil || a.lifecycleBinding == nil {
		return
	}
	workers, ok := a.lifecycleBinding.LocalWorkers()
	if !ok || workers == nil {
		return
	}
	a.startTrackedWorker(ctx, func(workerCtx context.Context) <-chan struct{} {
		return workerplatform.StartNamedLoop(workerCtx, runtimeWorkerIdentity("lifecycle_cleanup"), 5*time.Minute, func(tickCtx context.Context) (workerplatform.TickResult, error) {
			now := a.worker.Clock.Now()
			scope := lifecycleaccess.NewSystemScope(lifecycleaccess.SystemScopeGlobal, "process lifecycle cleanup jobs")
			return runLifecycleCleanupTick(tickCtx, a.worker.Control, func() (int, error) {
				result, err := workers.Tick(tickCtx, lifecyclesdk.WorkerTick{LeaseOwner: a.worker.WorkerID.String(), BatchSize: 100, JobLimit: 25, Now: now, Scope: scope})
				return result.ProcessedJobs + result.DeletedArtifacts, err
			}, nil)
		})
	})
}

func runLifecycleCleanupTick(ctx context.Context, control *workerplatform.Controller, process, cleanup func() (int, error)) (workerplatform.TickResult, error) {
	return runControlledRuntimeWorkerTick(ctx, control, func(context.Context) (workerplatform.TickResult, error) {
		processed := 0
		var joined error
		for _, tick := range []func() (int, error){process, cleanup} {
			if tick == nil {
				continue
			}
			count, err := tick()
			processed += count
			joined = errors.Join(joined, err)
		}
		return workerplatform.TickResult{Worked: processed > 0, ProcessedCount: processed}, joined
	})
}
