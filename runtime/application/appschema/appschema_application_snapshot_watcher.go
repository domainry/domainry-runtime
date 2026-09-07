package appschema

import (
	"context"
	"errors"
	"time"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	appschemaservice "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// StartSnapshotWatcher invalidates the local immutable schema projection when
// another Runtime publishes a new database metadata revision.
func (s *ApplicationSchemaApplicationService) StartSnapshotWatcher(ctx context.Context, interval time.Duration, scope principalmodel.SystemScope) <-chan struct{} {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return workerplatform.Stopped()
	}
	dependencies := ApplicationSchemaSnapshotWatcherDependencies{}
	if s != nil && s.repository != nil {
		dependencies.Revision = func(ctx context.Context) (string, error) {
			return s.repository.SnapshotRevision(ctx, scope)
		}
		dependencies.Reload = s.reloadMetadataFromSource
	}
	watcher := newApplicationSchemaSnapshotWatcherApplicationService(dependencies)
	return watcher.Start(ctx, interval)
}

func (s *ApplicationSchemaApplicationService) reloadMetadataFromSource(ctx context.Context) error {
	manifest, err := s.repository.LoadManifest(ctx, metadataInstallationScope("refresh metadata snapshot"))
	if err != nil {
		return err
	}
	if err := manifest.ValidateTimeZone(); err != nil {
		return err
	}
	templateID, version, name := valueOrDefault(manifest.TemplateID, s.templateID), valueOrDefault(manifest.Version, s.version), valueOrDefault(manifest.Name, s.name)
	candidate := appschemaservice.BuildSchemaSnapshot(appschemaservice.SchemaSnapshotState{
		TemplateID: templateID, TemplateVersion: version, Name: name, TimeZone: manifest.EffectiveTimeZone(),
		Objects: manifest.Objects, Actions: manifest.Actions, Workflows: manifest.Workflows,
		AutomationRules: manifest.AutomationRules, Dictionaries: manifest.Dictionaries, Integrations: manifest.Integrations,
		Reports: manifest.Reports, Skills: manifest.Skills, Agents: manifest.Agents,
		AgentTasks: manifest.AgentTasks, AgentEntrypoints: manifest.AgentEntrypoints, AgentServicePrincipals: manifest.AgentServicePrincipals,
		IdentityProfileExtensions: manifest.IdentityProfileExtensions,
	})
	preparations, err := s.prepareReloadObservers(ctx, candidate)
	if err != nil {
		return err
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "refresh published workflow definitions from metadata snapshot")
	if err := s.workflows.InitializePublishedWorkflowDefinitions(ctx, manifest.Workflows, scope); err != nil {
		return errors.Join(err, abortReloadObservers(context.WithoutCancel(ctx), preparations))
	}
	s.runtime.ApplyManifestMetadata(templateID, version, name, candidate.TimeZone, candidate.Objects, candidate.Actions, candidate.Workflows, candidate.AutomationRules, candidate.Dictionaries, candidate.Integrations, candidate.Reports, candidate.Skills, candidate.Agents, candidate.IdentityProfileExtensions)
	applyManifestAgentMetadata(s.runtime, manifest)
	for _, preparation := range preparations {
		if preparation.Commit != nil {
			preparation.Commit()
		}
	}
	return nil
}
