package runtime

import (
	"testing"

	workerplatform "github.com/domainry/domainry-foundation/worker"
)

func TestProductionWorkerDependenciesCannotEnableFaultInjection(t *testing.T) {
	dependencies, err := newRuntimeWorkerDependencies("runtime-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := dependencies.Faults.(workerplatform.NoopFaultInjector); !ok {
		t.Fatalf("production faults=%T, want NoopFaultInjector", dependencies.Faults)
	}
	if dependencies.IDs == nil || dependencies.Random == nil || dependencies.Clock == nil {
		t.Fatalf("missing injectable dependency: %#v", dependencies)
	}
}
