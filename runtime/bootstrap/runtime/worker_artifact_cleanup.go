package runtime

import (
	"context"
	"errors"
	"time"

	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/blobstore"
	artifactstore "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/artifact"
)

const runtimeArtifactCleanupBatchSize = 500

type runtimeArtifactCleanupTarget struct {
	owner string
	kind  string
}

var runtimeArtifactCleanupTargets = []runtimeArtifactCleanupTarget{
	{owner: sharedartifact.OwnerAudit, kind: "export"},
	{owner: sharedartifact.OwnerLifecycle, kind: "archive"},
	{owner: sharedartifact.OwnerLifecycle, kind: "subject_export"},
	{owner: sharedartifact.OwnerOperations, kind: "result"},
	{owner: sharedartifact.OwnerReport, kind: "export"},
}

func (a *Runtime) startArtifactCleanupWorker(ctx context.Context) {
	if a == nil || a.store == nil || a.blobStore == nil {
		return
	}
	cleanup, err := sharedartifact.NewCleanupService(
		artifactstore.NewStore(a.store),
		blobstore.LifecycleContentStore{Blobs: a.blobStore},
	)
	if err != nil {
		return
	}
	a.startControlledWorker(ctx, "artifact_cleanup", func(workerCtx context.Context) <-chan struct{} {
		interval := a.cfg.EffectiveWorkerPollInterval()
		if interval < time.Minute {
			interval = time.Minute
		}
		return workerplatform.StartAdaptiveLoop(workerCtx, "artifact_cleanup", interval, max(5*time.Minute, interval*10), func() bool {
			return runLoggedRuntimeWorkerTickWork(workerCtx, a.worker.Control, "artifact cleanup worker failed", func() (bool, error) {
				result, err := reconcileRuntimeArtifactContent(workerCtx, cleanup, a.worker.Clock.Now(), runtimeArtifactCleanupBatchSize)
				return result.Expired > 0 || result.Deleted > 0, err
			})
		})
	})
}

func reconcileRuntimeArtifactContent(ctx context.Context, cleanup *sharedartifact.CleanupService, now time.Time, limit int) (sharedartifact.CleanupResult, error) {
	result := sharedartifact.CleanupResult{}
	var reconcileErr error
	for _, target := range runtimeArtifactCleanupTargets {
		ownerResult, err := cleanup.Reconcile(ctx, target.owner, target.kind, now, limit)
		result.Scanned += ownerResult.Scanned
		result.Expired += ownerResult.Expired
		result.Deleted += ownerResult.Deleted
		reconcileErr = errors.Join(reconcileErr, err)
	}
	return result, reconcileErr
}
