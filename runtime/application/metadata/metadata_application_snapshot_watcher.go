package metadata

import (
	"context"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

// StartSnapshotWatcher invalidates the local immutable schema projection when
// another Runtime publishes a new database metadata revision.
func (s *MetadataApplicationService) StartSnapshotWatcher(ctx context.Context, interval time.Duration, scope principalmodel.SystemScope) <-chan struct{} {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return workerplatform.Stopped()
	}
	dependencies := MetadataSnapshotWatcherDependencies{}
	if s != nil && s.repository != nil {
		dependencies.Revision = func(ctx context.Context) (string, error) {
			return s.repository.SnapshotRevision(ctx, scope)
		}
		dependencies.Reload = s.reloadMetadataFromSource
	}
	watcher := newMetadataSnapshotWatcherApplicationService(dependencies)
	return watcher.Start(ctx, interval)
}

func (s *MetadataApplicationService) reloadMetadataFromSource(ctx context.Context) error {
	manifest, err := s.repository.LoadManifest(ctx, metadataInstallationScope("refresh metadata snapshot"))
	if err != nil {
		return err
	}
	s.runtime.ApplyManifestMetadata(valueOrDefault(manifest.TemplateID, s.templateID), valueOrDefault(manifest.Version, s.version), valueOrDefault(manifest.Name, s.name), manifest.Objects, manifest.Views, manifest.Actions, manifest.Workflows, manifest.AutomationRules, manifest.Dictionaries, manifest.Integrations, manifest.Reports, manifest.EntryPoints, manifest.Skills, manifest.Agents, manifest.IdentityProfileExtensions)
	applyManifestAgentMetadata(s.runtime, manifest)
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "refresh published workflow definitions from metadata snapshot")
	if err := s.workflows.InitializePublishedWorkflowDefinitions(ctx, manifest.Workflows, scope); err != nil {
		return err
	}
	s.notifyReloadObservers(s.runtime.Schema())
	return nil
}
