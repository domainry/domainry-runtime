package deploymentmodel

import (
	"testing"
)

func TestRuntimeReleaseIdentityErrorEdges(t *testing.T) {
	if _, err := RuntimeRegistrySHA256("v1", make(chan int)); err == nil {
		t.Fatal("unencodable registry descriptors accepted")
	}
}

func TestRuntimeReleaseCoordinationDependsOnProjectIdentityNotReplicaCount(t *testing.T) {
	identity := RuntimeReleaseIdentity{}
	if identity.Coordinated() {
		t.Fatal("empty Generic Runtime identity enabled release coordination")
	}
	identity.ProjectDefinitionRegistrySHA256 = "project-registry-hash"
	if !identity.Coordinated() {
		t.Fatal("project Runtime identity did not enable release coordination")
	}
}
