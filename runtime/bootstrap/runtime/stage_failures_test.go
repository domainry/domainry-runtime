package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/worker"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
)

func TestAssembleRuntimeServicesReportsCancelledInitialization(t *testing.T) {
	cfg := bootstrapTestConfig(t)
	store, err := prepareRuntimeStore(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	model := projectmodel.RuntimeModel{ProjectKey: "test", ContentHash: "model-hash"}
	if _, err := assembleRuntimeServices(cancelled, cfg, model, runtimeext.ProjectDefinitions{}, appschemamodel.IntegrationSchema{}, nil, store, runtimeIdentityProjectionStub{}, nil, nil, worker.Dependencies{}); err == nil {
		t.Fatal("cancelled service initialization must fail assembly")
	}
}

func TestCompleteRuntimeServiceAssemblyPropagatesEveryCompletionStage(t *testing.T) {
	failure := errors.New("service assembly failure")
	success := func() error { return nil }
	configured := false
	if _, err := completeRuntimeServiceAssembly(runtimeServiceAssembly{}, func() error { return failure }, func() { configured = true }); !errors.Is(err, failure) || configured {
		t.Fatalf("completion error=%v configured=%t", err, configured)
	}
	want := runtimeServiceAssembly{}
	if got, err := completeRuntimeServiceAssembly(want, success, func() { configured = true }); err != nil || !configured || got.services != want.services {
		t.Fatalf("successful completion=%#v configured=%t error=%v", got, configured, err)
	}
}
