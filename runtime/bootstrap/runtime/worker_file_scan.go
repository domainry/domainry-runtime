package runtime

import (
	"context"
	"time"

	workerplatform "github.com/domainry/domainry-foundation/worker"
)

func (a *Runtime) startFileScanWorker(ctx context.Context) {
	if a == nil || !a.schemaCapabilities.Uploads || a.fileScanProcessor == nil {
		return
	}
	a.startTrackedWorker(ctx, func(workerCtx context.Context) <-chan struct{} {
		interval := a.cfg.EffectiveWorkerPollInterval()
		if interval <= 0 {
			interval = time.Second
		}
		batch := a.cfg.EffectiveWorkerBatchSize()
		if batch <= 0 || batch > 100 {
			batch = 25
		}
		return workerplatform.StartNamedLoop(workerCtx, "file_scan", interval, func() {
			runLoggedRuntimeWorkerTickWork(workerCtx, a.worker.Control, "file scan worker failed", func() (bool, error) {
				processed, err := a.fileScanProcessor.ProcessPending(workerCtx, batch)
				return processed > 0, err
			})
		})
	})
}
