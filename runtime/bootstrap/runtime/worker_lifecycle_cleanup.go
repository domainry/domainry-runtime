package runtime

import (
	"context"
	"time"

	"github.com/domainry/domainry-foundation/logging"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	lifecycleapplication "github.com/domainry/domainry-runtime/runtime/application/lifecycle"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (a *Runtime) startLifecycleCleanupWorker(ctx context.Context) {
	if a == nil || a.records == nil || a.records.Applications().Lifecycle == nil {
		return
	}
	a.startTrackedWorker(ctx, func(workerCtx context.Context) <-chan struct{} {
		runner := lifecycleapplication.NewWorkerRunner(a.records.Applications().Lifecycle)
		return workerplatform.StartNamedLoop(workerCtx, "lifecycle_cleanup", 5*time.Minute, func() {
			now := a.worker.Clock.Now()
			scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "process lifecycle cleanup jobs")
			runLifecycleCleanupTick(workerCtx, a.worker.Control, func() error {
				_, err := runner.Tick(workerCtx, lifecycleapplication.WorkerTick{LeaseOwner: a.worker.WorkerID.String(), BatchSize: 100, JobLimit: 25, Now: now, Scope: scope})
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
