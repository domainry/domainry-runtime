package runtime

import (
	"context"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/logging"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

func (a *Runtime) startLifecycleCleanupWorker(ctx context.Context) {
	if a == nil || a.records == nil || a.records.Applications().Lifecycle == nil {
		return
	}
	a.startTrackedWorker(ctx, func(workerCtx context.Context) <-chan struct{} {
		return workerplatform.StartNamedLoop(workerCtx, "lifecycle_cleanup", 5*time.Minute, func() {
			now := a.worker.Clock.Now()
			scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "process lifecycle cleanup jobs")
			runLifecycleCleanupTick(workerCtx, a.worker.Control,
				func() error {
					_, err := a.records.Applications().Lifecycle.ProcessRunnableCleanupJobs(workerCtx, a.worker.WorkerID.String(), 100, 25, now, scope)
					return err
				},
				func() error {
					_, err := a.records.Applications().Lifecycle.CleanupExpiredSubjectArtifacts(workerCtx, now, scope)
					return err
				},
			)
		})
	})
}

func runLifecycleCleanupTick(ctx context.Context, control *workerplatform.Controller, process, cleanup func() error) {
	control.RunIfAccepting(func() {
		if err := process(); err != nil && ctx.Err() == nil {
			logging.FromContext(ctx).Error("lifecycle cleanup worker failed", logging.StableErrorFields(err)...)
		}
		if err := cleanup(); err != nil && ctx.Err() == nil {
			logging.FromContext(ctx).Error("lifecycle artifact cleanup failed", logging.StableErrorFields(err)...)
		}
	})
}
