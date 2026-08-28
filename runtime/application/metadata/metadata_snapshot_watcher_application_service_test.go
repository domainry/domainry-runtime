package metadata

import (
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"

	"context"
	"errors"
	"sync"
	"testing"
	"time"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type metadataWatcherRepository struct {
	metadatarepository.MetadataRepository
	mu       sync.RWMutex
	revision string
	manifest manifestmodel.ManifestSchema
}

func (r *metadataWatcherRepository) SnapshotRevision(ctx context.Context, _ principalmodel.SystemScope) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.revision, nil
}

func (r *metadataWatcherRepository) LoadManifest(ctx context.Context, _ principalmodel.SystemScope) (manifestmodel.ManifestSchema, error) {
	if err := ctx.Err(); err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.manifest, nil
}

func (r *metadataWatcherRepository) publish(revision string, manifest manifestmodel.ManifestSchema) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.revision, r.manifest = revision, manifest
}

type metadataWatcherRuntime struct {
	mu    sync.Mutex
	names []string
}

func (r *metadataWatcherRuntime) ApplyManifestMetadata(_ string, _ string, name string, _ []definitionmodel.ObjectSchema, _ []definitionmodel.ViewSchema, _ []definitionmodel.ActionSchema, _ []definitionmodel.WorkflowSchema, _ []automationmodel.AutomationRuleSchema, _ []metadatamodel.DictionarySchema, _ integrationmodel.IntegrationSchema, _ []reportmodel.ReportSchema, _ []definitionmodel.EntryPointSchema, _ []agentmodel.SkillSchema, _ []agentmodel.AgentSchema, _ []profilebindingmodel.Binding) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.names = append(r.names, name)
}

func (r *metadataWatcherRuntime) Schema() metadatamodel.MetadataSchemaSnapshot {
	return metadatamodel.MetadataSchemaSnapshot{}
}

type metadataWatcherWorkflowStub struct{}

func (metadataWatcherWorkflowStub) InitializePublishedWorkflowDefinitions(context.Context, []definitionmodel.WorkflowSchema, principalmodel.SystemScope) error {
	return nil
}

func TestSnapshotWatcherReloadsOnlyAfterSharedRevisionChanges(t *testing.T) {
	repository := &metadataWatcherRepository{revision: "r1", manifest: manifestmodel.ManifestSchema{Name: "one"}}
	runtime := &metadataWatcherRuntime{}
	application := NewMetadataApplicationService(MetadataApplicationDependencies{Repository: repository, Runtime: runtime, Workflows: metadataWatcherWorkflowStub{}})
	ctx, cancel := context.WithCancel(t.Context())
	done := application.StartSnapshotWatcher(ctx, 5*time.Millisecond, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test metadata snapshot watcher"))
	time.Sleep(15 * time.Millisecond)
	runtime.mu.Lock()
	initialReloads := len(runtime.names)
	runtime.mu.Unlock()
	if initialReloads != 0 {
		t.Fatalf("unchanged revision reloaded %d times", initialReloads)
	}
	repository.publish("r2", manifestmodel.ManifestSchema{Name: "two"})
	deadline := time.Now().Add(time.Second)
	for {
		runtime.mu.Lock()
		count := len(runtime.names)
		name := ""
		if count > 0 {
			name = runtime.names[count-1]
		}
		runtime.mu.Unlock()
		if count == 1 && name == "two" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("watcher did not reload revision: count=%d name=%q", count, name)
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(15 * time.Millisecond)
	runtime.mu.Lock()
	finalReloads := len(runtime.names)
	runtime.mu.Unlock()
	if finalReloads != 1 {
		t.Fatalf("stable revision reloaded %d times", finalReloads)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop")
	}
}

func TestSnapshotWatcherCapturesBaselineBeforeStartReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var mu sync.Mutex
	revision := "r1"
	revisionCalls, reloadCalls := 0, 0
	watcher := newMetadataSnapshotWatcherApplicationService(MetadataSnapshotWatcherDependencies{
		Revision: func(context.Context) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			revisionCalls++
			return revision, nil
		},
		Reload: func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			reloadCalls++
			return nil
		},
	})
	done := watcher.Start(ctx, time.Millisecond)
	mu.Lock()
	if revisionCalls != 1 {
		mu.Unlock()
		t.Fatalf("initial revision calls = %d, want 1 before Start returns", revisionCalls)
	}
	revision = "r2"
	mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		calls := reloadCalls
		mu.Unlock()
		if calls == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("publish after Start return was missed: reloads=%d", calls)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
}

func TestSnapshotWatcherBlankBaselineAndTickFailureConditions(t *testing.T) {
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	blank := newMetadataSnapshotWatcherApplicationService(MetadataSnapshotWatcherDependencies{
		Revision: func(context.Context) (string, error) { return " ", nil },
		Reload:   func(context.Context) error { return nil },
	})
	<-blank.Start(cancelled, time.Millisecond)

	for _, cancelDuringTick := range []bool{false, true} {
		name := "live context"
		if cancelDuringTick {
			name = "cancelled context"
		}
		t.Run(name, func(t *testing.T) {
			ctx, stop := context.WithCancel(t.Context())
			calls := 0
			failed := make(chan struct{}, 1)
			watcher := newMetadataSnapshotWatcherApplicationService(MetadataSnapshotWatcherDependencies{
				Revision: func(context.Context) (string, error) {
					calls++
					if calls == 1 {
						return "r1", nil
					}
					if cancelDuringTick {
						stop()
					}
					select {
					case failed <- struct{}{}:
					default:
					}
					return "", errors.New("revision failed")
				},
				Reload: func(context.Context) error { return nil },
			})
			done := watcher.Start(ctx, time.Hour)
			select {
			case <-failed:
			case <-time.After(time.Second):
				t.Fatal("watcher did not execute immediate failure tick")
			}
			stop()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("watcher did not stop")
			}
		})
	}
}
