package metadata

import (
	"context"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/logging"
	workerplatform "github.com/domainry/domainry-foundation/worker"
)

type MetadataSnapshotWatcherDependencies struct {
	Revision func(context.Context) (string, error)
	Reload   func(context.Context) error
}

type metadataSnapshotWatcherApplicationService struct {
	dependencies MetadataSnapshotWatcherDependencies
}

func newMetadataSnapshotWatcherApplicationService(dependencies MetadataSnapshotWatcherDependencies) *metadataSnapshotWatcherApplicationService {
	return &metadataSnapshotWatcherApplicationService{dependencies: dependencies}
}

func (w *metadataSnapshotWatcherApplicationService) Start(ctx context.Context, interval time.Duration) <-chan struct{} {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	last := ""
	if w != nil && w.dependencies.Revision != nil && w.dependencies.Reload != nil {
		revision, err := w.dependencies.Revision(ctx)
		if err != nil {
			if ctx.Err() == nil {
				logging.FromContext(ctx).Error("metadata snapshot watcher revision failed", logging.StableErrorFields(err)...)
			}
		} else if strings.TrimSpace(revision) != "" {
			last = revision
		}
	}
	return workerplatform.StartNamedLoop(ctx, "metadata_snapshot", interval, func() {
		if w == nil || w.dependencies.Revision == nil || w.dependencies.Reload == nil {
			return
		}
		revision, err := w.dependencies.Revision(ctx)
		if err != nil {
			if ctx.Err() == nil {
				logging.FromContext(ctx).Error("metadata snapshot watcher revision failed", logging.StableErrorFields(err)...)
			}
			return
		}
		if strings.TrimSpace(revision) == "" || revision == last {
			return
		}
		if last == "" {
			last = revision
			return
		}
		if err := w.dependencies.Reload(ctx); err != nil {
			if ctx.Err() == nil {
				logging.FromContext(ctx).Error("metadata snapshot watcher reload failed", logging.StableErrorFields(err)...)
			}
			return
		}
		last = revision
	})
}
