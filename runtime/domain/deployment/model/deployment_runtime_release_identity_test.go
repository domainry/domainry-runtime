package deploymentmodel

import (
	"errors"
	"testing"
)

func TestRuntimeReleaseIdentityErrorEdges(t *testing.T) {
	if _, err := RuntimeRegistrySHA256("v1", make(chan int)); err == nil {
		t.Fatal("unencodable registry descriptors accepted")
	}
	conflict := RuntimeReleaseCohortConflict{Active: RuntimeReleaseIdentity{CombinationSHA256: "active"}, Joining: RuntimeReleaseIdentity{CombinationSHA256: "joining"}}
	if !errors.Is(conflict, ErrRuntimeReleaseConflict) || conflict.Error() == "" {
		t.Fatalf("conflict=%v", conflict)
	}
}
