package provision

import (
	"path/filepath"
	"testing"
)

func TestAuthoringDeliveryRejectsInvalidReadyTransitions(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "runtime.json")
	ready := newConfiguringLifecycle("runtime", "task", "snapshot")
	ready.Status = LifecycleStatusReady
	if err := writeLifecycle(manifestPath, ready); err != nil {
		t.Fatal(err)
	}
	if _, err := transitionAuthoringValidation(manifestPath, "task", "", LifecycleStatusReady); err == nil {
		t.Fatal("ready replay without snapshot hash was accepted")
	}

	configuring := newConfiguringLifecycle("runtime", "task", "snapshot")
	if err := writeLifecycle(manifestPath, configuring); err != nil {
		t.Fatal(err)
	}
	if _, err := transitionAuthoringValidation(manifestPath, "task", "snapshot", LifecycleStatusReady); err == nil {
		t.Fatal("configuring lifecycle entered ready before verification")
	}
}
