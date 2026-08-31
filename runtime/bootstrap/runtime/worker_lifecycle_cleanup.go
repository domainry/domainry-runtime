package runtime

import (
	"context"
	"time"

	"github.com/domainry/domainry-foundation/logging"
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
		return workerplatform.StartNamedLoop(workerCtx, "lifecycle_cleanup", 5*time.Minute, func() {
			now := a.worker.Clock.Now()
			scope := lifecycleaccess.NewSystemScope(lifecycleaccess.SystemScopeGlobal, "process lifecycle cleanup jobs")
			runLifecycleCleanupTick(workerCtx, a.worker.Control, func() error {
				_, err := workers.Tick(workerCtx, lifecyclesdk.WorkerTick{LeaseOwner: a.worker.WorkerID.String(), BatchSize: 100, JobLimit: 25, Now: now, Scope: scope})
				return err
			}, nil)
		})
	})
}

func runLifecycleCleanupTick(ctx context.Context, control *workerplatform.Controller, process, cleanup func() error) {
	control.RunIfAccepting(func() {
		for _, tick := range []func() error{process, cleanup} {
			if tick == nil {
				continue
			}
			if err := tick(); err != nil && ctx.Err() == nil {
				logging.FromContext(ctx).Error("lifecycle cleanup worker failed", logging.StableErrorFields(err)...)
			}
		}
	})
}
