package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/requestcontext"
	"github.com/domainry/domainry-foundation/worker"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type externalWorkforceDirectoryStub struct{ runtimeIdentityDirectoryStub }

func (externalWorkforceDirectoryStub) ListWorkforce(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.WorkforceEntry, error) {
	return []identitysdk.WorkforceEntry{}, nil
}

var _ identitysdk.Directory = externalWorkforceDirectoryStub{}

func TestRuntimeSeedSynchronizationBindsInstallationWorkspace(t *testing.T) {
	ctx := runtimeSeedSynchronizationContext(requestcontext.WithWorkspaceID(t.Context(), "stale-workspace"))
	if got := requestcontext.WorkspaceID(ctx); got != principalmodel.InstallationWorkspaceID {
		t.Fatalf("seed workspace=%q", got)
	}
}

func TestAssembleRuntimeServicesReportsWorkflowFailures(t *testing.T) {
	cfg := bootstrapTestConfig(t)
	manifest, err := prepareRuntimeManifest(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	store, err := prepareRuntimeStore(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := assembleRuntimeServices(cancelled, cfg, manifest, nil, store, runtimeIdentityDirectoryStub{}, nil, nil, nil, nil, worker.Dependencies{}); err == nil {
		t.Fatal("cancelled workflow initialization must fail assembly")
	}
}

func TestCompleteRuntimeServiceAssemblyPropagatesEveryCompletionStage(t *testing.T) {
	failure := errors.New("service assembly failure")
	success := func() error { return nil }
	for _, test := range []struct {
		name                string
		installLifecycle    func() error
		initializeWorkflows func() error
	}{
		{name: "lifecycle", installLifecycle: func() error { return failure }, initializeWorkflows: success},
		{name: "workflows", installLifecycle: success, initializeWorkflows: func() error { return failure }},
	} {
		t.Run(test.name, func(t *testing.T) {
			configured := false
			if _, err := completeRuntimeServiceAssembly(runtimeServiceAssembly{}, test.installLifecycle, func() { configured = true }, test.initializeWorkflows); !errors.Is(err, failure) {
				t.Fatalf("completion error=%v", err)
			}
			if configured != (test.name == "workflows") {
				t.Fatalf("configured=%t", configured)
			}
		})
	}
	want := runtimeServiceAssembly{}
	configured := false
	if got, err := completeRuntimeServiceAssembly(want, success, func() { configured = true }, success); err != nil || !configured || got.services != want.services {
		t.Fatalf("successful completion=%#v configured=%t error=%v", got, configured, err)
	}
}

func TestSynchronizeRuntimeSeedsRejectsClosedStore(t *testing.T) {
	cfg := bootstrapTestConfig(t)
	manifest, err := prepareRuntimeManifest(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	store, err := prepareRuntimeStore(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := synchronizeRuntimeSeeds(t.Context(), store, manifest, true, externalWorkforceDirectoryStub{}); err == nil {
		t.Fatal("closed store must fail seed synchronization")
	}
}

func TestSynchronizeRuntimeSeedsRequiresSDKDirectoryForBusinessSeeds(t *testing.T) {
	cfg := bootstrapTestConfig(t)
	manifest, err := prepareRuntimeManifest(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	store, err := prepareRuntimeStore(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := synchronizeRuntimeSeeds(t.Context(), store, manifest, true, nil); err == nil {
		t.Fatal("business seed synchronization accepted a missing Identity SDK directory")
	}
	if err := synchronizeRuntimeSeeds(t.Context(), store, manifest, false, nil); err != nil {
		t.Fatalf("disabled business seed synchronization required a directory: %v", err)
	}
}

func TestCompleteRuntimeSeedSynchronizationPropagatesEveryStageFailure(t *testing.T) {
	failure := errors.New("seed synchronization failure")
	success := func() error { return nil }
	cases := []struct {
		name       string
		operations runtimeSeedSynchronizationOperations
	}{
		{name: "business", operations: runtimeSeedSynchronizationOperations{
			synchronizeBusiness: func() error { return failure }, synchronizeAutomation: success,
		}},
		{name: "automation", operations: runtimeSeedSynchronizationOperations{
			synchronizeBusiness: success, synchronizeAutomation: func() error { return failure },
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := completeRuntimeSeedSynchronization(test.operations); !errors.Is(err, failure) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if err := completeRuntimeSeedSynchronization(runtimeSeedSynchronizationOperations{
		synchronizeBusiness: success, synchronizeAutomation: success,
	}); err != nil {
		t.Fatal(err)
	}
}
